package presets_test

import (
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/presets"
)

// No shipped app may cover Discord voice and Go Live, Twitch streams or the
// Twitch emote services, nor carry a UDP rule: a UDP rule ignores hostnames,
// so no exclusion can turn it off.
func TestShippedAppsExcludeDiscordVoiceAndStream(t *testing.T) {
	s := presets.Load(presets.Embedded())
	if len(s.Warnings) != 0 || len(s.Apps) == 0 {
		t.Fatalf("%d apps, warnings: %v", len(s.Apps), s.Warnings)
	}
	banned := []string{"discord.media", "live-video.net", "7tv.app", "7tv.io", "betterttv.net", "frankerfacez.com", "ffzap.com"}
	for id, a := range s.Apps {
		for _, h := range a.Hosts {
			for _, b := range banned {
				if h == b || strings.HasSuffix(h, "."+b) || strings.HasSuffix(b, "."+h) {
					t.Errorf("%s: host %q covers %q", id, h, b)
				}
			}
		}
		if a.UDP != nil {
			t.Errorf("%s: UDP rule on ports %s, l7 %v", id, a.UDP.Ports, a.UDP.L7)
		}
	}
}
