// Package paths holds every fixed filesystem path and name splitdpi uses.
package paths

import "path/filepath"

const name = "splitdpi"

type Paths struct {
	Root         string
	ConfigDir    string
	ConfigFile   string
	UserExclude  string
	UserHosts    string
	StateDir     string
	EngineDir    string
	PresetCache  string
	AutoHostlist string
	TakeoverFile string
	RunDir       string
	LockFile     string
	StatusFile   string
	HostlistFile string
	ExcludeFile  string
	LogFile      string
	BinPath      string
	SystemdUnit  string
	OpenRCScript string
	RunitDir     string
	RunitLink    string
	NftTable     string
}

// Under returns the paths prefixed by root. An empty root means "/".
func Under(root string) Paths {
	if root == "" {
		root = "/"
	}
	j := func(p ...string) string { return filepath.Join(append([]string{root}, p...)...) }
	cfg := j("etc", name)
	state := j("var/lib", name)
	run := j("run", name)
	return Paths{
		Root:         filepath.Clean(root),
		ConfigDir:    cfg,
		ConfigFile:   filepath.Join(cfg, "config.toml"),
		UserExclude:  filepath.Join(cfg, "exclude.txt"),
		UserHosts:    filepath.Join(cfg, "hosts.txt"),
		StateDir:     state,
		EngineDir:    filepath.Join(state, "engine"),
		PresetCache:  filepath.Join(state, "presets"),
		AutoHostlist: filepath.Join(state, "autohostlist.txt"),
		TakeoverFile: filepath.Join(state, "takeover.json"),
		RunDir:       run,
		LockFile:     filepath.Join(run, "lock"),
		StatusFile:   filepath.Join(run, "status.json"),
		HostlistFile: filepath.Join(run, "hostlist.txt"),
		ExcludeFile:  filepath.Join(run, "exclude.txt"),
		LogFile:      j("var/log", name+".log"),
		BinPath:      j("usr/local/bin", name),
		SystemdUnit:  j("etc/systemd/system", name+".service"),
		OpenRCScript: j("etc/init.d", name),
		RunitDir:     j("etc/sv", name),
		RunitLink:    j("var/service", name),
		NftTable:     name,
	}
}
