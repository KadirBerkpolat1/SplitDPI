// Package doctor checks whether this machine can run splitdpi.
package doctor

import (
	"bytes"
	"context"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

type Severity int

const (
	OK Severity = iota
	Warn
	Blocker
)

type Check struct {
	ID       string
	Severity Severity
	Msg, Fix string
}

// Env is the machine under inspection. Root is the host filesystem ("/").
type Env struct {
	Root     fs.FS
	P        paths.Paths
	Run      execx.Runner
	LookPath func(string) (string, error)
	Procs    func() ([]Proc, error) // every process (/proc/*/stat)
	Euid     func() int
	DNSCheck func(context.Context) []probe.DNSResult // nil skips the dns check
}

type Proc struct {
	PID, PPID int
	Comm      string
	Exe       string // the executable's path; "" when it cannot be read
}

const minMajor, minMinor = 5, 4

const probeTable = "splitdpi_probe"

var probeRuleset = `table inet ` + probeTable + ` {
	chain c {
		type filter hook output priority 0;
		meta mark 0x7fffffff queue num 65000 bypass
	}
}
`

// zapretProcs run under a zapret install, which `splitdpi takeover` can switch off.
var zapretProcs = []string{"nfqws", "nfqws2", "tpws"}

var conflictProcs = slices.Concat(zapretProcs, []string{"ciadpi"})

const fixEngine = "sudo splitdpi engine install"

// Run performs every check in a fixed order.
func Run(ctx context.Context, env Env) []Check {
	pm := sysinfo.DetectPkgManager(osRelease(env.Root), env.Root)
	cs := []Check{root(env), kernel(env)}
	nft := tool(env, "nft", "nftables", Blocker, pm)
	cs = append(cs, nft, tool(env, "curl", "curl", Warn, pm))
	if nft.Severity == OK {
		cs = append(cs, nfqueue(ctx, env))
	}
	cs = append(cs, engineCheck(env), strategyCheck(env), lsm(ctx, env))
	if conf := Conflicts(ctx, env); len(conf) > 0 {
		cs = append(cs, conf...)
	} else {
		cs = append(cs, Check{ID: "conflict", Msg: i18n.T("doctor.conflict.none")})
	}
	cs = append(cs, initCheck(env), ipv6(ctx, env))
	if c := dnsCheck(ctx, env); c != nil {
		cs = append(cs, *c)
	}
	return cs
}

func dnsCheck(ctx context.Context, env Env) *Check {
	if env.DNSCheck == nil {
		return nil
	}
	results := env.DNSCheck(ctx)
	for _, r := range results {
		if r.Poisoned {
			return &Check{
				ID:       "dns",
				Severity: Warn,
				Msg:      i18n.T("doctor.dns.poisoned", r.Domain, r.Reason),
				Fix:      "sudo splitdpi dns on",
			}
		}
	}
	return &Check{ID: "dns", Msg: i18n.T("doctor.dns.ok")}
}

// ExitCode is 1 if any check is a Blocker, else 0.
func ExitCode(cs []Check) int {
	for _, c := range cs {
		if c.Severity == Blocker {
			return 1
		}
	}
	return 0
}

// Conflicts lists other DPI-bypass tools and their leftovers. It returns nothing on a clean system.
func Conflicts(ctx context.Context, env Env) []Check {
	var cs []Check
	names, _ := ConflictProcs(env)
	for _, name := range names {
		c := Check{ID: "conflict", Severity: Blocker, Msg: i18n.T("doctor.conflict.process", name)}
		if slices.Contains(zapretProcs, name) {
			c.Msg, c.Fix = i18n.T("takeover.conflict", name), "sudo splitdpi takeover"
		}
		cs = append(cs, c)
	}
	if len(cs) == 0 {
		for _, dir := range []string{"opt/zapret", "opt/zapret2"} {
			if _, err := fs.Stat(env.Root, dir); err != nil {
				continue
			}
			// Distro packages (e.g. AUR zapret2-bin) ship no uninstall script.
			c := Check{ID: "conflict", Severity: Warn, Msg: i18n.T("doctor.conflict.dir_pkg", "/"+dir)}
			if _, err := fs.Stat(env.Root, dir+"/uninstall_easy.sh"); err == nil {
				c.Msg = i18n.T("doctor.conflict.dir", "/"+dir)
				c.Fix = "sudo /" + dir + "/uninstall_easy.sh"
			}
			cs = append(cs, c)
		}
	}
	if out, err := env.Run(ctx, "", "lsattr", "/etc/resolv.conf"); err == nil {
		if f := strings.Fields(string(out)); len(f) > 0 && strings.Contains(f[0], "i") {
			cs = append(cs, Check{ID: "conflict", Severity: Warn, Msg: i18n.T("doctor.conflict.resolv"), Fix: "sudo chattr -i /etc/resolv.conf"})
		}
	}
	return cs
}

// ConflictProcs returns the names of the other DPI-bypass processes running,
// leaving out splitdpi's own nfqws2.
func ConflictProcs(env Env) ([]string, error) {
	procs, err := env.Procs()
	ours := ownProcess(env.P, procs)
	var names []string
	for _, name := range conflictProcs {
		if slices.ContainsFunc(procs, func(p Proc) bool { return p.Comm == name && !ours(p) }) {
			names = append(names, name)
		}
	}
	return names, err
}

// ownProcess matches splitdpi's own nfqws2: one run from its engine dir (as
// blockcheck2 does during a scan, without the lock), and while a supervisor
// holds the lock, the child PID in status.json or any descendant of the lock
// holder (nfqws2 runs under a guard process). A stale lock or status file
// matches nothing.
func ownProcess(p paths.Paths, procs []Proc) func(Proc) bool {
	fromEngine := func(pr Proc) bool { return strings.HasPrefix(pr.Exe, p.EngineDir+"/") }
	pid, _, held := supervisor.LockInfo(p)
	if !held {
		return fromEngine
	}
	st, _ := supervisor.ReadStatus(p)
	parent := make(map[int]int, len(procs))
	for _, pr := range procs {
		parent[pr.PID] = pr.PPID
	}
	return func(pr Proc) bool {
		if fromEngine(pr) || st.ChildPID != 0 && pr.PID == st.ChildPID {
			return true
		}
		for a, depth := pr.PPID, 0; a > 1 && depth < len(procs); a, depth = parent[a], depth+1 {
			if a == pid {
				return true
			}
		}
		return false
	}
}

// ParseProcStat reads pid, comm and ppid from a /proc/<pid>/stat line.
// comm sits in parentheses and may itself contain spaces and ")".
func ParseProcStat(s string) (Proc, bool) {
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return Proc{}, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(s[:open]))
	if err != nil {
		return Proc{}, false
	}
	f := strings.Fields(s[end+1:]) // state ppid ...
	if len(f) < 2 {
		return Proc{}, false
	}
	ppid, err := strconv.Atoi(f[1])
	if err != nil {
		return Proc{}, false
	}
	return Proc{PID: pid, PPID: ppid, Comm: s[open+1 : end]}, true
}

