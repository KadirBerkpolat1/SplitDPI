// Package render turns config and presets into the nfqws2 argv and list files.
package render

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

type Input struct {
	Engine                 engine.Engine
	Cfg                    config.Config
	Set                    presets.Set
	Country                string
	UserExclude, UserHosts []string
	P                      paths.Paths
	User                   string // passwd name, or "uid:gid" for --uid
}

type Output struct {
	Argv                      []string // Argv[0] is the nfqws2 binary
	Hostlist, Exclude         []string
	HostlistFile, ExcludeFile string
	TCPPorts, UDPPorts        []string
}

var (
	ErrNoStrategy = errors.New("no strategy configured")
	ErrNoApps     = errors.New(`mode "apps" needs at least one enabled app or host`)
)

type ErrUnknownApp struct{ ID string }

func (e ErrUnknownApp) Error() string { return fmt.Sprintf("unknown app %q", e.ID) }

type ErrBadStrategy struct {
	Field string
	Err   error
}

func (e ErrBadStrategy) Error() string { return fmt.Sprintf("strategy %s: %v", e.Field, e.Err) }
func (e ErrBadStrategy) Unwrap() error { return e.Err }

// nfqws2 loads --lua-init globally, not per profile.
var luaInits = []string{"zapret-lib.lua", "zapret-antidpi.lua", "zapret-auto.lua"}

// Render builds the profiles in this order: app UDP, app QUIC, app
// TLS/HTTP, broad. The broad (autohostlist) profile must be last: it wins
// every connection with a known hostname on its ports.
func Render(in Input) (Output, error) {
	st := in.Cfg.Strategy
	if st == nil {
		return Output{}, ErrNoStrategy
	}
	if err := presets.ValidateArgs(st.TCPTLS); err != nil {
		return Output{}, ErrBadStrategy{Field: "tcp_tls", Err: err}
	}
	for _, f := range [...]struct{ name, val string }{{"tcp_http", st.TCPHTTP}, {"quic", st.QUIC}} {
		if f.val == "" {
			continue
		}
		if err := presets.ValidateArgs(f.val); err != nil {
			return Output{}, ErrBadStrategy{Field: f.name, Err: err}
		}
	}

	apps := make([]presets.App, 0, len(in.Cfg.Apps))
	var appHosts []string
	quic := false
	for _, id := range in.Cfg.Apps {
		a, ok := in.Set.Apps[id]
		if !ok {
			return Output{}, ErrUnknownApp{ID: id}
		}
		apps = append(apps, a)
		appHosts = append(appHosts, a.Hosts...)
		quic = quic || a.QUIC
	}

	out := Output{
		HostlistFile: in.P.HostlistFile,
		ExcludeFile:  in.P.ExcludeFile,
		Hostlist:     sortedUnique(slices.Concat(appHosts, in.UserHosts)),
	}
	broad := in.Cfg.Mode != config.ModeApps
	if !broad && len(out.Hostlist) == 0 {
		return Output{}, ErrNoApps
	}

	// Shipped exclusions must not cancel an enabled app or a user site; the
	// user's exclusions always win.
	var ex []string
	for _, d := range in.Set.ExcludeFor(in.Country) {
		if !slices.ContainsFunc(out.Hostlist, func(h string) bool { return related(d, h) }) {
			ex = append(ex, d)
		}
	}
	out.Exclude = sortedUnique(slices.Concat(ex, in.UserExclude))

	tcp := strings.Split(st.TCPTLS, " ")
	tcpFilter := []string{"--filter-tcp=443", "--filter-l7=tls"}
	out.TCPPorts = []string{"443"}
	if st.TCPHTTP != "" {
		tcp = append(tcp, strings.Split(st.TCPHTTP, " ")...)
		tcpFilter = []string{"--filter-tcp=443,80", "--filter-l7=tls,http"}
		out.TCPPorts = append(out.TCPPorts, "80")
	}
	exclude := "--hostlist-exclude=" + out.ExcludeFile
	hostlist := "--hostlist=" + out.HostlistFile

	var profiles [][]string
	for _, a := range apps {
		if a.UDP == nil {
			continue
		}
		profiles = append(profiles, slices.Concat(
			[]string{"--filter-udp=" + a.UDP.Ports, "--filter-l7=" + strings.Join(a.UDP.L7, ","), exclude},
			strings.Split(a.UDP.Desync, " ")))
		out.UDPPorts = append(out.UDPPorts, strings.Split(a.UDP.Ports, ",")...)
	}
	// An empty --hostlist file must never be rendered, so host-bound profiles need hosts.
	if quic && st.QUIC != "" && len(out.Hostlist) > 0 {
		profiles = append(profiles, slices.Concat(
			[]string{"--filter-udp=443", "--filter-l7=quic", hostlist, exclude},
			strings.Split(st.QUIC, " ")))
		out.UDPPorts = append([]string{"443"}, out.UDPPorts...)
	}
	if len(out.Hostlist) > 0 {
		profiles = append(profiles, slices.Concat(tcpFilter, []string{hostlist, exclude}, tcp))
	}
	if broad {
		profiles = append(profiles, slices.Concat(tcpFilter, []string{"--hostlist-auto=" + in.P.AutoHostlist, exclude}, tcp))
	}
	out.UDPPorts = uniqueInOrder(out.UDPPorts)

	argv := []string{in.Engine.Nfqws2(), "--qnum=" + strconv.Itoa(in.Cfg.QNum)}
	if strings.Contains(in.User, ":") {
		argv = append(argv, "--uid="+in.User)
	} else if in.User != "" {
		argv = append(argv, "--user="+in.User)
	}
	for _, f := range luaInits {
		argv = append(argv, "--lua-init=@"+filepath.Join(in.Engine.LuaDir(), f))
	}
	for i, p := range profiles {
		if i > 0 {
			argv = append(argv, "--new")
		}
		argv = append(argv, p...)
	}
	out.Argv = argv
	return out, nil
}

// related reports whether one domain equals the other or is its subdomain;
// nfqws2 lists match subdomains, so either way the exclusion would hit the app.
func related(a, b string) bool {
	return a == b || strings.HasSuffix(a, "."+b) || strings.HasSuffix(b, "."+a)
}

func sortedUnique(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return slices.Compact(s)
}

func uniqueInOrder(s []string) []string {
	var out []string
	for _, v := range s {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
