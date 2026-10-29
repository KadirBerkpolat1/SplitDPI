package sysinfo

import (
	"io/fs"
	"strings"
)

type LSM struct {
	SELinux  string // enforcing|permissive|disabled
	AppArmor bool
}

func DetectLSM(root fs.FS) LSM {
	var l LSM
	l.SELinux = "disabled"
	if b, err := fs.ReadFile(root, "sys/fs/selinux/enforce"); err == nil {
		switch strings.TrimSpace(string(b)) {
		case "1":
			l.SELinux = "enforcing"
		case "0":
			l.SELinux = "permissive"
		}
	}
	if b, err := fs.ReadFile(root, "sys/module/apparmor/parameters/enabled"); err == nil {
		v := strings.TrimSpace(string(b))
		l.AppArmor = v == "Y" || strings.EqualFold(v, "yes")
	}
	return l
}
