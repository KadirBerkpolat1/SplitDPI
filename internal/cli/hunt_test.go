package cli

import (
	"slices"
	"testing"
)

// --apps names each app once, in the order given.
func TestAppsFlagDropsRepeats(t *testing.T) {
	want := []string{"discord", "youtube"}
	opt, _, _, err := parseScanArgs([]string{"--apps", "discord,youtube,discord", "--apps", "youtube"})
	if err != nil || !slices.Equal(opt.Apps, want) {
		t.Errorf("scan --apps: %q, %v; want %q", opt.Apps, err, want)
	}
	apps, _, err := parseProbeArgs([]string{"--apps", "discord, youtube,discord"})
	if err != nil || !slices.Equal(apps, want) {
		t.Errorf("probe --apps: %q, %v; want %q", apps, err, want)
	}
}
