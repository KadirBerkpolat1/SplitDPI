package ops

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/initsys"
	"github.com/KadirBerkpolat1/SplitDPI/internal/sysinfo"
)

// Foreign is another zapret install on this machine (bol-van's zapret or
// zapret2, or an installer such as keift's that sets up one of them).
type Foreign struct {
	Kind     string                 // "zapret" or "zapret2", also its service name
	Dir      string                 // install dir; "" if there is none
	Service  initsys.ForeignService // zero if it has no service
	Hostlist string                 // its user site list; "" if there is none
	Running  bool
}

type foreignKind struct {
	name, dir string
	procs     []string
}

var foreignKinds = []foreignKind{
	{"zapret", "opt/zapret", []string{"nfqws", "tpws"}},
	{"zapret2", "opt/zapret2", []string{"nfqws2"}},
}

// DetectForeign lists the zapret installs found through their processes,
// services or install dirs. It returns nothing on a clean system.
func (o *Ops) DetectForeign(ctx context.Context) ([]Foreign, error) {
	procs, err := doctor.ConflictProcs(o.doctorEnv())
	if err != nil {
		return nil, err
	}
	in := sysinfo.DetectInit(o.Root)
	var found []Foreign
	for _, k := range foreignKinds {
		f := Foreign{Kind: k.name, Running: slices.ContainsFunc(k.procs, func(p string) bool { return slices.Contains(procs, p) })}
		if _, err := fs.Stat(o.Root, k.dir); err == nil {
			f.Dir = "/" + k.dir
			// zapret itself uses the .gz when both exist.
			for _, name := range []string{"zapret-hosts-user.txt.gz", "zapret-hosts-user.txt"} {
				if _, err := fs.Stat(o.Root, k.dir+"/ipset/"+name); err == nil {
					f.Hostlist = f.Dir + "/ipset/" + name
					break
				}
			}
		}
		f.Service = initsys.FindForeign(ctx, o.Root, o.Exec, in, k.name)
		if f.Dir != "" || f.Service.Name != "" || f.Running {
			found = append(found, f)
		}
	}
	return found, nil
}

// TakeoverReport is what Takeover changed.
type TakeoverReport struct {
	Added    []string
	Skipped  []ImportSkip
	Stopped  bool // its service was stopped
	Disabled bool // it no longer starts on boot
}

// Takeover moves f's sites into the user's sites, then stops its service and
// turns off its start on boot, whatever the init said about it. It records
// what it turned off before touching it, so Revert can turn it back on; a
// service whose boot state is unknown is recorded as starting on boot. A
// Running f without a service keeps running: there is no service to stop it with.
func (o *Ops) Takeover(ctx context.Context, f Foreign) (TakeoverReport, error) {
	var r TakeoverReport
	if f.Hostlist != "" {
		var err error
		if r.Added, r.Skipped, err = o.importHostlist(f.Hostlist); err != nil {
			return r, err
		}
	}
	if f.Service.Name == "" {
		return r, nil
	}
	t := takenService{Service: f.Service, Start: f.Running}
	t.Service.Enabled = f.Service.MayStartOnBoot()
	if t.Start || t.Service.Enabled {
		if err := o.recordTakeover(t); err != nil {
			return r, err
		}
	}
	if t.Start {
		if err := f.Service.Stop(ctx, o.Exec, o.P.Root); err != nil {
			return r, err
		}
		r.Stopped = true
	}
	if err := f.Service.Disable(ctx, o.Exec, o.P.Root); err != nil {
		return r, err
	}
	r.Disabled = true
	return r, nil
}

// importHostlist imports a host path read through Root, gunzipping a .gz.
func (o *Ops) importHostlist(path string) ([]string, []ImportSkip, error) {
	f, err := o.Root.Open(strings.TrimPrefix(path, "/"))
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		z, err := gzip.NewReader(f)
		if err != nil {
			return nil, nil, err
		}
		defer z.Close()
		r = z
	}
	return o.importSites(r)
}

// takenService is a service Takeover turned off. Service.Enabled means it
// started on boot; Start means it was running.
type takenService struct {
	Service initsys.ForeignService `json:"service"`
	Start   bool                   `json:"start"`
}

type takeoverRecord struct {
	Services []takenService `json:"services"`
}

// readTakeover returns the record, empty if there is none.
func readTakeover(path string) (takeoverRecord, error) {
	var rec takeoverRecord
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return rec, nil
	}
	if err != nil {
		return rec, err
	}
	return rec, json.Unmarshal(b, &rec)
}

func writeTakeover(path string, rec takeoverRecord) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// recordTakeover adds t to the record. A service taken over twice keeps what
// the first takeover saw: by the second it is already stopped and disabled.
func (o *Ops) recordTakeover(t takenService) error {
	rec, err := readTakeover(o.P.TakeoverFile)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(rec.Services, func(e takenService) bool {
		return e.Service.Init == t.Service.Init && e.Service.Name == t.Service.Name
	})
	if i < 0 {
		rec.Services = append(rec.Services, t)
	} else {
		e := &rec.Services[i]
		if t.Service.Enabled {
			e.Service = t.Service
		}
		e.Start = e.Start || t.Start
	}
	if err := os.MkdirAll(o.P.StateDir, 0o755); err != nil {
		return err
	}
	return writeTakeover(o.P.TakeoverFile, rec)
}

// TakenOver lists the services Takeover turned off that Revert would turn
// back on, as "name (init)"; nothing when there is no record.
func (o *Ops) TakenOver() ([]string, error) {
	rec, err := readTakeover(o.P.TakeoverFile)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(rec.Services))
	for i, t := range rec.Services {
		out[i] = t.Service.Name + " (" + string(t.Service.Init) + ")"
	}
	return out, nil
}

// restoreTakeover turns the services in the takeover record back on and
// removes the record. Services that fail stay in the record for a later revert.
func (o *Ops) restoreTakeover(ctx context.Context, r *RevertReport) {
	rec, err := readTakeover(o.P.TakeoverFile)
	if err != nil {
		r.Leftover = append(r.Leftover, o.P.TakeoverFile+": "+err.Error())
		return
	}
	if len(rec.Services) == 0 {
		if err := os.Remove(o.P.TakeoverFile); err != nil && !os.IsNotExist(err) {
			r.Leftover = append(r.Leftover, o.P.TakeoverFile+": "+err.Error())
		}
		return
	}
	var failed []takenService
	for _, t := range rec.Services {
		s := t.Service
		label := s.Name + " (" + string(s.Init) + ")"
		var err error
		if s.Enabled {
			err = s.Enable(ctx, o.Exec, o.P.Root)
		}
		if err == nil && t.Start {
			err = s.Start(ctx, o.Exec, o.P.Root)
		}
		if err != nil {
			r.Leftover = append(r.Leftover, label+": "+err.Error())
			failed = append(failed, t)
			continue
		}
		r.Restored = append(r.Restored, label)
	}
	if len(failed) > 0 {
		err = writeTakeover(o.P.TakeoverFile, takeoverRecord{Services: failed})
	} else {
		err = os.Remove(o.P.TakeoverFile)
	}
	if err != nil {
		r.Leftover = append(r.Leftover, o.P.TakeoverFile+": "+err.Error())
	}
}
