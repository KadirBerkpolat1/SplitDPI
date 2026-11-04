package ops

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

// machine fakes PATH and the package manager: the commands in gone are not on
// PATH until an install runs, and fail makes a command ("name arg0") fail.
type machine struct {
	mu       sync.Mutex
	gone     map[string]bool
	fail     map[string]error
	installs bool // a successful install puts the missing commands on PATH
	calls    []string
}

func newMachine(missing ...string) *machine {
	m := &machine{gone: map[string]bool{}, fail: map[string]error{}, installs: true}
	for _, c := range missing {
		m.gone[c] = true
	}
	return m
}

func (m *machine) lookPath(name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gone[name] {
		return "", exec.ErrNotFound
	}
	return "/usr/bin/" + name, nil
}

func (m *machine) run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	line := strings.Join(append([]string{name}, args...), " ")
	m.calls = append(m.calls, line)
	key := name
	if len(args) > 0 {
		key += " " + args[0]
	}
	if err := m.fail[key]; err != nil {
		return nil, err
	}
	installers := []string{"apt-get install", "dnf install", "pacman -S", "zypper --non-interactive", "apk add", "xbps-install -Sy", "emerge --noreplace", "rpm-ostree install"}
	if m.installs && slices.Contains(installers, key) {
		clear(m.gone)
	}
	return nil, nil
}

func pkgOps(t *testing.T, distro string, m *machine) *Ops {
	t.Helper()
	osr, err := os.ReadFile(filepath.Join("..", "sysinfo", "testdata", "os-release", distro))
	if err != nil {
		t.Fatal(err)
	}
	return &Ops{P: paths.Under(t.TempDir()), Root: fstest.MapFS{"etc/os-release": {Data: osr}}, Exec: m.run, LookPath: m.lookPath}
}

func TestMissingPackages(t *testing.T) {
	m := newMachine("curl", "nslookup", "host")
	o := pkgOps(t, "debian13", m)
	p := o.MissingPackages(sysinfo.Curl, sysinfo.Lookup)
	if p.Manager != sysinfo.Apt || !slices.Equal(p.Names, []string{"curl", "bind9-host"}) {
		t.Errorf("got %+v", p)
	}
	// host alone is enough for blockcheck2.
	delete(m.gone, "host")
	if p := o.MissingPackages(); !slices.Equal(p.Names, []string{"curl"}) {
		t.Errorf("with host on PATH: %q", p.Names)
	}
	m.gone["nft"] = true
	if p := o.MissingPackages(); !slices.Equal(p.Names, []string{"nftables", "curl"}) {
		t.Errorf("every tool by default: %q", p.Names)
	}
}

func TestInstallPackages(t *testing.T) {
	m := newMachine("curl", "nslookup", "host")
	o := pkgOps(t, "debian13", m)
	if err := o.InstallPackages(context.Background(), o.MissingPackages()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"apt-get update", "apt-get install -y curl bind9-host"}; !slices.Equal(m.calls, want) {
		t.Errorf("ran %q, want %q", m.calls, want)
	}
	if p := o.MissingPackages(); len(p.Tools) != 0 {
		t.Errorf("still missing %q", p.Names)
	}
}

func TestInstallPackagesErrors(t *testing.T) {
	// Stale or broken package lists do not stop the install.
	m := newMachine("curl")
	m.fail["apt-get update"] = errors.New("apt-get: exit status 100: E: a repository is unreachable")
	o := pkgOps(t, "debian13", m)
	if err := o.InstallPackages(context.Background(), o.MissingPackages()); err != nil {
		t.Errorf("a failed refresh stopped the install: %v", err)
	}

	m = newMachine("curl")
	m.fail["apt-get install"] = errors.New("apt-get: exit status 100: E: Unable to locate package curl")
	o = pkgOps(t, "debian13", m)
	err := o.InstallPackages(context.Background(), o.MissingPackages())
	if err == nil || !strings.Contains(err.Error(), "Unable to locate package curl") || !strings.Contains(err.Error(), "sudo apt install curl") {
		t.Errorf("failed install: %v", err)
	}
	// The TUI words the failure itself and shows only the package manager's error.
	var ie InstallError
	if !errors.As(err, &ie) || ie.Err.Error() != "apt-get: exit status 100: E: Unable to locate package curl" {
		t.Errorf("failed install is not an InstallError: %#v", err)
	}

	// A stale pacman database fails -S with 404s; -S again would too.
	m = newMachine("curl")
	m.fail["pacman -S"] = errors.New("pacman: exit status 1: error: failed retrieving file 'curl-8.17.0-1-x86_64.pkg.tar.zst' : The requested URL returned error: 404")
	o = pkgOps(t, "arch", m)
	if err := o.InstallPackages(context.Background(), o.MissingPackages()); !errors.As(err, &ie) || ie.Hint != "sudo pacman -Syu curl" {
		t.Errorf("pacman failure hint: %v", err)
	}

	// The package manager succeeds but the command is still not there.
	m = newMachine("nslookup", "host")
	m.installs = false
	o = pkgOps(t, "fedora", m)
	err = o.InstallPackages(context.Background(), o.MissingPackages())
	if err == nil || !strings.Contains(err.Error(), "still missing: bind-utils") {
		t.Errorf("tools still missing: %v", err)
	}
}

