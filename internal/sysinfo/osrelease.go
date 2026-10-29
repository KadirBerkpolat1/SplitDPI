package sysinfo

import (
	"bufio"
	"io"
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

func splitLike(s string) []string {
	s = strings.ReplaceAll(s, ",", " ")
	var out []string
	for _, p := range strings.Fields(s) {
		out = append(out, p)
	}
	return out
}
