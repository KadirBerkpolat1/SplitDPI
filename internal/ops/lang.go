package ops

import "github.com/KadirBerkpolat1/SplitDPI/internal/config"

// SetLang saves the UI language; it applies when no --lang is given.
func (o *Ops) SetLang(lang string) error {
	c, err := config.Load(o.P)
	if err != nil {
		return err
	}
	c.Lang = lang
	return config.Save(o.P, c)
}

// SetRunMode saves how the TUI's run key starts the bypass; it starts or
// stops nothing.
func (o *Ops) SetRunMode(mode config.RunMode) error {
	c, err := config.Load(o.P)
	if err != nil {
		return err
	}
	c.RunMode = mode
	return config.Save(o.P, c)
}
