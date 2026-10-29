package ops

import (
	"context"
	"io/fs"
	"os/exec"
	"path"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/doctor"
	"github.com/KadirBerkpolat1/SplitDPI/internal/probe"
)

func (o *Ops) Doctor(ctx context.Context) []doctor.Check {
	return doctor.Run(ctx, o.doctorEnv())
}

// doctorEnv is the one doctor view of this machine, shared by `doctor` and the
// conflict checks of `run` and `scan`.
func (o *Ops) doctorEnv() doctor.Env {
	return doctor.Env{
		Root:     o.Root,
		P:        o.P,
		Run:      o.Exec,
		LookPath: exec.LookPath,
		Procs:    o.procs,
		Euid:     o.Euid,
		DNSCheck: func(ctx context.Context) []probe.DNSResult {
			c, err := config.Load(o.P)
			if err != nil || len(c.Apps) == 0 {
				return nil
			}
			return o.ProbeDNS(ctx, c.Apps)
		},
	}
}

// procs lists every process from /proc/*/stat, with its executable from
// /proc/*/exe. Processes that exit while being read are skipped.
func (o *Ops) procs() ([]doctor.Proc, error) {
	files, err := fs.Glob(o.Root, "proc/[0-9]*/stat")
	if err != nil {
		return nil, err
	}
	ps := make([]doctor.Proc, 0, len(files))
	for _, f := range files {
		if b, err := fs.ReadFile(o.Root, f); err == nil {
			if p, ok := doctor.ParseProcStat(string(b)); ok {
				p.Exe, _ = fs.ReadLink(o.Root, path.Dir(f)+"/exe")
				ps = append(ps, p)
			}
		}
	}
	return ps, nil
}
