package ops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/nft"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/render"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

// ConfigError is a config.toml problem found while preparing a run.
type ConfigError struct {
	File string
	Err  error
}

func (e ConfigError) Error() string { return fmt.Sprintf("%s: %v", e.File, e.Err) }
func (e ConfigError) Unwrap() error { return e.Err }

var (
	ErrEngineMissing = errors.New("engine not installed")
	ErrNotRunning    = errors.New("not running")
	ErrStopTimeout   = errors.New("splitdpi did not stop within 10 s")
)

// Run is `splitdpi run`: refuse on conflicting tools, then supervise nfqws2
// until ctx is done.
func (o *Ops) Run(ctx context.Context, service bool) error {
	if err := o.refuseConflicts(ctx); err != nil {
		return err
	}
	cfg, err := config.Load(o.P)
	if err != nil {
		return err
	}
	mode, log := "foreground", o.log()
	if service {
		mode = "service"
		// Without a journal, the service's output would be lost (OpenRC
		// backgrounds it, runit has no log service): keep it in LogFile.
		if sysinfo.DetectInit(o.Root) != sysinfo.Systemd {
			f, err := supervisor.OpenLog(o.P.LogFile)
			if err != nil {
				return err
			}
			defer f.Close()
			log = io.MultiWriter(log, f)
		}
	}
	j, err := o.prepare(cfg, mode, log, nil)
	if err != nil {
		return err
	}
	return j.sup.Run(ctx, j.out, j.ruleset, j.meta)
}

type job struct {
	sup     *supervisor.Supervisor
	out     render.Output
	ruleset string
	meta    supervisor.Status
}

// prepare renders everything a supervisor run needs. `run` and scan trials
// share it; extraHosts join the user sites for this run only.
func (o *Ops) prepare(cfg config.Config, mode string, log io.Writer, extraHosts []string) (job, error) {
	set := presets.Current(o.P)
	excl, err := config.ReadList(o.P.UserExclude)
	if err != nil {
		return job{}, err
	}
	hosts, err := config.ReadList(o.P.UserHosts)
	if err != nil {
		return job{}, err
	}
	hosts = append(hosts, extraHosts...)
	eng, installed := engine.Installed(o.P)
	name, uid, gid := pickUser()
	out, err := render.Render(render.Input{
		Engine:      eng,
		Cfg:         cfg,
		Set:         set,
		Country:     countryFor(set, cfg.Strategy),
		UserExclude: excl,
		UserHosts:   hosts,
		P:           o.P,
		User:        name,
	})
	if err != nil {
		return job{}, ConfigError{File: o.P.ConfigFile, Err: err}
	}
	var startDNS func(context.Context) error
	dnsPort := 0
	if cfg.DNS {
		f := &dns.Forwarder{Upstreams: probe.DoHUpstreams, Client: o.HTTP, Port: dns.Port}
		startDNS, dnsPort = f.Start, dns.Port
	}
	ruleset, err := nft.Ruleset(nft.RulesetInput{
		Table: o.P.NftTable, QNum: cfg.QNum, TCPPorts: out.TCPPorts, UDPPorts: out.UDPPorts,
		SkipIfaces: cfg.SkipIfaces, DNSRedirectPort: dnsPort,
	})
	if err != nil {
		return job{}, ConfigError{File: o.P.ConfigFile, Err: err}
	}
	if !installed {
		return job{}, ErrEngineMissing
	}
	spawn := o.Spawn
	if spawn == nil {
		self, err := os.Executable()
		if err != nil {
			return job{}, err
		}
		spawn = supervisor.ExecSpawn(log, self, guardOptions(cfg, o.P)...)
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return job{
		sup: &supervisor.Supervisor{
			P: o.P, Exec: o.Exec, Spawn: spawn, Now: now, Sleep: time.Sleep, Log: log, UID: uid, GID: gid,
			StartDNS: startDNS,
		},
		out:     out,
		ruleset: ruleset,
		meta: supervisor.Status{
			Mode:     mode,
			Strategy: cfg.Strategy.ID,
			Engine:   strings.TrimPrefix(filepath.Base(eng.Dir), "zapret2-"),
			Apps:     cfg.Apps,
		},
	}, nil
}

// guardOptions makes the guard drop the DNS redirect when the supervisor dies:
// its forwarder dies with the supervisor, and no rule of ours may block
// traffic after a crash or kill -9. The queue chains fail open on their own.
func guardOptions(cfg config.Config, p paths.Paths) []string {
	if !cfg.DNS {
		return nil
	}
	return []string{supervisor.DropChainOpt(p.NftTable, nft.DNSChain)}
}

func (o *Ops) log() io.Writer {
	if o.Log == nil {
		return io.Discard
	}
	return o.Log
}

var pickUser = nfqwsUser

// nfqwsUser picks nfqws2's unprivileged user in upstream's order
// (init.d/sysv/functions:42-65) but never creates one: tpws, daemon, nobody, else uid 1:1.
func nfqwsUser() (name string, uid, gid int) {
	for _, n := range []string{"tpws", "daemon", "nobody"} {
		if u, err := user.Lookup(n); err == nil {
			uid, _ := strconv.Atoi(u.Uid)
			gid, _ := strconv.Atoi(u.Gid)
			return n, uid, gid
		}
	}
	return "1:1", 1, 1
}

// countryFor finds the country of the ISP preset matching the strategy's ASN,
// which selects exclude/<cc>.txt. Config has no country of its own.
func countryFor(set presets.Set, st *config.Strategy) string {
	if st == nil {
		return ""
	}
	for _, isp := range set.ISPs {
		if slices.Contains(isp.ASN, st.ASN) {
			return isp.Country
		}
	}
	return ""
}

var trialReadyTimeout = 5 * time.Second

// startTrial runs the supervisor with one candidate strategy, the same way as
// `run`, and returns once nfqws2 has bound its queue. Probing earlier would
// pass traffic through the fail-open queue and test no bypass at all.
// stop tears everything down: child gone, table deleted, lock released.
func (o *Ops) startTrial(ctx context.Context, cfg config.Config, st presets.Strategy, extraHosts []string) (stop func(), err error) {
	cfg.Strategy = &config.Strategy{ID: st.ID, TCPTLS: st.TCPTLS, TCPHTTP: st.TCPHTTP, QUIC: st.QUIC}
	j, err := o.prepare(cfg, "scan", io.Discard, extraHosts)
	if err != nil {
		return nil, err
	}
	spawned := make(chan struct{})
	var once sync.Once
	spawn := j.sup.Spawn
	j.sup.Spawn = func(argv []string) (supervisor.Child, error) {
		c, err := spawn(argv)
		if err == nil {
			once.Do(func() { close(spawned) })
		}
		return c, err
	}

	tctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- j.sup.Run(tctx, j.out, j.ruleset, j.meta) }()
	stop = func() { cancel(); <-done }
	ended := func(err error) error {
		cancel()
		if err == nil {
			err = errors.New("trial supervisor exited early")
		}
		return err
	}

	deadline := time.After(trialReadyTimeout)
	select {
	case err := <-done:
		return nil, ended(err)
	case <-deadline:
		stop()
		return nil, fmt.Errorf("nfqws2 did not start within %s", trialReadyTimeout)
	case <-spawned:
	}
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for !o.queueBound(cfg.QNum) {
		select {
		case err := <-done:
			return nil, ended(err)
		case <-deadline:
			stop()
			return nil, fmt.Errorf("nfqws2 did not bind queue %d within %s", cfg.QNum, trialReadyTimeout)
		case <-tick.C:
		}
	}
	return stop, nil
}

