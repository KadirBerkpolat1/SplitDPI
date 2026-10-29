package initsys

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
	"github.com/KadirBerkpolat1/SplitDPI/internal/paths"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

var ErrUnsupportedInit = errors.New("unsupported init system")

type Manager interface {
	Install() error
	Enable(ctx context.Context) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Disable(ctx context.Context) error
	Remove() error
	Installed() bool
	LogsCmd(follow bool) []string
}

// For returns the manager for kind, writing service files under p. A manager
// needs no prior Install: revert and disable use a fresh one.
func For(kind sysinfo.Init, run execx.Runner, bin string, p paths.Paths) (Manager, error) {
	switch kind {
	case sysinfo.Systemd:
		return &systemd{run: run, bin: bin, p: p}, nil
	case sysinfo.OpenRC:
		return &openrc{run: run, bin: bin, p: p}, nil
	case sysinfo.Runit:
		return &runit{run: run, bin: bin, p: p}, nil
	default:
		return nil, ErrUnsupportedInit
	}
}

func SystemdUnitContent(bin string) string {
	return fmt.Sprintf(`[Unit]
Description=SplitDPI DPI bypass (zapret2)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s run --service
ExecStopPost=%s revert --rules-only
Restart=on-failure
RestartSec=5
RestartPreventExitStatus=3

[Install]
WantedBy=multi-user.target
`, bin, bin)
}

func OpenRCScriptContent(bin string) string {
	return fmt.Sprintf(`#!/sbin/openrc-run

description="SplitDPI DPI bypass (zapret2)"
command="%s"
command_args="run --service"
command_background="yes"
pidfile="/run/splitdpi.pid"

depend() {
	need net;
}

stop_post() {
	%s revert --rules-only;
}
`, bin, bin)
}

func RunitRunContent(bin string) string {
	return fmt.Sprintf("#!/bin/sh\nexec %s run --service 2>&1\n", bin)
}

func RunitFinishContent(bin string) string {
	return fmt.Sprintf(`#!/bin/sh
%s revert --rules-only
[ "$1" = 3 ] && sv down splitdpi
exit 0
`, bin)
}

type systemd struct {
	run execx.Runner
	bin string
	p   paths.Paths
}

func (m *systemd) Install() error {
	return writeFile(m.p.SystemdUnit, SystemdUnitContent(m.bin), 0o644)
}

func (m *systemd) Enable(ctx context.Context) error {
	if _, err := m.run(ctx, "", "systemctl", "daemon-reload"); err != nil {
		return err
	}
	_, err := m.run(ctx, "", "systemctl", "enable", "splitdpi")
	return err
}

func (m *systemd) Start(ctx context.Context) error {
	_, err := m.run(ctx, "", "systemctl", "start", "splitdpi")
	return err
}

func (m *systemd) Stop(ctx context.Context) error {
	_, err := m.run(ctx, "", "systemctl", "stop", "splitdpi")
	return err
}

func (m *systemd) Disable(ctx context.Context) error {
	_, err := m.run(ctx, "", "systemctl", "disable", "splitdpi")
	return err
}

func (m *systemd) Remove() error {
	return os.Remove(m.p.SystemdUnit)
}

func (m *systemd) Installed() bool {
	_, err := os.Stat(m.p.SystemdUnit)
	return err == nil
}

func (m *systemd) LogsCmd(follow bool) []string {
	args := []string{"journalctl", "-u", "splitdpi", "--no-pager"}
	if follow {
		args = append(args, "-f")
	}
	return args
}

type openrc struct {
	run execx.Runner
	bin string
	p   paths.Paths
}

func (m *openrc) Install() error {
	return writeFile(m.p.OpenRCScript, OpenRCScriptContent(m.bin), 0o755)
}

func (m *openrc) Enable(ctx context.Context) error {
	_, err := m.run(ctx, "", "rc-update", "add", "splitdpi", "default")
	return err
}

func (m *openrc) Start(ctx context.Context) error {
	_, err := m.run(ctx, "", "rc-service", "splitdpi", "start")
	return err
}

func (m *openrc) Stop(ctx context.Context) error {
	_, err := m.run(ctx, "", "rc-service", "splitdpi", "stop")
	return err
}

func (m *openrc) Disable(ctx context.Context) error {
	_, err := m.run(ctx, "", "rc-update", "del", "splitdpi", "default")
	return err
}

func (m *openrc) Remove() error {
	return os.Remove(m.p.OpenRCScript)
}

func (m *openrc) Installed() bool {
	_, err := os.Stat(m.p.OpenRCScript)
	return err == nil
}

func (m *openrc) LogsCmd(bool) []string { return nil }

type runit struct {
	run execx.Runner
	bin string
	p   paths.Paths
}

func (m *runit) Install() error {
	if err := writeFile(filepath.Join(m.p.RunitDir, "run"), RunitRunContent(m.bin), 0o755); err != nil {
		return err
	}
	return writeFile(filepath.Join(m.p.RunitDir, "finish"), RunitFinishContent(m.bin), 0o755)
}

func (m *runit) Enable(context.Context) error {
	if err := os.MkdirAll(filepath.Dir(m.p.RunitLink), 0o755); err != nil {
		return err
	}
	_ = os.Remove(m.p.RunitLink)
	target, err := filepath.Abs(m.p.RunitDir)
	if err != nil {
		return err
	}
	return os.Symlink(target, m.p.RunitLink)
}

func (m *runit) Start(ctx context.Context) error {
	_, err := m.run(ctx, "", "sv", "up", "splitdpi")
	return err
}

func (m *runit) Stop(ctx context.Context) error {
	_, err := m.run(ctx, "", "sv", "down", "splitdpi")
	return err
}

func (m *runit) Disable(context.Context) error {
	if err := os.Remove(m.p.RunitLink); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *runit) Remove() error {
	_ = os.Remove(m.p.RunitLink)
	return os.RemoveAll(m.p.RunitDir)
}

func (m *runit) Installed() bool {
	_, err := os.Stat(filepath.Join(m.p.RunitDir, "run"))
	return err == nil
}

func (m *runit) LogsCmd(bool) []string { return nil }

func writeFile(path, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), mode)
}
