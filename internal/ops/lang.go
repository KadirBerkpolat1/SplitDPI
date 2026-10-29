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
