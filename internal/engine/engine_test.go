package engine_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

type ent struct {
	name, body, link string
	typ              byte
	mode             int64
}

func good() []ent {
	return []ent{
		{name: "zapret2-v1.0.6/", typ: tar.TypeDir, mode: 0o755},
		{name: "zapret2-v1.0.6/binaries/linux-x86_64/nfqws2", body: "elf", mode: 0o755},
		{name: "zapret2-v1.0.6/binaries/linux-x86_64/mdig", body: "elf", mode: 0o755},
		{name: "zapret2-v1.0.6/binaries/linux-arm64/nfqws2", body: "elf", mode: 0o755},
		{name: "zapret2-v1.0.6/blockcheck2.sh", body: "#!/bin/sh\n", mode: 0o755},
		{name: "zapret2-v1.0.6/lua/zapret-lib.lua", body: "-- lib\n", mode: 0o644},
	}
}

func build(t *testing.T, ents []ent) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range ents {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Typeflag: typ, Mode: e.mode, Linkname: e.link, Size: int64(len(e.body))}
		if typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
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

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func install(p paths.Paths, b []byte) (engine.Engine, error) {
	return engine.Install(p, bytes.NewReader(b), sum(b), "amd64")
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	var names []string
	for _, de := range des {
		names = append(names, de.Name())
	}
	return names
}

// seedPrevious installs a fake earlier engine that a failed install must leave alone.
func seedPrevious(t *testing.T, p paths.Paths) {
	t.Helper()
	dir := filepath.Join(p.EngineDir, "zapret2-v1.0.6")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.EngineDir, "current"), []byte("zapret2-v1.0.6\nlinux-x86_64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func checkPrevious(t *testing.T, p paths.Paths) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p.EngineDir, "zapret2-v1.0.6", "marker"))
	if err != nil || string(b) != "old" {
		t.Errorf("previous engine changed: %q, %v", b, err)
	}
	b, err = os.ReadFile(filepath.Join(p.EngineDir, "current"))
	if err != nil || string(b) != "zapret2-v1.0.6\nlinux-x86_64\n" {
		t.Errorf("current changed: %q, %v", b, err)
	}
	if got := entries(t, p.EngineDir); !slices.Equal(got, []string{"current", "zapret2-v1.0.6"}) {
		t.Errorf("engine dir entries = %v", got)
	}
	if got := entries(t, p.StateDir); !slices.Equal(got, []string{"engine"}) {
		t.Errorf("state dir entries = %v", got)
	}
}

func TestInstallOK(t *testing.T) {
	p := paths.Under(t.TempDir())
	b := build(t, good())
	for range 2 { // the second run replaces the first
		e, err := install(p, b)
		if err != nil {
			t.Fatal(err)
		}
		if e.Dir != p.EngineDir+"/zapret2-v1.0.6" {
			t.Errorf("Dir = %q", e.Dir)
		}
		st, err := os.Stat(e.Nfqws2())
		if err != nil || st.Mode()&0o111 == 0 {
			t.Errorf("nfqws2: %v, %v", st, err)
		}
		if e.Nfqws2() != e.Dir+"/binaries/linux-x86_64/nfqws2" || e.Mdig() != e.Dir+"/binaries/linux-x86_64/mdig" ||
			e.Blockcheck2() != e.Dir+"/blockcheck2.sh" || e.LuaDir() != e.Dir+"/lua" {
			t.Errorf("paths: %+v", e)
		}
		got, ok := engine.Installed(p)
		if !ok || got != e {
			t.Errorf("Installed = %+v, %v; want %+v", got, ok, e)
		}
	}
	if got := entries(t, p.EngineDir); !slices.Equal(got, []string{"current", "zapret2-v1.0.6"}) {
		t.Errorf("engine dir entries = %v", got)
	}
	if got := entries(t, p.StateDir); !slices.Equal(got, []string{"engine"}) {
		t.Errorf("state dir entries = %v", got)
	}
}

func TestInstalledMissing(t *testing.T) {
	if _, ok := engine.Installed(paths.Under(t.TempDir())); ok {
		t.Error("Installed on empty root")
	}
}

func TestInstallRejectsChecksum(t *testing.T) {
	p := paths.Under(t.TempDir())
	_, err := engine.Install(p, bytes.NewReader(build(t, good())), strings.Repeat("0", 64), "amd64")
	if !errors.Is(err, engine.ErrChecksum) {
		t.Fatalf("err = %v", err)
	}
	for _, n := range entries(t, p.EngineDir) {
		t.Errorf("leftover in engine dir: %s", n)
	}
	if got := entries(t, p.StateDir); len(got) > 1 || (len(got) == 1 && got[0] != "engine") {
		t.Errorf("state dir entries = %v", got)
	}
}

func TestInstallRejectsTraversal(t *testing.T) {
	cases := map[string][]ent{
		"dotdot":   {{name: "../../evil", body: "x"}},
		"absolute": {{name: "ABS/evil", body: "x"}},
		"symlink":  {{name: "zapret2-v1.0.6/etc", typ: tar.TypeSymlink, link: "/etc"}},
		"symlink-up-then-write": {
			{name: "zapret2-v1.0.6/up", typ: tar.TypeSymlink, link: "../../.."},
			{name: "zapret2-v1.0.6/up/evil", body: "x"},
		},
		"hardlink": {{name: "zapret2-v1.0.6/pw", typ: tar.TypeLink, link: "/etc/passwd"}},
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			for i := range bad {
				bad[i].name = strings.Replace(bad[i].name, "ABS", base, 1)
			}
			p := paths.Under(filepath.Join(base, "root"))
			seedPrevious(t, p)
			_, err := install(p, build(t, append(good(), bad...)))
			if !errors.Is(err, engine.ErrUnsafePath) {
				t.Fatalf("err = %v", err)
			}
			checkPrevious(t, p)
			filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
				if d != nil && d.Name() == "evil" {
					t.Errorf("written: %s", path)
				}
				return nil
			})
		})
	}
}

func TestInstallRejectsTruncated(t *testing.T) {
	p := paths.Under(t.TempDir())
	seedPrevious(t, p)
	b := build(t, good())
	if _, err := install(p, b[:len(b)/2]); err == nil {
		t.Fatal("no error")
	}
	checkPrevious(t, p)
}

func TestInstallRejectsMissingBinary(t *testing.T) {
	p := paths.Under(t.TempDir())
	seedPrevious(t, p)
	var ents []ent
	for _, e := range good() {
		if !strings.HasSuffix(e.name, "linux-x86_64/nfqws2") {
			ents = append(ents, e)
		}
	}
	if _, err := install(p, build(t, ents)); err == nil {
		t.Fatal("no error")
	}
	checkPrevious(t, p)
}

func TestInstallRejectsArch(t *testing.T) {
	p := paths.Under(t.TempDir())
	b := build(t, good())
	if _, err := engine.Install(p, bytes.NewReader(b), sum(b), "mips"); !errors.Is(err, engine.ErrArch) {
		t.Fatalf("err = %v", err)
	}
	if got := entries(t, p.StateDir); got != nil {
		t.Errorf("state dir entries = %v", got)
	}
}
