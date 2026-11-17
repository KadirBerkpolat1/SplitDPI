package cli

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

func TestPrintProbeNoAddressIsNotAFailure(t *testing.T) {
	res := map[string][]probe.Result{
		"pastebin.com": {{URL: "https://pastebin.com/", OK: true}},
		"rbxcdn.com":   {{URL: "https://rbxcdn.com/", NoAddress: true, Err: "lookup rbxcdn.com: no such host"}},
		"roblox.info":  {{URL: "https://roblox.info/", NoAddress: true}},
	}
	var b bytes.Buffer
	if err := printProbe(&b, res, false); err != nil {
		t.Fatalf("only untested hosts failed: %v", err)
	}
	out := b.String()
	if want := "rbxcdn.com  https://rbxcdn.com/  -  " + probe.NoAddress.What(); !strings.Contains(out, want) {
		t.Errorf("no %q in:\n%s", want, out)
	}
	if strings.Contains(out, "✗") {
		t.Errorf("untested host marked as failed:\n%s", out)
	}
	if want := i18n.T("probe_summary.cli", 1, 0, 2); !strings.HasSuffix(out, want+"\n") {
		t.Errorf("summary %q missing:\n%s", want, out)
	}

	res["discord"] = []probe.Result{{URL: "https://discord.com/", Cause: &net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNRESET)}}}
	b.Reset()
	if err := printProbe(&b, res, false); err == nil {
		t.Fatal("a blocked app exited 0")
	}
	if want := i18n.T("probe_summary.cli", 1, 1, 2); !strings.Contains(b.String(), want) {
		t.Errorf("summary %q missing:\n%s", want, b.String())
	}
}

func TestParseProbeArgs(t *testing.T) {
	apps, verbose, err := parseProbeArgs([]string{"--apps", "discord, telegram", "--verbose"})
	if err != nil || !verbose || !slices.Equal(apps, []string{"discord", "telegram"}) {
		t.Fatalf("got %q, %v, %v", apps, verbose, err)
	}
	if apps, verbose, err := parseProbeArgs(nil); err != nil || verbose || apps != nil {
		t.Errorf("no args: %q, %v, %v", apps, verbose, err)
	}
	for _, args := range [][]string{{"--apps"}, {"--apps", " , "}, {"--bogus"}} {
		if _, _, err := parseProbeArgs(args); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
}

// A name the system resolver says does not exist, and a lookup that
// failed, get the failure mark: only a clean, matching answer is ✓.
func TestProbeCLIMarksDNSFailure(t *testing.T) {
	public := []netip.Addr{netip.MustParseAddr("162.159.128.233")}
	sys := func(_ context.Context, name string) ([]netip.Addr, error) {
		switch name {
		case "discord.com":
			return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
		case "discord.gg":
			return nil, &net.DNSError{Err: "i/o timeout", Name: name, IsTimeout: true}
		}
		return public, nil
	}
	doh := func(context.Context, string) ([]netip.Addr, error) { return public, nil }
	var b bytes.Buffer
	printDNS(&b, probe.CheckDNS(context.Background(), []string{"discord.com", "discord.gg", "pastebin.com"}, sys, doh))
	out := b.String()
	for _, want := range []string{"dns  discord.com  ✗  (", "dns  discord.gg  ✗  (", "dns  pastebin.com  ✓\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
}
