package probe

import "net/netip"

// KnownBlockIPs are resolver answers that indicate TR-style poisoning: Turk
// Telekom's block page over IPv4 and IPv6.
var KnownBlockIPs = []netip.Addr{
	netip.MustParseAddr("195.175.254.2"),
	netip.MustParseAddr("2a01:358:4014:a00::3"),
}
