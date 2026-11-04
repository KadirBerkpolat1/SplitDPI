package cli

import (
	"context"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

// debianRoot is a Debian 13 machine, merged with extra.
func debianRoot(t *testing.T, extra fstest.MapFS) fstest.MapFS {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "sysinfo", "testdata", "os-release", "debian13"))
	if err != nil {
		t.Fatal(err)
	}
	root := fstest.MapFS{"etc/os-release": {Data: data}}
	maps.Copy(root, extra)
	return root
}

// missingOps is a machine on root without the commands in missing; the
// returned set is what PATH lacks, so a test can empty it when a package
// manager installs them.
func missingOps(t *testing.T, root fstest.MapFS, missing ...string) (*ops.Ops, *fakeNft, map[string]bool) {
	o, f := runOpsFor(t, root)
	gone := map[string]bool{}
	for _, m := range missing {
		gone[m] = true
	}
	o.LookPath = func(name string) (string, error) {
		if gone[name] {
			return "", exec.ErrNotFound
		}
		return "/usr/bin/" + name, nil
	}
	return o, f, gone
}

func setPrompt(t *testing.T, tty bool, input string) {
	t.Helper()
	oldIn, oldTTY := promptIn, promptTTY
	t.Cleanup(func() { promptIn, promptTTY = oldIn, oldTTY })
	promptIn, promptTTY = strings.NewReader(input), func() bool { return tty }
}

func notHas(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("%q contains %q", s, sub)
	}
}

const (
	aptUpdate  = "apt-get update"
	aptInstall = "apt-get install -y curl bind9-host"
)

func TestDepsNoneMissing(t *testing.T) {
	o, f, _ := missingOps(t, debianRoot(t, nil))
	for _, args := range [][]string{{"deps"}, {"deps", "install"}, {"deps", "install", "--yes"}} {
		out, code := runOps(t, o, args...)
		eq(t, code, 0)
		has(t, out, "Every package splitdpi needs is installed.")
	}
	eq(t, len(f.calls), 0)
}

func TestDepsShowsPlan(t *testing.T) {
	o, f, _ := missingOps(t, debianRoot(t, nil), "curl", "nslookup", "host")
	out, code := runOps(t, o, "deps")
	eq(t, code, 0)
	has(t, out, "Missing packages:\n"+
		"  curl: scan (blockcheck2) tests sites with curl\n"+
		"  bind9-host: scan (blockcheck2) checks DNS with nslookup or host\n"+
		"Runs: "+aptUpdate+"; "+aptInstall+"\n"+
		"To remove them later: sudo apt remove curl bind9-host\n"+
		"To install them: sudo splitdpi deps install\n")
	eq(t, len(f.calls), 0)
}

func TestDepsInstallWithoutTTYOnlyHints(t *testing.T) {
	setPrompt(t, false, "y\n")
	o, f, _ := missingOps(t, debianRoot(t, nil), "nft")
	out, code := runOps(t, o, "deps", "install")
	eq(t, code, 0)
	has(t, out, "  nftables: splitdpi adds its firewall rules with nft")
	has(t, out, "To install them without a question, run: sudo splitdpi deps install --yes")
	notHas(t, out, "[y/N]")
	eq(t, len(f.calls), 0)
}

func TestDepsInstallYes(t *testing.T) {
	setPrompt(t, true, "n\n")
	o, f, gone := missingOps(t, debianRoot(t, nil), "curl", "nslookup", "host")
	f.hooks = map[string]func(){aptInstall: func() { clear(gone) }}
	out, code := runOps(t, o, "deps", "install", "--yes")
	eq(t, code, 0)
	notHas(t, out, "[y/N]")
	has(t, out, "Installing curl, bind9-host with apt...")
	has(t, out, "Installed: curl, bind9-host")
	eq(t, strings.Join(f.calls, "\n"), aptUpdate+"\n"+aptInstall)
}

func TestDepsInstallAsks(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		installed   bool
	}{
		{"yes", "y\n", true},
		{"no", "n\n", false},
		{"default no", "\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setPrompt(t, true, tc.input)
			o, f, gone := missingOps(t, debianRoot(t, nil), "curl")
			f.hooks = map[string]func(){"apt-get install -y curl": func() { clear(gone) }}
			out, code := runOps(t, o, "deps", "install")
			eq(t, code, 0)
			has(t, out, "Install them now? [y/N]")
			eq(t, f.saw("apt-get install -y curl"), tc.installed)
			if tc.installed {
				has(t, out, "Installed: curl")
			} else {
				has(t, out, "Nothing was installed.")
			}
		})
	}
}

func TestDepsInstallUnknownDistro(t *testing.T) {
	setPrompt(t, true, "y\n")
	o, f, _ := missingOps(t, fstest.MapFS{"etc/os-release": {Data: []byte("ID=nixos\n")}}, "nft")
	out, code := runOps(t, o, "deps", "install", "--yes")
	eq(t, code, 1)
	has(t, out, "Missing packages:\n  nftables: splitdpi adds its firewall rules with nft")
	has(t, out, "No supported package manager found. Install these yourself: nftables")
	notHas(t, out, "Runs:")
	eq(t, len(f.calls), 0)

	out, code = runOps(t, o, "deps")
	eq(t, code, 0)
	has(t, out, "No supported package manager found.")
	notHas(t, out, "sudo splitdpi deps install")
}

func TestDepsInstallErrorShowsHint(t *testing.T) {
	o, _, _ := missingOps(t, debianRoot(t, nil), "curl")
	run := o.Exec
	o.Exec = func(ctx context.Context, stdin, name string, args ...string) ([]byte, error) {
		if name == "apt-get" && args[0] == "install" {
			return nil, errors.New("apt-get: exit status 100: E: Unable to locate package curl")
		}
		return run(ctx, stdin, name, args...)
	}
	out, code := runOps(t, o, "deps", "install", "--yes")
	eq(t, code, 1)
	has(t, out, "Could not install the packages: apt-get: exit status 100: E: Unable to locate package curl")
	has(t, out, "Install them yourself: sudo apt install curl")
	notHas(t, out, "Installed:")
}

func TestDepsInstallFailureExits1(t *testing.T) {
	o, f, _ := missingOps(t, debianRoot(t, nil), "curl")
	out, code := runOps(t, o, "deps", "install", "--yes")
	eq(t, code, 1)
	if !f.saw("apt-get install -y curl") {
		t.Errorf("apt-get not run; calls %q", f.calls)
	}
	has(t, out, "still missing: curl")
	notHas(t, out, "Installed:")
}

func TestDepsUsage(t *testing.T) {
	for _, args := range [][]string{{"deps", "--yes"}, {"deps", "add"}, {"deps", "install", "-y"}, {"deps", "install", "--yes", "x"}} {
		out, code := run(t, 0, args...)
		eq(t, code, 2)
		has(t, out, "splitdpi deps [install [--yes]]")
	}
}

func TestDoctorPointsToDeps(t *testing.T) {
	o, _, _ := missingOps(t, debianRoot(t, nil), "curl")
	out, _ := runOps(t, o, "doctor")
	has(t, out, "Install the missing packages in one go: sudo splitdpi deps install")

	o, _, _ = missingOps(t, debianRoot(t, nil))
	out, _ = runOps(t, o, "doctor")
	notHas(t, out, "deps install")
}
