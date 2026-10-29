package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/render"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

func init() {
	commands["run"] = Command{Name: "run", Root: true, Help: "cmd.run.help", Run: runRun}
	commands["stop"] = Command{Name: "stop", Root: true, Help: "cmd.stop.help", Run: runStop}
	commands["status"] = Command{Name: "status", Root: true, Help: "cmd.status.help", Run: runStatus}
	commands["revert"] = Command{Name: "revert", Root: true, Help: "cmd.revert.help", Run: runRevert}
}

func runRun(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	service := false
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "--service":
		service = true
	default:
		return usageError(i18n.T("run.usage"))
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := o.Run(ctx, service)

	var conflict ops.ConflictError
	var already supervisor.ErrAlreadyRunning
	var bad ops.ConfigError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &conflict):
		printChecks(w, conflict.Checks)
		return errors.New(i18n.T("run.conflict"))
	case errors.Is(err, render.ErrNoStrategy):
		return errors.New(i18n.T("run.no_strategy"))
	case errors.As(err, &already):
		return errors.New(i18n.T("run.already", i18n.Enum("mode", already.Mode)))
	case errors.Is(err, ops.ErrEngineMissing):
		return errors.New(i18n.T("engine.not_installed"))
	case errors.Is(err, supervisor.ErrCrashLoop):
		msg := errors.New(i18n.T("run.crash_loop"))
		if service {
			return exitError{code: 3, err: msg}
		}
		return msg
	case errors.As(err, &bad):
		return errors.New(i18n.T("run.bad_config", bad.File, bad.Err))
	}
	return err
}

func runStop(_ context.Context, o *ops.Ops, w io.Writer, args []string) error {
	if len(args) != 0 {
		return usageError(i18n.T("stop.usage"))
	}
	switch err := o.Stop(); {
	case errors.Is(err, ops.ErrNotRunning):
		fmt.Fprintln(w, i18n.T("stop.not_running"))
		return nil
	case errors.Is(err, ops.ErrStopTimeout):
		return errors.New(i18n.T("stop.timeout"))
	case err != nil:
		return err
	}
	fmt.Fprintln(w, i18n.T("stop.stopped"))
	return nil
}

func runStatus(_ context.Context, o *ops.Ops, w io.Writer, args []string) error {
	if len(args) != 0 {
		return usageError(i18n.T("status.usage"))
	}
	st, cfg, err := o.Status()
	fmt.Fprintln(w, i18n.T("status.state", i18n.Enum("state", st.State)))
	if st.State == "running" {
		fmt.Fprintln(w, i18n.T("status.process", i18n.Enum("mode", st.Mode), st.PID, st.Since.Format("2006-01-02 15:04:05")))
	}
	if cfg.Strategy != nil {
		fmt.Fprintln(w, i18n.T("status.strategy", cfg.Strategy.ID, cfg.Strategy.Source))
	} else {
		fmt.Fprintln(w, i18n.T("status.no_strategy"))
	}
	if st.Engine != "" {
		fmt.Fprintln(w, i18n.T("status.engine", st.Engine))
	}
	fmt.Fprintln(w, i18n.T("status.apps", strings.Join(cfg.Apps, ", ")))
	if cfg.DNS {
		fmt.Fprintln(w, i18n.T("status.dns"))
	}
	if st.LastError != "" {
		fmt.Fprintln(w, i18n.T("status.last_error", st.LastError))
		for _, l := range st.StderrTail {
			fmt.Fprintln(w, "    "+l)
		}
	}
	return err
}

func runRevert(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	var rulesOnly, purge bool
	for _, a := range args {
		switch a {
		case "--rules-only":
			rulesOnly = true
		case "--purge":
			purge = true
		default:
			return usageError(i18n.T("revert.usage"))
		}
	}
	r := o.Revert(ctx, rulesOnly, purge)
	for _, d := range r.Removed {
		fmt.Fprintln(w, i18n.T("revert.removed", d))
	}
	if len(r.Leftover) == 0 {
		fmt.Fprintln(w, i18n.T("revert.clean"))
		return nil
	}
	for _, l := range r.Leftover {
		fmt.Fprintln(w, i18n.T("revert.leftover", l))
	}
	return errors.New(i18n.T("revert.not_clean"))
}
