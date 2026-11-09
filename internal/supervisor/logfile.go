package supervisor

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// logMax caps LogFile: one rotation to LogFile.1.
const logMax = 1 << 20

// Log is the service log for inits without a journal. It appends to its file
// and moves it to <file>.1 before a write would take it past 1 MiB. Every line
// starts with the local time (RFC 3339) and a space.
type Log struct {
	// Now, if set, replaces time.Now for the line times.
	Now func() time.Time

	mu      sync.Mutex
	path    string
	f       *os.File
	size    int64
	midLine bool   // the file ends inside a line we wrote
	buf     []byte // the stamped bytes of the current Write
}

func OpenLog(path string) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	l := &Log{path: path}
	return l, l.open()
}

func (l *Log) open() error {
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size, l.midLine = f, fi.Size(), false
	return nil
}

// Write returns len(p) on success, not the stamped length: io.Copy and
// io.MultiWriter fail on any other count.
func (l *Log) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now
	if now == nil {
		now = time.Now
	}
	var tb [40]byte
	ts := append(now().AppendFormat(tb[:0], time.RFC3339), ' ')
	start := !l.midLine
	l.buf = stamp(l.buf[:0], p, ts, start)
	if l.size > 0 && l.size+int64(len(l.buf)) > logMax {
		l.f.Close()
		if err := os.Rename(l.path, l.path+".1"); err != nil {
			return 0, err
		}
		if err := l.open(); err != nil {
			return 0, err
		}
		if !start { // the new file starts with a time too
			l.buf = stamp(l.buf[:0], p, ts, true)
		}
	}
	n, err := l.f.Write(l.buf)
	l.size += int64(n)
	if n > 0 {
		l.midLine = l.buf[n-1] != '\n'
	}
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// stamp appends p to b with ts in front of every line that starts in p;
// start tells whether p starts one.
func stamp(b, p, ts []byte, start bool) []byte {
	for len(p) > 0 {
		if start {
			b = append(b, ts...)
		}
		i := bytes.IndexByte(p, '\n') + 1
		if i == 0 {
			return append(b, p...)
		}
		b, p, start = append(b, p[:i]...), p[i:], true
	}
	return b
}

func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}