// queueBound reports whether a process has bound NFQUEUE qnum in our network namespace.
func (o *Ops) queueBound(qnum int) bool {
	b, err := fs.ReadFile(o.Root, "proc/net/netfilter/nfnetlink_queue")
	if err != nil {
		return false
	}
	want := strconv.Itoa(qnum)
	for line := range strings.Lines(string(b)) {
		if f := strings.Fields(line); len(f) > 0 && f[0] == want {
			return true
		}
	}
	return false
}

// Stop stops the running instance and waits up to 10 s for the lock to be
// released. A service is stopped through its manager, so it is not restarted.
func (o *Ops) Stop() error {
	pid, mode, held := supervisor.LockInfo(o.P)
	if !held {
		return ErrNotRunning
	}
	if mode == "service" {
		m, err := o.manager()
		if err != nil {
			return err
		}
		if err := m.Stop(context.Background()); err != nil {
			return err
		}
	} else if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	for range 100 {
		if _, _, held := supervisor.LockInfo(o.P); !held {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return ErrStopTimeout
}

// Status returns the last recorded status, corrected to "stopped" when no
// supervisor holds the lock, and the config.
func (o *Ops) Status() (supervisor.Status, config.Config, error) {
	st, err := supervisor.ReadStatus(o.P)
	if errors.Is(err, fs.ErrNotExist) {
		st, err = supervisor.Status{State: "stopped"}, nil
	}
	if _, _, held := supervisor.LockInfo(o.P); !held && st.State == "running" {
		st.State = "stopped"
	}
	cfg, cerr := config.Load(o.P)
	return st, cfg, errors.Join(err, cerr)
}
