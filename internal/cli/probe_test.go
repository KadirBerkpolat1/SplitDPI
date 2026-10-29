package cli

import (
	"slices"
	"testing"
)

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
