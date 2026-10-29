package sysinfo

import "io/fs"

type Init string

const (
	Systemd   Init = "systemd"
	OpenRC    Init = "openrc"
	Runit     Init = "runit"
	OtherInit Init = "other"
)

func DetectInit(root fs.FS) Init {
	if isDir(root, "run/systemd/system") {
		return Systemd
	}
	if isDir(root, "run/openrc") {
		return OpenRC
	}
	if isDir(root, "etc/runit") && isDir(root, "var/service") {
		return Runit
	}
	return OtherInit
}

func isDir(fsys fs.FS, path string) bool {
	e, err := fs.Stat(fsys, path)
	return err == nil && e.IsDir()
}
