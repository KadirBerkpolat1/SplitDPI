package safetar

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var testLimits = Limits{File: 1024, Total: 4096}

type ent struct {
	name, body, link string
	typ              byte
	mode             int64
}

func archive(t *testing.T, ents ...ent) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range ents {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Linkname: e.link}
		if e.typ == 0 {
			h.Typeflag, h.Size = tar.TypeReg, int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func extractTo(t *testing.T, b []byte) (string, error) {
	t.Helper()
	dir := t.TempDir()
	return dir, Extract(bytes.NewReader(b), dir, testLimits)
}

// diskBytes sums the sizes of the regular files under dir.
func diskBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var n int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		n += fi.Size()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestExtract(t *testing.T) {
	full := strings.Repeat("f", int(testLimits.File))
	// Every file is at most File bytes and together they make exactly Total.
	dir, err := extractTo(t, archive(t,
		ent{name: "top/", typ: tar.TypeDir, mode: 0o755},
		ent{name: "top/bin/tool", body: "elf", mode: 0o4755},
		ent{name: "top/deep/er/a", body: full, mode: 0o644},
		ent{name: "top/deep/er/b", body: full, mode: 0o644},
		ent{name: "top/deep/er/c", body: full, mode: 0o644},
		ent{name: "top/deep/er/d", body: full[3:], mode: 0o644},
		ent{name: "top/tool", typ: tar.TypeSymlink, link: "bin/tool"},
	))
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "top/bin/tool"))
	if err != nil || st.Mode()&fs.ModeSetuid != 0 || st.Mode()&0o100 == 0 {
		t.Errorf("tool: %v, %v", st, err)
	}
	if link, err := os.Readlink(filepath.Join(dir, "top/tool")); err != nil || link != "bin/tool" {
		t.Errorf("symlink: %q, %v", link, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "top/tool")); err != nil || string(b) != "elf" {
		t.Errorf("through symlink: %q, %v", b, err)
	}
	if n := diskBytes(t, dir); n != testLimits.Total {
		t.Errorf("unpacked %d bytes, want %d", n, testLimits.Total)
	}
}

func TestExtractRejectsUnsafe(t *testing.T) {
	cases := map[string][]ent{
		"dotdot":            {{name: "../evil", body: "x"}},
		"inner dotdot":      {{name: "a/../../evil", body: "x"}},
		"absolute":          {{name: "ABS/evil", body: "x"}},
		"absolute symlink":  {{name: "etc", typ: tar.TypeSymlink, link: "/etc"}},
		"climbing symlink":  {{name: "up", typ: tar.TypeSymlink, link: "../.."}, {name: "up/evil", body: "x"}},
		"dotdot in symlink": {{name: "a/b", typ: tar.TypeSymlink, link: "../c"}},
		"hardlink":          {{name: "pw", typ: tar.TypeLink, link: "/etc/passwd"}},
		"local hardlink":    {{name: "a", body: "x"}, {name: "b", typ: tar.TypeLink, link: "a"}},
		"char device":       {{name: "null", typ: tar.TypeChar}},
		"fifo":              {{name: "fifo", typ: tar.TypeFifo}},
	}
	for name, ents := range cases {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "root")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for i := range ents {
				ents[i].name = strings.Replace(ents[i].name, "ABS", base, 1)
			}
			if err := Extract(bytes.NewReader(archive(t, ents...)), dir, testLimits); !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("err = %v", err)
			}
			filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
				if d != nil && d.Name() == "evil" {
					t.Errorf("written: %s", path)
				}
				return nil
			})
		})
	}
}

func TestExtractLimits(t *testing.T) {
	full := strings.Repeat("x", int(testLimits.File))
	cases := map[string]struct {
		ents []ent
		want string
	}{
		"file": {[]ent{{name: "big", body: full + "x"}}, "big is over 1024 bytes"},
		"total": {
			[]ent{{name: "a", body: full}, {name: "b", body: full}, {name: "c", body: full}, {name: "d", body: full}, {name: "big", body: "x"}},
			"big brings the total over 4096 bytes",
		},
		// 1 MiB of zeros packs into about 1 KiB.
		"gzip bomb": {[]ent{{name: "big", body: strings.Repeat("\x00", 1<<20)}}, "big is over 1024 bytes"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir, err := extractTo(t, archive(t, tc.ents...))
			if !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
			if _, err := os.Lstat(filepath.Join(dir, "big")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("big was created: %v", err)
			}
		})
	}
}

// lying feeds extract regular files whose header claims size bytes while the
// body holds body bytes.
type lying struct {
	files []lie
	body  io.Reader
}

type lie struct {
	name       string
	size, body int64
}

func (l *lying) Next() (*tar.Header, error) {
	if len(l.files) == 0 {
		return nil, io.EOF
	}
	f := l.files[0]
	l.files = l.files[1:]
	l.body = bytes.NewReader(make([]byte, f.body))
	return &tar.Header{Name: f.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: f.size}, nil
}

func (l *lying) Read(p []byte) (int, error) { return l.body.Read(p) }

func TestExtractLyingHeader(t *testing.T) {
	cases := map[string]struct {
		files []lie
		want  string
	}{
		"file": {[]lie{{"big", 1, 1 << 20}}, "big is over 1024 bytes"},
		"total": {
			[]lie{{"a", 1000, 1000}, {"b", 1000, 1000}, {"c", 1000, 1000}, {"d", 1000, 1000}, {"big", 1, 1000}},
			"big brings the total over 4096 bytes",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			err := extract(&lying{files: tc.files}, dir, testLimits)
			if !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
			if n := diskBytes(t, dir); n > testLimits.Total+1 {
				t.Errorf("wrote %d bytes", n)
			}
			if st, err := os.Stat(filepath.Join(dir, "big")); err != nil || st.Size() > testLimits.File+1 {
				t.Errorf("big: %v, %v", st, err)
			}
		})
	}
}

func TestExtractRejectsBroken(t *testing.T) {
	b := archive(t, ent{name: "f", body: strings.Repeat("x", 100)})
	cases := map[string][]byte{"not gzip": []byte("plain text"), "truncated": b[:len(b)/2]}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := extractTo(t, in); err == nil {
				t.Fatal("no error")
			}
		})
	}
}
