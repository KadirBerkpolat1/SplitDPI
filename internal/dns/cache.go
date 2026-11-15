package dns

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
)

// caches are the local resolver caches FlushCaches clears, each with the
// resolver's own command. comm is its process name, cut to 15 bytes as the
// kernel shows it.
var caches = []struct {
	name, comm string
	argv       []string
}{
	{"systemd-resolved", "systemd-resolve", []string{"resolvectl", "flush-caches"}},
	{"nscd", "nscd", []string{"nscd", "-i", "hosts"}},
}

// FlushCaches clears the cache of each local resolver that runs; comms are
// the names of the running processes. Answers cached while DNS went around
// the forwarder, before the redirect loaded or between the stop and the start
// of a restart, can be an ISP's poisoned ones that keep a site on its block
// page. It returns the resolvers it cleared and the errors of the others.
func FlushCaches(ctx context.Context, run execx.Runner, comms []string) ([]string, error) {
	var flushed []string
	var errs []error
	for _, c := range caches {
		if !slices.Contains(comms, c.comm) {
			continue
		}
		if _, err := run(ctx, "", c.argv[0], c.argv[1:]...); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.name, err))
			continue
		}
		flushed = append(flushed, c.name)
	}
	return flushed, errors.Join(errs...)
}
