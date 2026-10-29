package supervisor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

func TestLogFileRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "var/log/splitdpi.log")
	l, err := supervisor.OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 1023) + "\n"
	for range 1536 { // 1.5 MiB
		if _, err := l.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() > 1<<20 {
		t.Errorf("log %v bytes, %v; want ≤ 1 MiB", fi.Size(), err)
	}
	old, err := os.Stat(path + ".1")
	if err != nil || old.Size() == 0 {
		t.Errorf("no rotated log: %v", err)
	}

	l, err = supervisor.OpenLog(path) // reopening appends and keeps counting
	if err != nil {
		t.Fatal(err)
	}
	l.Write([]byte("after reopen\n"))
	l.Close()
	if b, _ := os.ReadFile(path); !strings.HasSuffix(string(b), "after reopen\n") || int64(len(b)) != fi.Size()+13 {
		t.Errorf("reopen did not append: %d bytes", len(b))
	}
}
