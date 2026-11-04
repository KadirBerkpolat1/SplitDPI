package sysinfo

import (
	"bufio"
	"bytes"
	"io"
	"io/fs"
	"strings"
)

type OSRelease struct {
	ID         string
	IDLike     []string
	VariantID  string
	VersionID  string
	PrettyName string
}

func ParseOSRelease(r io.Reader) (OSRelease, error) {
	var o OSRelease
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.Trim(val, `"`)
		switch key {
		case "ID":
			o.ID = val
		case "ID_LIKE":
			o.IDLike = splitLike(val)
		case "VARIANT_ID":
			o.VariantID = val
		case "VERSION_ID":
			o.VersionID = val
		case "PRETTY_NAME":
			o.PrettyName = val
		}
	}
	return o, s.Err()
}

// ReadOSRelease reads root's os-release; an unreadable one is empty.
func ReadOSRelease(root fs.FS) OSRelease {
	for _, p := range []string{"etc/os-release", "usr/lib/os-release"} {
		if b, err := fs.ReadFile(root, p); err == nil {
			o, _ := ParseOSRelease(bytes.NewReader(b))
			return o
		}
	}
	return OSRelease{}
}

func splitLike(s string) []string {
	s = strings.ReplaceAll(s, ",", " ")
	var out []string
	for _, p := range strings.Fields(s) {
		out = append(out, p)
	}
	return out
}
