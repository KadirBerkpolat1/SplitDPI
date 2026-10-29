package scan_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/scan"
)

// SUMMARY is the last thing blockcheck2 prints: output written just before
// the script exits must never be lost.
func TestRunBlockcheckKeepsLastLines(t *testing.T) {
	dir := t.TempDir()
	script := "echo first\necho\necho '* SUMMARY'\necho last\n"
	if err := os.WriteFile(filepath.Join(dir, "blockcheck2.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := &nftRecorder{}
	for i := range 200 {
		log, err := scan.RunBlockcheck(context.Background(), engine.Engine{Dir: dir, ArchDir: "linux-x86_64"},
			rec.run, scan.BlockcheckParams{Domains: []string{"example.com"}, IPVS: "4"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(log, "* SUMMARY\nlast\n") {
			t.Fatalf("run %d lost the end of the output: %q", i, log)
		}
	}
}
