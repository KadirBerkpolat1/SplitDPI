package cli

import (
	"bytes"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

func TestDoctorReport(t *testing.T) {
	i18n.Init("en")
	var b bytes.Buffer
	printChecks(&b, []doctor.Check{
		{ID: "root", Msg: "running as root"},
		{ID: "curl", Severity: doctor.Warn, Msg: "curl missing", Fix: "sudo apt install curl"},
		{ID: "nft", Severity: doctor.Blocker, Msg: "nft missing", Fix: "sudo apt install nftables"},
	})
	eq(t, b.String(), "✓ root: running as root\n"+
		"! curl: curl missing\n    fix: sudo apt install curl\n"+
		"✗ nft: nft missing\n    fix: sudo apt install nftables\n")
}
