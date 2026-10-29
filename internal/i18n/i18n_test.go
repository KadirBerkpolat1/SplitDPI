package i18n_test

import (
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

func eq(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func get(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

func TestDetect(t *testing.T) {
	env := map[string]string{"LANG": "tr_TR.UTF-8"}
	eq(t, i18n.Detect("", get(env)), "tr")
	eq(t, i18n.Detect("ru", get(env)), "ru") // flag wins
	env["LC_MESSAGES"] = "ru_RU.UTF-8"
	eq(t, i18n.Detect("", get(env)), "ru")
	env["LC_ALL"] = "en_US.UTF-8"
	eq(t, i18n.Detect("", get(env)), "en")
	eq(t, i18n.Detect("", get(map[string]string{"LANG": "de_DE"})), "en")
}

func TestMissingKey(t *testing.T) {
	i18n.Init("en")
	eq(t, i18n.T("no.such"), "!no.such!")
}

func TestN(t *testing.T) {
	i18n.Init("en")
	for n, want := range map[int]string{
		0: "added 0 new sites from /x",
		1: "added 1 new site from /x",
		2: "added 2 new sites from /x",
	} {
		eq(t, i18n.N("takeover.imported", n, "/x"), want)
	}
	eq(t, i18n.N("no.such", 1), "!no.such_one!")
}
