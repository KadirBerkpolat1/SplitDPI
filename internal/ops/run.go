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
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/nft"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
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

// StopScanError refuses to stop a scan by signal: its trials end with the
// scan, where it runs.
type StopScanError struct{}

func (StopScanError) Error() string { return i18n.T("fix_cekirdek.scan_running") }

// StopHereError refuses to signal this very process: the run it holds the
// lock for is stopped where it runs.
type StopHereError struct{}

func (StopHereError) Error() string { return i18n.T("fix_cekirdek.stop_here") }

// Run is `splitdpi run`: refuse on conflicting tools, then supervise nfqws2
// until ctx is done.
func (o *Ops) Run(ctx context.Context, service bool) error {
	if service {
		return o.run(ctx, "service")
	}
	return o.run(ctx, "foreground")
}

func (o *Ops) run(ctx context.Context, mode string) error {
	if err := o.refuseConflicts(ctx); err != nil {
		return err
	}
	cfg, err := config.Load(o.P)
	if err != nil {
		return err
	}
	log := o.log()
	// Without a journal, the service's output would be lost (OpenRC
	// backgrounds it, runit has no log service): keep it in LogFile. A
	// background run's stderr already is LogFile, so it writes there only,
	// through the rotation.
	if mode == "background" || mode == "service" && sysinfo.DetectInit(o.Root) != sysinfo.Systemd {
		f, err := supervisor.OpenLog(o.P.LogFile)
		if err != nil {
			return err
		}
		defer f.Close()
		if mode == "background" {
			log = f
		} else {
			log = io.MultiWriter(log, f)
		}
	}
	// Until the supervisor holds the run lock, only the engine lock keeps the
	// engine prepare picks from removal.
	release, err := engine.LockShared(o.P)
	if err != nil {
		return err
	}
	defer release()
	j, err := o.prepare(cfg, mode, log, nil)
	if err != nil {
		return err
	}
	// The supervisor spawns only while it holds the run lock: no other run can
	// be using an engine then. The engine lock goes first, or RemoveOld could
	// not take it.
	spawn := j.sup.Spawn
	var once sync.Once
	j.sup.Spawn = func(argv []string) (supervisor.Child, error) {
		once.Do(func() {
			release()
			o.removeOldEngines(log, nil, j.eng)
		})
		return spawn(argv)
	}
	return j.sup.Run(ctx, j.out, j.ruleset, j.meta)
}

type job struct {
	sup     *supervisor.Supervisor
	eng     engine.Engine
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
	var flushDNS func(context.Context, io.Writer)
	dnsPort, dns4Only := 0, false
	if cfg.DNSOn() {
		dns4Only = !dns.IPv6Loopback()
		f := &dns.Forwarder{DNS: o.dnsClient(cfg), Local: &dns.Local{Root: o.Root}, Port: dns.Port, IPv4Only: dns4Only}
		startDNS, flushDNS, dnsPort = f.Start, o.flushDNSCaches, dns.Port
	}
	ruleset, err := nft.Ruleset(nft.RulesetInput{
		Table: o.P.NftTable, QNum: cfg.QNum, TCPPorts: out.TCPPorts, UDPPorts: out.UDPPorts,
		SkipIfaces: cfg.SkipIfaces, DNSRedirectPort: dnsPort, DNSIPv4Only: dns4Only,
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
			StartDNS: startDNS, FlushDNS: flushDNS,
			Ready: func(pid int) bool { return o.queueBound(cfg.QNum, pid) },
		},
		eng:     eng,
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
	if !cfg.DNSOn() {
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

// countryFor is the strategy's country, which selects exclude/<cc>.txt: the
// one saved with it, else that of the ISP preset matching its ASN.
func countryFor(set presets.Set, st *config.Strategy) string {
	if st == nil {
		return ""
	}
	if st.Country != "" {
		return st.Country
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
// country picks the exclude list, as it will for the strategy once saved.
// The caller holds the run lock (supervisor.Lock) until the trial has stopped.
// stop tears everything down: child gone, table deleted.
func (o *Ops) startTrial(ctx context.Context, cfg config.Config, st presets.Strategy, country string, extraHosts []string) (stop func(), err error) {
	cfg.Strategy = &config.Strategy{ID: st.ID, TCPTLS: st.TCPTLS, TCPHTTP: st.TCPHTTP, QUIC: st.QUIC, Country: country}
	j, err := o.prepare(cfg, "scan", io.Discard, extraHosts)
	if err != nil {
		return nil, err
	}
	j.sup.Locked = true
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
	for !o.queueBound(cfg.QNum, 0) {
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

// queueBound reports whether NFQUEUE qnum is bound in our network namespace,
// by pid unless that is 0. The binding's netlink port ID is the PID of the
// process that bound it when, as nfqws2 does, it lets the kernel pick it.
func (o *Ops) queueBound(qnum, pid int) bool {
	b, err := fs.ReadFile(o.Root, "proc/net/netfilter/nfnetlink_queue")
	if err != nil {
		return false
	}
	want, port := strconv.Itoa(qnum), strconv.Itoa(pid)
	for line := range strings.Lines(string(b)) {
		if f := strings.Fields(line); len(f) > 1 && f[0] == want && (pid == 0 || f[1] == port) {
			return true
		}
	}
	return false
}

// Stop stops the running instance and waits up to 10 s for the lock to be
// released. A service is stopped through its manager, so it is not restarted;
// a foreground or background run gets SIGTERM. A scan and a run in this
// process get no signal: StopScanError and StopHereError.
func (o *Ops) Stop() error {
	pid, mode, held := supervisor.LockInfo(o.P)
	switch {
	case !held:
		return ErrNotRunning
	case mode == "scan":
		return StopScanError{}
	case mode == "service":
		m, err := o.manager()
		if err != nil {
			return err
		}
		if err := m.Stop(context.Background()); err != nil {
			return err
		}
	case pid == os.Getpid():
		return StopHereError{}
	default:
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
			return err
		}
	}
	if !o.waitReleased() {
		return ErrStopTimeout
	}
	return nil
}

// Status returns the last recorded status, corrected by the lock, and the
// config. A scan holding the lock reads as running in mode scan, whatever
// its trials last recorded; a status that is on but not of the process
// holding the lock, in its mode, reads as stopped.
func (o *Ops) Status() (supervisor.Status, config.Config, error) {
	st, err := supervisor.ReadStatus(o.P)
	if errors.Is(err, fs.ErrNotExist) {
		st, err = supervisor.Status{State: "stopped"}, nil
	}
	switch pid, mode, held := supervisor.LockInfo(o.P); {
	case held && mode == "scan":
		st = supervisor.Status{State: "running", Mode: mode, PID: pid}
		if fi, err := os.Stat(o.P.LockFile); err == nil {
			st.Since = fi.ModTime() // the lock records its holder when taken
		}
	case st.On() && (!held || st.PID != pid || st.Mode != mode):
		st.State = "stopped"
	}
	cfg, cerr := config.Load(o.P)
	return st, cfg, errors.Join(err, cerr)
}
