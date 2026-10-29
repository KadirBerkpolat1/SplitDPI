//go:build integration

package integration_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

// The next three tests share one run: start, kill -9, revert. They run in file order.

func TestRunPassesTrafficAndQueues(t *testing.T) {
	var err error
	if runCmd, err = startRun(shared, "run"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "status running", func() bool { return state(shared) == "running" })
	if err := curl(); err != nil {
		t.Fatalf("curl through splitdpi: %v", err)
	}
	if n := queuedPackets(t); n == 0 {
		t.Error("no packet went through the queue")
	}
}

func TestKill9FailsOpen(t *testing.T) {
	if runCmd == nil {
		t.Fatal("needs the run started by TestRunPassesTrafficAndQueues")
	}
	sup, ok := findComm(descendants(runCmd.Process.Pid), "splitdpi")
	if !ok {
		t.Fatal("supervisor process not found")
	}
	if _, ok := findComm(descendants(sup.PID), "nfqws2"); !ok {
		t.Fatal("nfqws2 is not running under the supervisor")
	}
	if err := syscall.Kill(sup.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, "nfqws2 to die with its parent (Pdeathsig)", func() bool {
		_, alive := findComm(descendants(runCmd.Process.Pid), "nfqws2")
		return !alive
	})
	if !tableExists() {
		t.Error("table vanished; kill -9 must leave it for revert")
	}
	if err := curl(); err != nil {
		t.Errorf("queue did not fail open: %v", err)
	}
}

func TestRevertClean(t *testing.T) {
	stopRun()
	out, code := splitdpi(shared, "revert")
	if code != 0 || !strings.Contains(out, "clean") {
		t.Fatalf("revert exit %d:\n%s", code, out)
	}
	if tableExists() {
		t.Error("table still present after revert")
	}
	if _, err := os.Stat(paths.Under(shared).RunDir); !os.IsNotExist(err) {
		t.Errorf("run dir still present: %v", err)
	}
}

// With dns = true, a query to a public resolver inside sdpi-cli is redirected
// to the forwarder, which answers it over DoH from the fake upstream in
// sdpi-srv. revert removes the redirect with the table.
func TestDNSRedirectViaForwarder(t *testing.T) {
	if got, want := dns.Upstreams(dns.Auto, dns.DoH)[0].URL(), "https://"+dohIP+"/dns-query"; got != want {
		t.Fatalf("first DoH upstream is %s; the fake upstream serves %s", got, want)
	}
	if _, err := dig("9.9.9.9", "discord.com"); err == nil {
		t.Fatal("9.9.9.9 answered before splitdpi ran; the test network is not isolated")
	}
	root, err := makeRoot("dns = true")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	cmd, err := startRun(root, "run")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	waitFor(t, 5*time.Second, "status running", func() bool { return state(root) == "running" })

	out, err := sh("ip", "netns", "exec", cliNS, "nft", "list", "table", "inet", "splitdpi")
	if err != nil || !strings.Contains(out, "redirect to :5453") {
		t.Fatalf("no DNS redirect in the table (%v):\n%s", err, out)
	}
	if got, err := dig("9.9.9.9", "discord.com"); err != nil || got != dohAnswer {
		t.Fatalf("dig @9.9.9.9 discord.com = %q, %v; want %s from the DoH upstream", got, err, dohAnswer)
	}

	cmd.Process.Kill()
	cmd.Wait()
	if out, code := splitdpi(root, "revert"); code != 0 || !strings.Contains(out, "clean") {
		t.Fatalf("revert exit %d:\n%s", code, out)
	}
	if tableExists() {
		t.Error("table, and with it the DNS redirect, still present after revert")
	}
	if got, err := dig("9.9.9.9", "discord.com"); err == nil {
		t.Errorf("dig still answered after revert: %q", got)
	}
}

// After kill -9 nothing of ours may block traffic. The forwarder dies
// with the supervisor, so the guard must drop the redirect; the queue chains
// stay and fail open as before.
func TestDNSFailsOpenAfterKill9(t *testing.T) {
	root, err := makeRoot("dns = true")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	cmd, err := startRun(root, "run")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		splitdpi(root, "revert")
	})
	waitFor(t, 5*time.Second, "status running", func() bool { return state(root) == "running" })
	table := func() string {
		out, _ := sh("ip", "netns", "exec", cliNS, "nft", "list", "table", "inet", "splitdpi")
		return out
	}
	if !strings.Contains(table(), "chain dns") {
		t.Fatalf("no DNS redirect chain while running:\n%s", table())
	}

	sup, ok := findComm(descendants(cmd.Process.Pid), "splitdpi")
	if !ok {
		t.Fatal("supervisor process not found")
	}
	if err := syscall.Kill(sup.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, "the DNS redirect chain to go", func() bool { return !strings.Contains(table(), "chain dns") })
	if out := table(); !strings.Contains(out, "chain postnat") || !strings.Contains(out, "chain prenat") {
		t.Errorf("queue chains gone too; they must stay for revert and fail open:\n%s", out)
	}
	if err := curl(); err != nil {
		t.Errorf("traffic blocked after kill -9: %v", err)
	}
}

func TestCrashLoopRemovesTable(t *testing.T) {
	root, err := makeRoot("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	eng, ok := engine.Installed(paths.Under(root))
	if !ok {
		t.Fatal("engine not installed")
	}
	if err := os.WriteFile(eng.Nfqws2(), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ip", "netns", "exec", cliNS,
		"unshare", "--pid", "--fork", "--mount-proc", "--kill-child", bin, "run", "--service")
	cmd.Env = envFor(root)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("got %v, want exit 3:\n%s", err, out)
	}
	if d := time.Since(start); d > 45*time.Second {
		t.Errorf("crash loop took %s", d)
	}
	if tableExists() {
		t.Error("table left after crash loop")
	}
}
