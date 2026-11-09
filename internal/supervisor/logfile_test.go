package supervisor_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

// logAt is a local time in UTC+3 for the stamp tests; logTS is its stamp.
var logAt = time.Date(2026, 11, 9, 15, 42, 7, 0, time.FixedZone("TRT", 3*60*60))

const logTS = "2026-11-09T15:42:07+03:00 "

// openLog opens a fresh log whose clock reads *now.
func openLog(t *testing.T, now *time.Time) (*supervisor.Log, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "var/log/splitdpi.log")
	l, err := supervisor.OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Now = func() time.Time { return *now }
	return l, path
}

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
	l.Now = func() time.Time { return logAt }
	l.Write([]byte("after reopen\n"))
	l.Close()
	want := logTS + "after reopen\n"
	if b, _ := os.ReadFile(path); !strings.HasSuffix(string(b), want) || int64(len(b)) != fi.Size()+int64(len(want)) {
		t.Errorf("reopen did not append: %d bytes", len(b))
	}
}

// Every line starts with the time of the write that began it, however the
// writes cut the lines, and Write counts only the caller's bytes.
func TestLogFileStampsLines(t *testing.T) {
	now := logAt
	l, path := openLog(t, &now)
	for _, w := range []string{"one\ntwo\n", "par", "ti", "al\nthree", "\n", "\n\nfour\n"} {
		if n, err := l.Write([]byte(w)); n != len(w) || err != nil {
			t.Fatalf("Write(%q) = %d, %v; want %d, nil", w, n, err, len(w))
		}
		now = now.Add(time.Second)
	}
	l.Close()
	want := "2026-11-09T15:42:07+03:00 one\n" +
		"2026-11-09T15:42:07+03:00 two\n" +
		"2026-11-09T15:42:08+03:00 partial\n" +
		"2026-11-09T15:42:10+03:00 three\n" +
		"2026-11-09T15:42:12+03:00 \n" +
		"2026-11-09T15:42:12+03:00 \n" +
		"2026-11-09T15:42:12+03:00 four\n"
	if b, _ := os.ReadFile(path); string(b) != want {
		t.Errorf("log:\n%s\nwant:\n%s", b, want)
	}
}

// A line the rotation cuts starts the new file with a time of its own.
func TestLogFileRotatesMidLine(t *testing.T) {
	now := logAt
	l, path := openLog(t, &now)
	big := strings.Repeat("x", 1<<20-len(logTS)-4) // ends 4 bytes short of 1 MiB, inside a line
	l.Write([]byte(big))
	l.Write([]byte("tail\nnext\n"))
	l.Close()
	old, _ := os.ReadFile(path + ".1")
	cur, _ := os.ReadFile(path)
	if string(old) != logTS+big || string(cur) != logTS+"tail\n"+logTS+"next\n" {
		t.Errorf("rotated %d bytes, then %q", len(old), cur)
	}
}

// Lines written from several goroutines at once keep one time each.
func TestLogFileConcurrentWrites(t *testing.T) {
	now := logAt
	l, path := openLog(t, &now)
	want := map[string]bool{}
	var wg sync.WaitGroup
	for g := range 8 {
		for i := range 100 {
			want[fmt.Sprintf("%sg%d line %d", logTS, g, i)] = true
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 100 {
				fmt.Fprintf(l, "g%d line %d\n", g, i)
			}
		}()
	}
	wg.Wait()
	l.Close()
	b, _ := os.ReadFile(path)
	for line := range strings.Lines(string(b)) {
		line = strings.TrimSuffix(line, "\n")
		if !want[line] {
			t.Errorf("line %q", line)
		}
		delete(want, line)
	}
	if len(want) > 0 {
		t.Errorf("%d lines missing", len(want))
	}
}

// Without a clock of its own, the log stamps the local time.
func TestLogFileLocalTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "splitdpi.log")
	l, err := supervisor.OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().Truncate(time.Second)
	l.Write([]byte("x\n"))
	l.Close()
	b, _ := os.ReadFile(path)
	ts, rest, _ := strings.Cut(string(b), " ")
	at, err := time.Parse(time.RFC3339, ts)
	if err != nil || rest != "x\n" || at.Before(before) || at.After(time.Now()) || at.In(time.Local).Format(time.RFC3339) != ts {
		t.Errorf("log %q, %v", b, err)
	}
}
