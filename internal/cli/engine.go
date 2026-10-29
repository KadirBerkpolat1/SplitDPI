package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
)

func init() {
	commands["engine"] = Command{Name: "engine", Root: true, Help: "cmd.engine.help", Run: runEngine}
}

func runEngine(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	switch {
	case len(args) >= 1 && args[0] == "install":
		if from, ok := parseFrom(args[1:]); ok {
			return engineInstall(ctx, o, w, from)
		}
	case len(args) == 1 && args[0] == "status":
		return engineStatus(o, w)
	}
	return usageError(i18n.T("engine.usage"))
}

// parseFrom accepts no arguments, "--from FILE" or "--from=FILE".
func parseFrom(args []string) (string, bool) {
	switch {
	case len(args) == 0:
		return "", true
	case len(args) == 2 && args[0] == "--from":
		return args[1], args[1] != ""
	case len(args) == 1 && strings.HasPrefix(args[0], "--from="):
		v := strings.TrimPrefix(args[0], "--from=")
		return v, v != ""
	}
	return "", false
}

func engineInstall(ctx context.Context, o *ops.Ops, w io.Writer, from string) error {
	if from == "" {
		fmt.Fprintln(w, i18n.T("engine.downloading", engine.URL))
	}
	e, err := o.EngineInstall(ctx, from)
	if err != nil {
		msg := i18n.T("engine.err.install", err)
		switch {
		case errors.Is(err, engine.ErrChecksum):
			msg = i18n.T("engine.err.checksum", engine.Version)
		case errors.Is(err, engine.ErrArch):
			msg = i18n.T("engine.err.arch", o.GOARCH)
		}
		if from == "" {
			msg += "\n" + i18n.T("engine.download_hint", engine.URL)
		}
		return errors.New(msg)
	}
	fmt.Fprintln(w, i18n.T("engine.installed", engine.Version, e.Dir))
	return nil
}

func engineStatus(o *ops.Ops, w io.Writer) error {
	e, ok := o.EngineStatus()
	if !ok {
		return errors.New(i18n.T("engine.not_installed"))
	}
	version := strings.TrimPrefix(filepath.Base(e.Dir), "zapret2-")
	fmt.Fprintln(w, i18n.T("engine.status", version, e.Dir))
	if version == engine.Version {
		fmt.Fprintln(w, i18n.T("engine.pin_ok"))
	} else {
		fmt.Fprintln(w, i18n.T("engine.pin_mismatch", engine.Version))
	}
	return nil
}
