package sysinfo

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
)

func KernelAtLeast(release string, major, minor int) (bool, error) {
	release = strings.TrimPrefix(release, "Linux ")
	parts := strings.Split(release, ".")
	if len(parts) < 2 {
		return false, nil
	}
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return false, err
	}
	minChunk := parts[1]
	if i := strings.IndexFunc(minChunk, func(r rune) bool {
		return r < '0' || r > '9'
	}); i >= 0 {
		minChunk = minChunk[:i]
	}
	min, err := strconv.Atoi(minChunk)
	if err != nil {
		return false, err
	}
	if maj != major {
		return maj > major, nil
	}
	return min >= minor, nil
}

// HasGlobalIPv6 reports whether this machine has working global IPv6: an IPv6
// default route and a usable (not tentative, deprecated or failed) global address.
func HasGlobalIPv6(ctx context.Context, run execx.Runner) bool {
	out, err := run(ctx, "", "ip", "-6", "route", "show", "default")
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return false
	}
	// BusyBox ip rejects flag filters like -tentative, so the flags are checked here.
	out, err = run(ctx, "", "ip", "-6", "addr", "show", "scope", "global")
	if err != nil {
		return false
	}
	for line := range strings.Lines(string(out)) {
		f := strings.Fields(line)
		if slices.Contains(f, "inet6") && !slices.ContainsFunc(f, func(s string) bool {
			return s == "tentative" || s == "deprecated" || s == "dadfailed"
		}) {
			return true
		}
	}
	return false
}