func osRelease(root fs.FS) sysinfo.OSRelease {
	for _, p := range []string{"etc/os-release", "usr/lib/os-release"} {
		if b, err := fs.ReadFile(root, p); err == nil {
			o, _ := sysinfo.ParseOSRelease(bytes.NewReader(b))
			return o
		}
	}
	return sysinfo.OSRelease{}
}

func root(env Env) Check {
	if env.Euid() != 0 {
		return Check{ID: "root", Severity: Blocker, Msg: i18n.T("doctor.root.no"), Fix: "sudo splitdpi doctor"}
	}
	return Check{ID: "root", Msg: i18n.T("doctor.root.ok")}
}

func kernel(env Env) Check {
	b, err := fs.ReadFile(env.Root, "proc/sys/kernel/osrelease")
	if err != nil {
		return Check{ID: "kernel", Severity: Warn, Msg: i18n.T("doctor.kernel.unknown", err)}
	}
	rel := strings.TrimSpace(string(b))
	if ok, err := sysinfo.KernelAtLeast(rel, minMajor, minMinor); err != nil || !ok {
		return Check{ID: "kernel", Severity: Blocker, Msg: i18n.T("doctor.kernel.old", rel, minMajor, minMinor)}
	}
	return Check{ID: "kernel", Msg: i18n.T("doctor.kernel.ok", rel)}
}

func tool(env Env, name, pkg string, missing Severity, pm sysinfo.PkgManager) Check {
	if p, err := env.LookPath(name); err == nil {
		return Check{ID: name, Msg: i18n.T("doctor.tool.ok", name, p)}
	}
	return Check{ID: name, Severity: missing, Msg: i18n.T("doctor.tool.missing." + name), Fix: sysinfo.InstallHint(pm, pkg)}
}

