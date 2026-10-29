package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
	"github.com/KadirBerkpolat1/SplitDPI/internal/initsys"
	"github.com/KadirBerkpolat1/SplitDPI/internal/ops"
	"github.com/KadirBerkpolat1/SplitDPI/internal/render"
	"github.com/KadirBerkpolat1/SplitDPI/internal/supervisor"
)

func init() {
	commands["enable"] = Command{Name: "enable", Root: true, Help: "cmd.enable.help", Run: runEnable}
	commands["disable"] = Command{Name: "disable", Root: true, Help: "cmd.disable.help", Run: runDisable}
	commands["logs"] = Command{Name: "logs", Root: true, Help: "cmd.logs.help", Run: runLogs}
}

func runEnable(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	if len(args) != 0 {
		return usageError(i18n.T("service.enable_usage"))
	}
	err := o.Enable(ctx)
	var conflict ops.ConflictError
	var already supervisor.ErrAlreadyRunning
	var nobin ops.BinaryMissingError
	switch {
	case err == nil:
		fmt.Fprintln(w, i18n.T("service.enabled"))
		return nil
	case errors.Is(err, render.ErrNoStrategy):
		return errors.New(i18n.T("run.no_strategy"))
	case errors.Is(err, initsys.ErrUnsupportedInit):
		return errors.New(i18n.T("service.unsupported"))
	case errors.As(err, &nobin):
		return errors.New(i18n.T("service.no_binary", nobin.Path, nobin.Path))
	case errors.As(err, &conflict):
		printChecks(w, conflict.Checks)
		return errors.New(i18n.T("run.conflict"))
	case errors.As(err, &already):
		return errors.New(i18n.T("run.already", i18n.Enum("mode", already.Mode)))
	case errors.Is(err, ops.ErrServiceNotStarted):
		return errors.New(i18n.T("service.not_started"))
	}
	return err
}

func runDisable(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	if len(args) != 0 {
		return usageError(i18n.T("service.disable_usage"))
	}
	switch err := o.Disable(ctx); {
	case errors.Is(err, ops.ErrNotEnabled):
		fmt.Fprintln(w, i18n.T("service.not_enabled"))
		return nil
	case errors.Is(err, initsys.ErrUnsupportedInit):
		return errors.New(i18n.T("service.unsupported"))
	case err != nil:
		return err
	}
	fmt.Fprintln(w, i18n.T("service.disabled"))
	return nil
}

func runLogs(ctx context.Context, o *ops.Ops, w io.Writer, args []string) error {
	follow := false
	switch {
	case len(args) == 0:
	case len(args) == 1 && (args[0] == "-f" || args[0] == "--follow"):
		follow = true
	default:
		return usageError(i18n.T("service.logs_usage"))
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := o.Logs(ctx, follow, w); errors.Is(err, ops.ErrNoLogs) {
		fmt.Fprintln(w, i18n.T("service.no_logs"))
	} else if err != nil {
		return err
	}
	return nil
}
