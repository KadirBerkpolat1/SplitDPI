// Package presets loads strategy, app, country and exclude data.
package presets

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

const schemaVersion = 1

type Strategy struct {
	ID      string `toml:"id"`
	TCPTLS  string `toml:"tcp_tls"`
	TCPHTTP string `toml:"tcp_http"`
	QUIC    string `toml:"quic"`
}

type ISP struct {
	Schema     int        `toml:"schema"`
	Name       string     `toml:"name"`
	Country    string     `toml:"country"`
	ASN        []int      `toml:"asn"`
	Source     string     `toml:"source"`
	Reported   string     `toml:"reported"`
	Strategies []Strategy `toml:"strategy"`
	File       string     `toml:"-"`
}

type UDP struct {
	Ports  string   `toml:"ports"`
	L7     []string `toml:"l7"`
	Desync string   `toml:"desync"`
}

type App struct {
	Schema int      `toml:"schema"`
	ID     string   `toml:"id"`
	Name   string   `toml:"name"`
	Hosts  []string `toml:"hosts"`
	Probe  []string `toml:"probe"`
	QUIC   bool     `toml:"quic"`
	UDP    *UDP     `toml:"udp"`
	Note   string   `toml:"note"`
}

type Country struct {
	Schema      int        `toml:"schema"`
	Code        string     `toml:"code"`
	DefaultApps []string   `toml:"default_apps"`
	Exclude     string     `toml:"exclude"`
	Strategies  []Strategy `toml:"strategy"`
	file        string
}

type generic struct {
	Schema     int        `toml:"schema"`
	Strategies []Strategy `toml:"strategy"`
}

// Set is one loaded preset snapshot. Origin is set by the caller that chose it.
type Set struct {
	ISPs      []ISP
	Apps      map[string]App
	Countries map[string]Country // keyed by upper-case country code
	Generic   []Strategy
	Exclude   map[string][]string // keyed by file stem: "global", "tr", ...
	Warnings  []string
	Origin    string
	Version   string
}

type Candidate struct {
	Strategy Strategy
	Source   string // file path within the preset tree
}

const GenericFile = "generic.toml"

// Load reads the fixed preset layout from fsys. It never fails: every bad
// file is skipped and becomes one Warning naming the file and the reason.
func Load(fsys fs.FS) Set {
	s := Set{Apps: map[string]App{}, Countries: map[string]Country{}, Exclude: map[string][]string{}}
	warn := func(name string, err error) { s.Warnings = append(s.Warnings, fmt.Sprintf("%s: %v", name, err)) }

	for _, f := range glob(fsys, "isp/*/*.toml") {
		var isp ISP
		err := decode(fsys, f, &isp, &isp.Schema)
		if err == nil {
			err = validateStrategies(isp.Strategies)
		}
		if err != nil {
			warn(f, err)
			continue
		}
		isp.File = f
		s.ISPs = append(s.ISPs, isp)
	}
	for _, f := range glob(fsys, "apps/*.toml") {
		var a App
		err := decode(fsys, f, &a, &a.Schema)
		if err == nil {
			err = validateApp(a)
		}
		if err != nil {
			warn(f, err)
			continue
		}
		s.Apps[a.ID] = a
	}
	for _, f := range glob(fsys, "countries/*.toml") {
		var c Country
		err := decode(fsys, f, &c, &c.Schema)
		if err == nil {
			err = validateStrategies(c.Strategies)
		}
		if err != nil {
			warn(f, err)
			continue
		}
		if c.Code == "" {
			c.Code = strings.TrimSuffix(path.Base(f), ".toml")
		}
		c.Code = strings.ToUpper(c.Code)
		c.file = f
		s.Countries[c.Code] = c
	}
	if _, err := fs.Stat(fsys, GenericFile); err == nil {
		var g generic
		err := decode(fsys, GenericFile, &g, &g.Schema)
		if err == nil {
			err = validateStrategies(g.Strategies)
		}
		if err != nil {
			warn(GenericFile, err)
		} else {
			s.Generic = g.Strategies
		}
	}
	for _, f := range glob(fsys, "exclude/*.txt") {
		list, err := readList(fsys, f)
		if err == nil {
			err = validateList(list)
		}
		if err != nil {
			warn(f, err)
			continue
		}
		s.Exclude[strings.TrimSuffix(path.Base(f), ".txt")] = list
	}
	if b, err := fs.ReadFile(fsys, "VERSION"); err == nil {
		s.Version = strings.TrimSpace(string(b))
	}
	return s
}

// Current returns the fetched snapshot in PresetCache if it has at least one
// app, otherwise the embedded seed.
func Current(p paths.Paths) Set {
	if s := Load(os.DirFS(p.PresetCache)); len(s.Apps) > 0 {
		s.Origin = "cache"
		return s
	}
	s := Load(Embedded())
	s.Origin = "embedded"
	return s
}

func glob(fsys fs.FS, pattern string) []string {
	m, _ := fs.Glob(fsys, pattern) // only ErrBadPattern, impossible for our constant patterns
	return m
}

func decode(fsys fs.FS, name string, v any, schema *int) error {
	md, err := toml.DecodeFS(fsys, name, v)
	if err != nil {
		return err
	}
	if *schema != schemaVersion {
		return fmt.Errorf("unsupported schema %d (want %d)", *schema, schemaVersion)
	}
	if u := md.Undecoded(); len(u) > 0 {
		return fmt.Errorf("unknown key %q", u[0].String())
	}
	return nil
}

// readList returns the non-blank lines that are not # comments.
func readList(fsys fs.FS, name string) ([]string, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out, sc.Err()
}

// Candidates lists strategies to try in order: the ISP files matching asn,
// then the country, then generic. Duplicate TCPTLS strings keep the first.
func (s Set) Candidates(asn int, country string) []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	add := func(src string, sts []Strategy) {
		for _, st := range sts {
			if !seen[st.TCPTLS] {
				seen[st.TCPTLS] = true
				out = append(out, Candidate{Strategy: st, Source: src})
			}
		}
	}
	for _, isp := range s.ISPs {
		if slices.Contains(isp.ASN, asn) {
			add(isp.File, isp.Strategies)
		}
	}
	if c, ok := s.Countries[strings.ToUpper(country)]; ok {
		add(c.file, c.Strategies)
	}
	add(GenericFile, s.Generic)
	return out
}

// ExcludeFor returns the sorted union of the global exclude list and the country's list.
func (s Set) ExcludeFor(country string) []string {
	name := strings.ToLower(country)
	if c, ok := s.Countries[strings.ToUpper(country)]; ok && c.Exclude != "" {
		name = strings.TrimSuffix(path.Base(c.Exclude), ".txt")
	}
	out := slices.Concat(s.Exclude["global"], s.Exclude[name])
	slices.Sort(out)
	return slices.Compact(out)
}
