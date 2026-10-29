package cli

import (
	"bytes"
	"net"
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