func TestInstallPackagesUnknownManager(t *testing.T) {
	m := newMachine("curl")
	o := &Ops{P: paths.Under(t.TempDir()), Root: fstest.MapFS{"etc/os-release": {Data: []byte("ID=nixos\n")}}, Exec: m.run, LookPath: m.lookPath}
	p := o.MissingPackages()
	if p.CanInstall() {
		t.Error("an unknown distro installs")
	}
	if err := o.InstallPackages(context.Background(), p); err == nil || !strings.Contains(err.Error(), "curl") || len(m.calls) != 0 {
		t.Errorf("err %v, ran %q", err, m.calls)
	}
}

// blockcheck2 exits at once without curl and nslookup or host, so a scan
// installs them first when allowed and otherwise stops before running it.
func TestScanPackages(t *testing.T) {
	setup := func(t *testing.T) (*Ops, *machine) {
		o := siteScanOps(t)
		osr, err := os.ReadFile("../sysinfo/testdata/os-release/debian13")
		if err != nil {
			t.Fatal(err)
		}
		o.Root.(fstest.MapFS)["etc/os-release"] = &fstest.MapFile{Data: osr}
		m := newMachine("curl")
		o.LookPath = m.lookPath
		nft := o.Exec
		o.Exec = func(ctx context.Context, stdin, name string, args ...string) ([]byte, error) {
			if name == "nft" {
				return nft(ctx, stdin, name, args...)
			}
			return m.run(ctx, stdin, name, args...)
		}
		o.HTTP = &http.Client{Transport: &fakeHTTP{}}
		return o, m
	}
	ranBlockcheck := func(log []string) bool {
		return slices.ContainsFunc(log, func(l string) bool { return strings.HasPrefix(l, "env DOMAINS=") })
	}

	for _, answer := range []func(context.Context, sysinfo.Packages) bool{nil, func(context.Context, sysinfo.Packages) bool { return false }} {
		o, m := setup(t)
		var log []string
		_, err := o.Scan(context.Background(), scan.Options{ASN: 9121, Site: "pastebin.com", Packages: answer}, func(l string) { log = append(log, l) })
		if err == nil || !strings.Contains(err.Error(), "blockcheck2 needs packages that are not installed: curl") || !strings.Contains(err.Error(), "sudo splitdpi deps install") {
			t.Errorf("err %v", err)
		}
		aptRan := slices.ContainsFunc(m.calls, func(c string) bool { return strings.HasPrefix(c, "apt-get") })
		if aptRan || ranBlockcheck(log) {
			t.Errorf("ran %q; log %q", m.calls, log)
		}
	}

	// A failed install stops the scan before blockcheck2, with the reason.
	o, m := setup(t)
	m.fail["apt-get install"] = errors.New("apt-get: exit status 100: E: Unable to locate package curl")
	var log []string
	yes := func(context.Context, sysinfo.Packages) bool { return true }
	_, err := o.Scan(context.Background(), scan.Options{ASN: 9121, Site: "pastebin.com", Packages: yes}, func(l string) { log = append(log, l) })
	var ie InstallError
	if !errors.As(err, &ie) || ranBlockcheck(log) {
		t.Errorf("err %v; log %q", err, log)
	}

	o, m = setup(t)
	var asked []string
	log = nil
	opt := scan.Options{ASN: 9121, Site: "pastebin.com", Packages: func(_ context.Context, p sysinfo.Packages) bool {
		asked = p.Names
		return true
	}}
	rep, err := o.Scan(context.Background(), opt, func(l string) { log = append(log, l) })
	if err != nil || rep.Winner == nil {
		t.Fatalf("winner %v, err %v", rep.Winner, err)
	}
	if !slices.Equal(asked, []string{"curl"}) || !slices.Contains(m.calls, "apt-get install -y curl") {
		t.Errorf("asked %q, ran %q", asked, m.calls)
	}
	installed := slices.Index(log, "Installed: curl")
	if installed < 0 || !ranBlockcheck(log[installed:]) {
		t.Errorf("blockcheck2 did not run after the install: %q", log)
	}
}
