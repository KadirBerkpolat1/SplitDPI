package presets

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// The user's apps live in one directory, one file per app named <id>.toml in
// the preset app format. A user app replaces the preset app of the same id,
// so editing a shipped app saves a full copy of it there.

// addUser lays the valid user apps in dir over s; a bad file is skipped and
// becomes one Warning.
func (s *Set) addUser(dir string) {
	fsys := os.DirFS(dir)
	for _, f := range glob(fsys, "*.toml") {
		var a App
		err := decode(fsys, f, &a, &a.Schema)
		if err == nil {
			err = validateApp(a)
		}
		if err == nil && f != a.ID+".toml" {
			err = fmt.Errorf("id %q does not match the file name", a.ID)
		}
		if err != nil {
			s.Warnings = append(s.Warnings, fmt.Sprintf("%s: %v", filepath.Join(dir, f), err))
			continue
		}
		if old, ok := s.Apps[a.ID]; ok {
			a.Preset = &old
		}
		a.User = true
		s.Apps[a.ID] = a
	}
}

// SaveUser writes a as the user app file of its id in dir.
func SaveUser(dir string, a App) error {
	a.Schema, a.User, a.Preset = schemaVersion, false, nil
	if err := validateApp(a); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(a); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".app-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, a.ID+".toml"))
}

// RemoveUser deletes the user app file of id in dir; a missing file is no error.
func RemoveUser(dir, id string) error {
	if !nameRe.MatchString(id) {
		return fmt.Errorf("invalid id %q", id)
	}
	if err := os.Remove(filepath.Join(dir, id+".toml")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
