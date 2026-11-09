package ops

import "github.com/KadirBerkpolat1/SplitDPI/internal/config"

// ServiceState is what this machine's init says about the splitdpi service.
// It alone tells whether SplitDPI starts at boot: run mode boot only asks
// for that.
type ServiceState struct {
	Supported bool // the init has a splitdpi service
	Installed bool // its files are there
	Enabled   bool // the init starts it at boot
}

// ServiceState reads the service's state from the init's files; it runs nothing.
func (o *Ops) ServiceState() ServiceState {
	m, err := o.manager()
	if err != nil {
		return ServiceState{}
	}
	return ServiceState{Supported: true, Installed: m.Installed(), Enabled: m.Enabled()}
}

// leaveBootMode turns run mode boot into the default once no service starts
// SplitDPI at boot. A config that cannot be read stays as it is: nothing can
// read a run mode from it either.
func (o *Ops) leaveBootMode() error {
	c, err := config.Load(o.P)
	if err != nil || c.RunMode != config.RunBoot {
		return nil
	}
	c.RunMode = config.Default().RunMode
	return config.Save(o.P, c)
}
