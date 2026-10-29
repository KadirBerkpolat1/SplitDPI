// Package config loads and saves splitdpi config.toml and user list files.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"
	"github.com/KadirBerkpolat1/SplitDPI/internal/dns"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
)

type Mode string

const (
	ModeBroad Mode = "broad"
	ModeApps  Mode = "apps"
)

// RunMode is how the TUI's run key starts the bypass; CLI commands ignore it.
type RunMode string

const (
	RunWindow     RunMode = "window"     // in the TUI's process, until it quits
	RunBackground RunMode = "background" // run --background, until reboot
	RunBoot       RunMode = "boot"       // enable: as a service, at every boot
)

type Strategy struct {
	ID      string `toml:"id"`
	Source  string `toml:"source"`
	TCPTLS  string `toml:"tcp_tls"`
	TCPHTTP string `toml:"tcp_http"`
	QUIC    string `toml:"quic"`
	ASN     int    `toml:"asn"`
	Date    string `toml:"date"`
	Engine  string `toml:"engine"`
}

type Config struct {
	Lang               string       `toml:"lang"`
	Mode               Mode         `toml:"mode"`
	Apps               []string     `toml:"apps"`
	Strategy           *Strategy    `toml:"strategy"`
	QNum               int          `toml:"qnum"`
	SkipIfaces         []string     `toml:"skip_ifaces"`
	DNSProvider        string       `toml:"dns_provider"`
	DNSProtocol        dns.Protocol `toml:"dns_protocol"`
	IPInfoURL          string       `toml:"ipinfo_url"`
	DisclaimerAccepted bool         `toml:"disclaimer_accepted"`
	RunMode            RunMode      `toml:"run_mode"`
}

func Default() Config {
	return Config{
		Mode:        ModeBroad,
		QNum:        200,
		SkipIfaces:  []string{"wg*", "tun*", "tailscale*"},
		DNSProvider: dns.Off,
		DNSProtocol: dns.DoH,
		IPInfoURL:   "https://ipinfo.io/json",
		RunMode:     RunWindow,
	}
}

// DNSOn reports whether port-53 traffic goes to the encrypted DNS forwarder.
func (c Config) DNSOn() bool { return c.DNSProvider != "" && c.DNSProvider != dns.Off }

func Load(p paths.Paths) (Config, error) {
	b, err := os.ReadFile(p.ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Config{}, err
	}
	var c Config
	meta, err := toml.Decode(string(b), &c)
	if err != nil {
		return Config{}, fmt.Errorf("%s: line %d: %w", filepath.Base(p.ConfigFile), errLine(err), err)
	}
	if meta.IsDefined("mode") && c.Mode != ModeBroad && c.Mode != ModeApps {
		return Config{}, fmt.Errorf("%s: invalid mode %q", filepath.Base(p.ConfigFile), c.Mode)
	}
	if !meta.IsDefined("mode") {
		c.Mode = ModeBroad
	}
	if !meta.IsDefined("qnum") {
		c.QNum = 200
	}
	if !meta.IsDefined("skip_ifaces") {
		c.SkipIfaces = []string{"wg*", "tun*", "tailscale*"}
	}
	if !meta.IsDefined("ipinfo_url") {
		c.IPInfoURL = "https://ipinfo.io/json"
	}
	if !meta.IsDefined("dns_provider") {
		// v0.1 had `dns = true|false`, which meant today's auto over DoH.
		var old struct {
			DNS bool `toml:"dns"`
		}
		toml.Decode(string(b), &old)
		c.DNSProvider = dns.Off
		if old.DNS {
			c.DNSProvider = dns.Auto
		}
	}
	if !meta.IsDefined("dns_protocol") {
		c.DNSProtocol = dns.DoH
	}
	// A run mode this version does not know falls back to the default
	// instead of failing: only the TUI reads it.
	if c.RunMode != RunBackground && c.RunMode != RunBoot {
		c.RunMode = RunWindow
	}
	if !dns.Valid(c.DNSProvider) {
		return Config{}, fmt.Errorf("%s: invalid dns_provider %q", filepath.Base(p.ConfigFile), c.DNSProvider)
	}
	if c.DNSProtocol != dns.DoH && c.DNSProtocol != dns.DoT {
		return Config{}, fmt.Errorf("%s: invalid dns_protocol %q", filepath.Base(p.ConfigFile), c.DNSProtocol)
	}
	return c, nil
}

func errLine(err error) int {
	s := err.Error()
	const pref = "Line "
	if i := strings.Index(s, pref); i >= 0 {
		rest := s[i+len(pref):]
		var n int
		if _, e := fmt.Sscanf(rest, "%d", &n); e == nil {
			return n
		}
	}
	return 1
}

func Save(p paths.Paths, c Config) error {
	if err := os.MkdirAll(p.ConfigDir, 0o755); err != nil {
		return err
	}
	var buf strings.Builder
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(c); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(p.ConfigDir, ".config-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.WriteString(buf.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, p.ConfigFile)
}

func ReadList(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

// AddToList appends the entries of ds that the list does not have yet.
func AddToList(path string, ds ...string) error {
	lines, err := ReadList(path)
	if err != nil {
		return err
	}
	have := make(map[string]bool, len(lines)+len(ds))
	for _, l := range lines {
		have[l] = true
	}
	var add strings.Builder
	for _, d := range ds {
		if !have[d] {
			have[d] = true
			add.WriteString(d + "\n")
		}
	}
	if add.Len() == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	line := add.String()
	if fi.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, fi.Size()-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			line = "\n" + line
		}
	}
	if _, err := f.WriteString(line); err != nil {
		return err
	}
	return nil
}

func RemoveFromList(path string, d string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var kept []string
	removed := false
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		line := sc.Text()
		trim := strings.TrimSpace(line)
		if trim != "" && !strings.HasPrefix(trim, "#") && trim == d {
			removed = true
			continue
		}
		kept = append(kept, line)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if !removed {
		return nil
	}
	out := strings.Join(kept, "\n")
	if len(kept) > 0 && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".list-*")
	if err != nil {
		return err
	}
	tname := tmp.Name()
	defer os.Remove(tname)
	// The replacement keeps the original's mode and owner: the autohostlist
	// belongs to the nfqws2 user, which must still be able to write it.
	st := fi.Sys().(*syscall.Stat_t)
	if err := tmp.Chmod(fi.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chown(int(st.Uid), int(st.Gid)); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tname, path)
}
