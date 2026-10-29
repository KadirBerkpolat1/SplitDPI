package probe

import "net/netip"

// KnownBlockIPs are resolver answers that indicate TR-style poisoning.
var KnownBlockIPs = []netip.Addr{netip.MustParseAddr("195.175.254.2")}
