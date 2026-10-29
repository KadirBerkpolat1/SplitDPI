package execx_test

import (
	"context"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
)

func TestRealStdinStdout(t *testing.T) {
	out, err := execx.Real(context.Background(), "hi", "cat")
	if err != nil || string(out) != "hi" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestRealErrorCarriesStderr(t *testing.T) {
	_, err := execx.Real(context.Background(), "", "sh", "-c", "echo boom >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v", err)
	}
}