// nfqueue loads a throwaway queue rule to prove the kernel supports NFQUEUE.
// The probe table is deleted whatever happens, even if ctx is cancelled.
func nfqueue(ctx context.Context, env Env) Check {
	defer env.Run(context.WithoutCancel(ctx), "", "nft", "delete", "table", "inet", probeTable)
	if _, err := env.Run(ctx, probeRuleset, "nft", "-f", "-"); err != nil {
		return Check{ID: "nfqueue", Severity: Blocker, Msg: i18n.T("doctor.nfqueue.fail", err), Fix: "sudo modprobe -a nft_queue nfnetlink_queue"}
	}
	return Check{ID: "nfqueue", Msg: i18n.T("doctor.nfqueue.ok")}
}

func strategyCheck(env Env) Check {
	c, err := config.Load(env.P)
	if err != nil || c.Strategy == nil {
		return Check{ID: "strategy", Msg: i18n.T("doctor.strategy.ok")}
	}
	if c.Strategy.Engine != engine.Version {
		return Check{
			ID:       "strategy",
			Severity: Warn,
			Msg:      i18n.T("doctor.strategy.old", c.Strategy.Engine, engine.Version),
			Fix:      "sudo splitdpi scan",
		}
	}
	return Check{ID: "strategy", Msg: i18n.T("doctor.strategy.ok")}
}

func engineCheck(env Env) Check {
	e, ok := engine.Installed(env.P)
	if !ok {
		return Check{ID: "engine", Severity: Blocker, Msg: i18n.T("doctor.engine.missing"), Fix: fixEngine}
	}
	if want := "zapret2-" + engine.Version; filepath.Base(e.Dir) != want {
		return Check{ID: "engine", Severity: Warn, Msg: i18n.T("doctor.engine.old", filepath.Base(e.Dir), engine.Version), Fix: fixEngine}
	}
	return Check{ID: "engine", Msg: i18n.T("doctor.engine.ok", engine.Version, e.Dir)}
}

func lsm(ctx context.Context, env Env) Check {
	l := sysinfo.DetectLSM(env.Root)
	if l.SELinux != "enforcing" {
		return Check{ID: "lsm", Msg: i18n.T("doctor.lsm.ok", l.SELinux, l.AppArmor)}
	}
	n, src, err := selinuxDenials(ctx, env)
	switch {
	case err != nil:
		return Check{ID: "lsm", Severity: Warn, Msg: i18n.T("doctor.lsm.enforcing_nolog", err)}
	case n > 0:
		return Check{ID: "lsm", Severity: Warn, Msg: i18n.N("doctor.lsm.enforcing", n, src)}
	}
	return Check{ID: "lsm", Msg: i18n.T("doctor.lsm.enforcing_clean", src)}
}

const auditLog = "var/log/audit/audit.log"

// selinuxDenials counts AVC denials for nfqws2/splitdpi. With auditd running
// they go only to the audit log, so dmesg is the fallback.
func selinuxDenials(ctx context.Context, env Env) (int, string, error) {
	if b, err := fs.ReadFile(env.Root, auditLog); err == nil {
		n := 0
		for line := range strings.Lines(string(b)) {
			ours := strings.Contains(line, `comm="nfqws2"`) || strings.Contains(line, `comm="splitdpi"`)
			if ours && strings.Contains(line, "type=AVC") && strings.Contains(line, "denied") {
				n++
			}
		}
		return n, "/" + auditLog, nil
	}
	out, err := env.Run(ctx, "", "dmesg")
	if err != nil {
		return 0, "", err
	}
	n := 0
	for line := range strings.Lines(string(out)) {
		ours := strings.Contains(line, "nfqws2") || strings.Contains(line, "splitdpi")
		denial := strings.Contains(line, "avc:") || strings.Contains(line, "DENIED")
		if ours && denial {
			n++
		}
	}
	return n, "dmesg", nil
}

func initCheck(env Env) Check {
	in := sysinfo.DetectInit(env.Root)
	if in == sysinfo.OtherInit {
		return Check{ID: "init", Severity: Warn, Msg: i18n.T("doctor.init.other")}
	}
	return Check{ID: "init", Msg: i18n.T("doctor.init.ok", string(in))}
}

func ipv6(ctx context.Context, env Env) Check {
	if sysinfo.HasGlobalIPv6(ctx, env.Run) {
		return Check{ID: "ipv6", Msg: i18n.T("doctor.ipv6.yes")}
	}
	return Check{ID: "ipv6", Msg: i18n.T("doctor.ipv6.no")}
}
