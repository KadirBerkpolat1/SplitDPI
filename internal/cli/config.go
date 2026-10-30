package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

func init() {
	commands["config"] = Command{Name: "config", Root: true, Help: "cmd.config.help", Run: runConfig}
}

// setting is one key of `splitdpi config`. get shows the value as set takes it.
type setting struct {
	key     string
	get     func(config.Config) string
	set     func(o *ops.Ops, v string) error
	restart bool // the running bypass uses it, so it applies on the next start
}

var settings = []setting{
	{key: "lang",
		get: func(c config.Config) string { return c.Lang },
		set: func(o *ops.Ops, v string) error { return o.SetLang(v) }},
	{key: "run_mode",
		get: func(c config.Config) string { return string(c.RunMode) },
		set: func(o *ops.Ops, v string) error { return o.SetRunMode(config.RunMode(v)) }},
	{key: "mode", restart: true,
		get: func(c config.Config) string { return string(c.Mode) },
		set: func(o *ops.Ops, v string) error { return o.SetMode(config.Mode(v)) }},
	{key: "skip_ifaces", restart: true,
		get: func(c config.Config) string { return strings.Join(c.SkipIfaces, ",") },
		set: func(o *ops.Ops, v string) error { return o.SetSkipIfaces(splitList(v)) }},
	{key: "ipinfo_url",
		get: func(c config.Config) string { return c.IPInfoURL },
		set: func(o *ops.Ops, v string) error { return o.SetIPInfoURL(v) }},
	{key: "qnum", restart: true,
		get: func(c config.Config) string { return strconv.Itoa(c.QNum) },
		set: func(o *ops.Ops, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil {
				return errors.New(i18n.T("config.err_qnum", v))
			}
			return o.SetQNum(n)
		}},
}

func runConfig(_ context.Context, o *ops.Ops, w io.Writer, args []string) error {
	if len(args) == 0 {
		return printConfig(o, w)
	}
	var s setting
	if len(args) >= 2 {
		var err error
		if s, err = findSetting(args[1]); err != nil {
			return err
		}
	}
	switch {
	case len(args) == 2 && args[0] == "get":
		c, err := config.Load(o.P)
		if err != nil {
			return err
		}
		fmt.Fprintln(w, s.get(c))
		return nil
	case len(args) == 3 && args[0] == "set":
		return setConfig(o, w, s, args[2])
	case len(args) == 2 && args[0] == "reset":
		return setConfig(o, w, s, s.get(config.Default()))
	}
	return usageError(i18n.T("config.usage"))
}

func findSetting(key string) (setting, error) {
	keys := make([]string, len(settings))
	for i, s := range settings {
		if s.key == key {
			return s, nil
		}
		keys[i] = s.key
	}
	return setting{}, usageError(i18n.T("config.unknown_key", key, strings.Join(keys, ", ")))
}

func setConfig(o *ops.Ops, w io.Writer, s setting, v string) error {
	if err := s.set(o, v); err != nil {
		if errors.Is(err, ops.ErrAppsModeEmpty) {
			return errors.New(i18n.T("config.apps_empty"))
		}
		return err
	}
	c, err := config.Load(o.P)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, i18n.T("config.saved", s.key, showValue(s, c)))
	if s.key == "qnum" {
		fmt.Fprintln(w, i18n.T("config.qnum_note"))
	}
	switch {
	case s.restart && o.Running():
		fmt.Fprintln(w, i18n.T("strategy.restart"))
	case s.restart:
		fmt.Fprintln(w, i18n.T("config.next_start"))
	}
	return nil
}

func printConfig(o *ops.Ops, w io.Writer) error {
	c, err := config.Load(o.P)
	if err != nil {
		return err
	}
	def := config.Default()
	kw, vw := 0, 0
	for _, s := range settings {
		kw = max(kw, len(s.key))
		vw = max(vw, utf8.RuneCountInString(showValue(s, c)))
	}
	for _, s := range settings {
		v := showValue(s, c)
		mark := i18n.T("config.is_default")
		if s.get(c) != s.get(def) {
			mark = i18n.T("config.default", showValue(s, def))
		}
		fmt.Fprintln(w, pad(s.key, kw)+"  "+pad(v, vw)+"  "+mark)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, i18n.T("config.usage"))
	return nil
}

// showValue is the value for people: an empty one says what it means.
func showValue(s setting, c config.Config) string {
	v := s.get(c)
	switch {
	case v != "":
		return v
	case s.key == "lang":
		return i18n.T("config.lang_system")
	default:
		return i18n.T("config.none")
	}
}

func pad(s string, n int) string {
	return s + strings.Repeat(" ", max(0, n-utf8.RuneCountInString(s)))
}

// splitList reads a comma-separated list; blanks around and between commas are dropped.
func splitList(v string) []string {
	var out []string
	for _, f := range strings.Split(v, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
