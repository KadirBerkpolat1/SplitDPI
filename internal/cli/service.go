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

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
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
	r, err := o.Enable(ctx, nil)
	if r.Stopped != "" { // the service took its place, even if saving run mode boot failed
		fmt.Fprintln(w, i18n.T("service.took_over", i18n.Enum("mode", r.Stopped), r.PID))
	}
	var takeover ops.TakeoverError
	var conflict ops.ConflictError
	var already supervisor.ErrAlreadyRunning
	var nobin ops.BinaryMissingError
	var unsafe ops.UnsafeBinaryError
	switch {
	case err == nil:
		fmt.Fprintln(w, i18n.T("service.enabled"))
		return nil
	case errors.As(err, &takeover):
		return errors.New(takeoverText(w, takeover))
	case errors.Is(err, render.ErrNoStrategy):
		return errors.New(i18n.T("run.no_strategy"))
	case errors.Is(err, initsys.ErrUnsupportedInit):
		return errors.New(i18n.T("service.unsupported"))
	case errors.As(err, &nobin):
		return errors.New(i18n.T("service.no_binary", nobin.Path, nobin.Path))
	case errors.As(err, &unsafe):
		return errors.New(unsafeText(unsafe))
	case errors.As(err, &conflict):
		printChecks(w, conflict.Checks)
		return errors.New(i18n.T("run.conflict"))
	case errors.As(err, &already):
		return errors.New(i18n.T("run.already", i18n.Enum("mode", already.Mode)))
	}
	return err
}

// unsafeText words an UnsafeBinaryError: what is wrong, and either where to
// install splitdpi instead or, for the default place, what to fix.
func unsafeText(e ops.UnsafeBinaryError) string {
	reason := i18n.T("service.unsafe_"+e.Reason, e.At)
	if e.Path == e.Install {
		return i18n.T("service.unsafe_default", e.Path, reason)
	}
	return i18n.T("service.unsafe", e.Path, reason, e.Path, e.Install)
}

// takeoverText words a TakeoverError, one line each: why the service did not
// take over, whether removing it failed, and what runs the bypass now.
func takeoverText(w io.Writer, e ops.TakeoverError) string {
	var stop ops.StopRunError
	var lines []string
	switch {
	case errors.As(e.Err, &stop):
		lines = append(lines, i18n.T("service.stop_failed", i18n.Enum("mode", stop.Mode), stop.PID, stop.Err))
	case errors.Is(e.Err, ops.ErrServiceNotStarted):
		lines = append(lines, i18n.T("service.not_started"))
	default:
		lines = append(lines, i18n.T("service.start_failed", e.Err))
	}
	if e.Undo != nil {
		lines = append(lines, i18n.T("service.undo_failed", e.Undo))
	}
	switch {
	case e.Stopped == "":
	case e.Running != "":
		lines = append(lines, i18n.T("service.takeover_kept", i18n.Enum("mode", e.Running)))
	case e.RestartErr != nil:
		lines = append(lines, runError(w, e.RestartErr, false).Error(), i18n.T("service.takeover_down"))
	default:
		lines = append(lines, i18n.T("service.takeover_restarted", e.Restarted))
	}
	return strings.Join(lines, "\n")
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

// printBoot says whether SplitDPI starts at boot, as the init has it, and
// warns when the run mode disagrees. A config that cannot be read (cfgErr)
// has no run mode to compare: then it says why instead.
func printBoot(w io.Writer, s ops.ServiceState, mode config.RunMode, cfgErr error) {
	switch {
	case s.Enabled:
		fmt.Fprintln(w, i18n.T("status.boot_yes"))
	case !s.Supported:
		fmt.Fprintln(w, i18n.T("status.boot_unsupported"))
	default:
		fmt.Fprintln(w, i18n.T("status.boot_no"))
	}
	if cfgErr != nil {
		fmt.Fprintln(w, i18n.T("status.run_mode_unknown", cfgErr))
		return
	}
	warnBoot(w, s, mode)
}

// warnBoot warns when run mode and the service disagree: boot without the
// service, or another mode while the service still starts SplitDPI at boot.
// Saving a run mode changes neither.
func warnBoot(w io.Writer, s ops.ServiceState, mode config.RunMode) {
	switch {
	case mode == config.RunBoot && !s.Supported:
		fmt.Fprintln(w, i18n.T("service.unsupported"))
	case mode == config.RunBoot && !s.Enabled:
		fmt.Fprintln(w, i18n.T("run_mode.boot_not_enabled"))
	case mode != config.RunBoot && s.Enabled:
		fmt.Fprintln(w, i18n.T("run_mode.boot_still_enabled"))
	}
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
