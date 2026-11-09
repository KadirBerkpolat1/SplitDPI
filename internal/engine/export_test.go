package engine

import "github.com/KadirBerkpolat1/SplitDPI/internal/safetar"

// SetLimits makes Install unpack with lim until the returned func runs.
func SetLimits(lim safetar.Limits) (restore func()) {
	old := limits
	limits = lim
	return func() { limits = old }
}

// SetRename makes Install rename with f until the returned func runs.
func SetRename(f func(oldpath, newpath string) error) (restore func()) {
	old := rename
	rename = f
	return func() { rename = old }
}
