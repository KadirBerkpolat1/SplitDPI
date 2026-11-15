package dns_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
)

func TestFlushCaches(t *testing.T) {
	for _, tc := range []struct {
		name    string
		comms   []string
		fail    string // the command that fails
		ran     []string
		flushed []string
		err     string
	}{
		{name: "no caching resolver", comms: []string{"systemd", "sshd"}},
		// The kernel cuts process names to 15 bytes: systemd-resolved runs as systemd-resolve.
		{name: "systemd-resolved", comms: []string{"systemd", "systemd-resolve"},
			ran: []string{"resolvectl flush-caches"}, flushed: []string{"systemd-resolved"}},
		{name: "nscd", comms: []string{"nscd"}, ran: []string{"nscd -i hosts"}, flushed: []string{"nscd"}},
		{name: "one fails, the other is still cleared", comms: []string{"systemd-resolve", "nscd"}, fail: "resolvectl",
			ran: []string{"resolvectl flush-caches", "nscd -i hosts"}, flushed: []string{"nscd"}, err: "systemd-resolved: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ran []string
			run := func(_ context.Context, _, name string, args ...string) ([]byte, error) {
				ran = append(ran, strings.Join(append([]string{name}, args...), " "))
				if name == tc.fail {
					return nil, errors.New("exit status 1")
				}
				return nil, nil
			}
			flushed, err := dns.FlushCaches(context.Background(), run, tc.comms)
			if !slices.Equal(ran, tc.ran) || !slices.Equal(flushed, tc.flushed) {
				t.Errorf("ran %q, flushed %q", ran, flushed)
			}
			switch {
			case tc.err == "" && err != nil:
				t.Errorf("err %v", err)
			case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
				t.Errorf("err %v, want %q", err, tc.err)
			}
		})
	}
}
