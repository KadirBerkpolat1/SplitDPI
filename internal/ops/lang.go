package ops

import (
	"errors"
	"slices"
	"strings"

	"github.com/KadirBerkpolat1/SplitDPI/internal/config"
	"github.com/KadirBerkpolat1/SplitDPI/internal/i18n"
)

// SetLang saves the UI language; it applies when no --lang is given.
// "" follows the system language.
func (o *Ops) SetLang(lang string) error {
	if lang != "" && !slices.Contains(i18n.Supported(), lang) {
		return errors.New(i18n.T("config.err_lang", lang, strings.Join(i18n.Supported(), ", ")))
	}
	return o.updateConfig(func(c *config.Config) error {
		c.Lang = lang
		return nil
	})
}

// SetRunMode saves how the TUI's run key starts the bypass; it starts or
// stops nothing.
func (o *Ops) SetRunMode(mode config.RunMode) error {
	if mode != config.RunWindow && mode != config.RunBackground && mode != config.RunBoot {
		return errors.New(i18n.T("config.err_run_mode", string(mode)))
	}
	return o.updateConfig(func(c *config.Config) error {
		c.RunMode = mode
		return nil
	})
}
