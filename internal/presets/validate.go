package presets

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/domain"
)

//go:generate go run ./genlua -engine v1.0.6 -o luafuncs_gen.go $SPLITDPI_ENGINE_DIR/lua/zapret-antidpi.lua $SPLITDPI_ENGINE_DIR/lua/zapret-auto.lua

// Strategy strings run as root inside nfqws2, which executes Lua, so they are
// untrusted input wherever they come from.
var (
	tokenRe   = regexp.MustCompile(`^--([a-z-]+)=(.+)$`)
	valueRe   = regexp.MustCompile(`^[A-Za-z0-9_:=,.+\-]+$`)
	rangeRe   = regexp.MustCompile(`^[A-Za-z0-9<\-]+$`) // e.g. -s1, <s1, s1<d1
	blobDefRe = regexp.MustCompile(`^([A-Za-z0-9_]+):0x[0-9a-fA-F]+$`)
	hexRe     = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)
	nameRe    = regexp.MustCompile(`^[a-z0-9_-]+$`)
)

var builtinBlobs = map[string]bool{"fake_default_tls": true, "fake_default_http": true, "fake_default_quic": true}

// nfqws2 resolves these --lua-desync arguments as blob names, falling back to
// any Lua global, so they are restricted like blob=.
var blobArgs = map[string]bool{"blob": true, "pattern": true, "seqovl_pattern": true, "fallback": true}

// These arguments name a Lua global that zapret2 calls (_G[value](...)), so
// each may only name the engine's own functions for that role.
var funcArgs = map[string]map[string]bool{
	"iff":              condFuncs,
	"cond":             condFuncs,
	"hostkey":          {"standard_hostkey": true},
	"failure_detector": {"standard_failure_detector": true},
	"success_detector": {"standard_success_detector": true},
	"ipfrag":           {"": true, "ipfrag2": true},
}

// cond_lua is left out: it runs the cond_code argument as Lua.
var condFuncs = map[string]bool{"cond_true": true, "cond_false": true, "cond_random": true, "cond_payload_str": true, "cond_tcp_has_ts": true}

// Arguments that carry Lua code or a free function name; no safe value exists.
var forbiddenArgs = map[string]bool{"code": true, "cond_code": true, "fool": true}

// ValidateArgs accepts only space-separated --payload, --lua-desync,
// --in-range, --out-range and --blob=<name>:0x<hex> tokens.
func ValidateArgs(s string) error {
	if s == "" {
		return errors.New("empty strategy")
	}
	toks := strings.Split(s, " ")
	blobs := map[string]bool{}
	for _, t := range toks {
		m := tokenRe.FindStringSubmatch(t)
		if m == nil {
			return fmt.Errorf("token %q is not --option=value", t)
		}
		opt, val := m[1], m[2]
		switch opt {
		case "payload", "lua-desync":
			if !valueRe.MatchString(val) {
				return fmt.Errorf("--%s: invalid characters in %q", opt, val)
			}
		case "in-range", "out-range":
			if !rangeRe.MatchString(val) {
				return fmt.Errorf("--%s: invalid range %q", opt, val)
			}
		case "blob":
			bm := blobDefRe.FindStringSubmatch(val)
			if bm == nil {
				return fmt.Errorf("--blob must be <name>:0x<hex>, got %q", val)
			}
			blobs[bm[1]] = true
		case "lua-init":
			return errors.New("--lua-init is not allowed (it runs arbitrary Lua)")
		default:
			return fmt.Errorf("option --%s is not allowed", opt)
		}
	}
	for _, t := range toks {
		val, ok := strings.CutPrefix(t, "--lua-desync=")
		if !ok {
			continue
		}
		parts := strings.Split(val, ":")
		if !LuaFuncs[parts[0]] {
			return fmt.Errorf("--lua-desync function %q is not allowed", parts[0])
		}
		for _, a := range parts[1:] {
			k, v, _ := strings.Cut(a, "=")
			if forbiddenArgs[k] {
				return fmt.Errorf("--lua-desync %s: argument %q is not allowed", parts[0], k)
			}
			if allowed, ok := funcArgs[k]; ok && !allowed[v] {
				return fmt.Errorf("--lua-desync %s: %s=%q is not an allowed function", parts[0], k, v)
			}
			if blobArgs[k] && !builtinBlobs[v] && !blobs[v] && !hexRe.MatchString(v) {
				return fmt.Errorf("--lua-desync %s: %s=%q is not a built-in blob, a --blob name or 0x hex", parts[0], k, v)
			}
		}
	}
	return nil
}

// ValidatePorts accepts a comma-separated list of ports and lo-hi ranges within 1-65535.
func ValidatePorts(s string) error {
	for _, r := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(r, "-")
		a, err := port(lo)
		if err != nil {
			return fmt.Errorf("ports %q: %w", s, err)
		}
		b := a
		if isRange {
			if b, err = port(hi); err != nil {
				return fmt.Errorf("ports %q: %w", s, err)
			}
		}
		if a > b {
			return fmt.Errorf("ports %q: range %s is reversed", s, r)
		}
	}
	return nil
}

func port(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("%q is not a port (1-65535)", s)
	}
	return n, nil
}

func validateStrategies(sts []Strategy) error {
	for _, st := range sts {
		for _, f := range [...]struct{ name, val string }{{"tcp_tls", st.TCPTLS}, {"tcp_http", st.TCPHTTP}, {"quic", st.QUIC}} {
			if f.val == "" {
				continue
			}
			if err := ValidateArgs(f.val); err != nil {
				return fmt.Errorf("strategy %q %s: %w", st.ID, f.name, err)
			}
		}
	}
	return nil
}

func validateApp(a App) error {
	if a.ID == "" {
		return errors.New("missing id")
	}
	if !nameRe.MatchString(a.ID) {
		return fmt.Errorf("invalid id %q", a.ID)
	}
	for _, h := range a.Hosts {
		if !domain.Valid(h) {
			return fmt.Errorf("invalid host %q", h)
		}
	}
	for _, p := range a.Probe {
		u, err := url.Parse(p)
		if err != nil || u.Scheme != "https" || !domain.Valid(u.Hostname()) {
			return fmt.Errorf("invalid probe URL %q (want https://<domain>/...)", p)
		}
	}
	if a.UDP != nil {
		if err := ValidatePorts(a.UDP.Ports); err != nil {
			return fmt.Errorf("udp: %w", err)
		}
		for _, l := range a.UDP.L7 {
			if !nameRe.MatchString(l) {
				return fmt.Errorf("udp: invalid l7 name %q", l)
			}
		}
		if err := ValidateArgs(a.UDP.Desync); err != nil {
			return fmt.Errorf("udp desync: %w", err)
		}
	}
	return nil
}

func validateList(list []string) error {
	for _, d := range list {
		if !domain.Valid(d) {
			return fmt.Errorf("invalid domain %q", d)
		}
	}
	return nil
}
