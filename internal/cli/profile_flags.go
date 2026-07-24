package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type repeatedStrings []string

func (v *repeatedStrings) String() string { return strings.Join(*v, ",") }
func (v *repeatedStrings) Set(value string) error {
	*v = append(*v, value)
	return nil
}

type profileOptions struct {
	provider, mode, alias, mouse, prefix, backDetach string
	scrollback                                       int
	unsets                                           repeatedStrings
	changed                                          map[string]bool
}

func newProfileFlagSet(name string) (*flag.FlagSet, *profileOptions) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := &profileOptions{}
	fs.StringVar(&opts.provider, "provider", "", "provider")
	fs.StringVar(&opts.mode, "mode", "", "safe or yolo")
	fs.StringVar(&opts.alias, "alias", "", "command alias")
	fs.StringVar(&opts.mouse, "mouse", "", "auto, on, or off")
	fs.StringVar(&opts.prefix, "prefix", "", "control prefix")
	fs.StringVar(&opts.backDetach, "back-detach", "", "auto, on, or off")
	fs.IntVar(&opts.scrollback, "scrollback", 0, "scrollback lines")
	fs.Var(&opts.unsets, "unset", "field to inherit (repeatable)")
	return fs, opts
}

func (o *profileOptions) captureChanged(fs *flag.FlagSet) {
	o.changed = map[string]bool{}
	fs.Visit(func(f *flag.Flag) { o.changed[f.Name] = true })
}

func (o *profileOptions) applyProfile(profile *store.Profile) error {
	for _, field := range o.unsets {
		if err := unsetProfileField(profile, field); err != nil {
			return err
		}
	}
	if o.changed["provider"] {
		profile.Provider = pointerValue(o.provider)
	}
	return o.applyShared(&profile.Mode, &profile.CommandAlias, &profile.Mouse, &profile.ControlPrefix, &profile.BackDetach, &profile.ScrollbackLines)
}

func (o *profileOptions) applyOverrides(overrides *store.SessionProfileOverrides) error {
	for _, field := range o.unsets {
		if field == "provider" {
			return errors.New("provider cannot be unset on a session override")
		}
		profile := store.Profile{Mode: overrides.Mode, CommandAlias: overrides.CommandAlias, Mouse: overrides.Mouse, ControlPrefix: overrides.ControlPrefix, BackDetach: overrides.BackDetach, ScrollbackLines: overrides.ScrollbackLines}
		if err := unsetProfileField(&profile, field); err != nil {
			return err
		}
		overrides.Mode, overrides.CommandAlias, overrides.Mouse = profile.Mode, profile.CommandAlias, profile.Mouse
		overrides.ControlPrefix, overrides.BackDetach, overrides.ScrollbackLines = profile.ControlPrefix, profile.BackDetach, profile.ScrollbackLines
	}
	return o.applyShared(&overrides.Mode, &overrides.CommandAlias, &overrides.Mouse, &overrides.ControlPrefix, &overrides.BackDetach, &overrides.ScrollbackLines)
}

func (o *profileOptions) applyShared(mode **store.Mode, alias **string, mouse **store.MousePolicy, prefix **string, backDetach **bool, scrollback **int) error {
	if o.changed["mode"] {
		value := store.Mode(o.mode)
		*mode = &value
	}
	if o.changed["alias"] {
		*alias = pointerValue(o.alias)
	}
	if o.changed["mouse"] {
		value := store.MousePolicy(o.mouse)
		*mouse = &value
	}
	if o.changed["prefix"] {
		*prefix = pointerValue(o.prefix)
	}
	if o.changed["back-detach"] {
		switch o.backDetach {
		case "auto":
			*backDetach = nil
		case "on", "off":
			*backDetach = pointerValue(o.backDetach == "on")
		default:
			return fmt.Errorf("invalid back-detach policy %q", o.backDetach)
		}
	}
	if o.changed["scrollback"] {
		*scrollback = pointerValue(o.scrollback)
	}
	return nil
}

func unsetProfileField(profile *store.Profile, field string) error {
	switch field {
	case "provider":
		profile.Provider = nil
	case "mode":
		profile.Mode = nil
	case "alias":
		profile.CommandAlias = nil
	case "mouse":
		profile.Mouse = nil
	case "prefix":
		profile.ControlPrefix = nil
	case "back-detach":
		profile.BackDetach = nil
	case "scrollback":
		profile.ScrollbackLines = nil
	default:
		return fmt.Errorf("unknown profile field %q", field)
	}
	return nil
}

func pointerValue[T any](value T) *T { return &value }
