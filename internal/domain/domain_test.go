package domain_test

import (
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/domain"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

func TestNormalizeDomain(t *testing.T) {
	ok := map[string]string{
		"https://Discord.com/app": "discord.com",
		"discord.com.":            "discord.com",
		"*.ziraat.com.tr":         "ziraat.com.tr",
		" Roblox.com:443 ":        "roblox.com",
	}
	for in, want := range ok {
		got, err := domain.Normalize(in)
		if err != nil {
			t.Errorf("Normalize(%q): unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	bad := []string{"", "1.2.3.4", "not a domain", "localhost", "a..b.com"}
	for _, in := range bad {
		if _, err := domain.Normalize(in); err == nil {
			t.Errorf("Normalize(%q): want error", in)
		}
	}
	got, err := domain.Normalize("çiçek.com")
	if err != nil {
		t.Fatalf("Normalize(çiçek.com): %v", err)
	}
	if !strings.HasPrefix(got, "xn--") {
		t.Errorf("Normalize IDN = %q, want xn-- prefix", got)
	}
}

func TestNormalizeErrorsTranslated(t *testing.T) {
	t.Cleanup(func() { i18n.Init("en") })
	i18n.Init("tr")
	_, err := domain.Normalize("not a domain")
	if err == nil || err.Error() != i18n.T("domain.invalid", "not a domain") {
		t.Errorf("invalid: got %v, want %q", err, i18n.T("domain.invalid", "not a domain"))
	}
	_, err = domain.Normalize("  ")
	if err == nil || err.Error() != i18n.T("domain.empty") {
		t.Errorf("empty: got %v, want %q", err, i18n.T("domain.empty"))
	}
}

func TestValidRejectsNonLDH(t *testing.T) {
	for _, s := range []string{
		"x.$(reboot).com", "a;b.com", "a b.com", "a'b.com", "a`id`.com", "a\"b.com", "a&b.com",
		"a_b.com", "Discord.com", "çiçek.com", "-a.com", "a-.com", "a.com-",
	} {
		if domain.Valid(s) {
			t.Errorf("Valid(%q) = true, want false", s)
		}
	}
}

func TestValidAcceptsLDH(t *testing.T) {
	for _, s := range []string{"discord.com", "xn--bcher-kva.example", "a-b.c0m.tr", "1e100.net"} {
		if !domain.Valid(s) {
			t.Errorf("Valid(%q) = false, want true", s)
		}
	}
}
