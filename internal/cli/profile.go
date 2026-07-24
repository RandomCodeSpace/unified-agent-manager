package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func runProfile(ctx context.Context, svc *app.Service, args []string) error {
	if len(args) == 0 {
		return errors.New("profile requires ls, show, set, rm, default, assign, override, or effective")
	}
	switch args[0] {
	case "ls":
		return runProfileList(svc, args[1:])
	case "show":
		return runProfileShow(svc, args[1:])
	case "set":
		return runProfileSet(svc, args[1:])
	case "rm":
		return runProfileRemove(svc, args[1:])
	case "default":
		return runProfileDefault(svc, args[1:])
	case "assign":
		return runProfileAssign(svc, args[1:])
	case "override":
		return runProfileOverride(svc, args[1:])
	case "effective":
		return runProfileEffective(ctx, svc, args[1:])
	default:
		return fmt.Errorf("unknown profile command %q", args[0])
	}
}

func runProfileList(svc *app.Service, args []string) error {
	fs := flag.NewFlagSet("profile ls", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return errors.New("profile ls takes no arguments")
	}
	list, err := svc.ListProfiles()
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(list)
	}
	for _, profile := range list.Profiles {
		marker := ""
		if profile.Default {
			marker = "\tdefault"
		}
		fmt.Printf("%s%s\n", profile.Name, marker)
	}
	return nil
}

func runProfileShow(svc *app.Service, args []string) error {
	if len(args) == 0 {
		return errors.New("profile show requires <name>")
	}
	fs := flag.NewFlagSet("profile show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return errors.New("profile show accepts one name")
	}
	profile, err := svc.Profile(args[0])
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(profile)
	}
	fmt.Printf("%s\tdefault=%t\n", profile.Name, profile.Default)
	return nil
}

func runProfileSet(svc *app.Service, args []string) error {
	if len(args) == 0 {
		return errors.New("profile set requires <name>")
	}
	fs, opts := newProfileFlagSet("profile set")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return errors.New("profile set accepts one name")
	}
	opts.captureChanged(fs)
	return svc.UpdateProfile(args[0], func(profile *store.Profile) error { return opts.applyProfile(profile) })
}

func runProfileRemove(svc *app.Service, args []string) error {
	name, err := exactlyOne(args, "profile rm requires <name>")
	if err != nil {
		return err
	}
	return svc.RemoveProfile(name)
}

func runProfileDefault(svc *app.Service, args []string) error {
	name, err := exactlyOne(args, "profile default requires <name|none>")
	if err != nil {
		return err
	}
	return svc.SetDefaultProfile(name)
}

func runProfileAssign(svc *app.Service, args []string) error {
	if len(args) != 2 {
		return errors.New("profile assign requires <session-id> <name|none>")
	}
	return svc.AssignProfileExact(args[0], args[1])
}

func runProfileOverride(svc *app.Service, args []string) error {
	if len(args) == 0 {
		return errors.New("profile override requires <session-id>")
	}
	fs, opts := newProfileFlagSet("profile override")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return errors.New("profile override accepts one session ID")
	}
	opts.captureChanged(fs)
	return svc.UpdateProfileOverridesExact(args[0], opts.provider, func(overrides *store.SessionProfileOverrides) error {
		return opts.applyOverrides(overrides)
	})
}

func runProfileEffective(ctx context.Context, svc *app.Service, args []string) error {
	if len(args) == 0 {
		return errors.New("profile effective requires <session-id>")
	}
	fs := flag.NewFlagSet("profile effective", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return errors.New("profile effective accepts one session ID")
	}
	profile, err := svc.EffectiveProfileExact(ctx, args[0])
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(profile)
	}
	fmt.Printf("%s\t%s\t%s\t%s\n", profile.SessionID, profile.Provider, profile.SelectedProfile, profile.EffectiveProfile)
	return nil
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func exactlyOne(args []string, message string) (string, error) {
	if len(args) != 1 {
		return "", errors.New(message)
	}
	return args[0], nil
}
