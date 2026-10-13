package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/config"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/app"
	"git.kebcloud.no/keb/lazysecrets/internal/update"
)

// updater is the app's way to find and install releases, replacing the
// binary that's running, from the servers (config releases) or the
// default ones.
func updater(servers []string) *app.Updater {
	c := &update.Client{Servers: servers}
	return &app.Updater{
		Latest: c.Latest,
		Install: func(ctx context.Context, r update.Release) error {
			exe, err := update.Executable()
			if err != nil {
				return err
			}
			return c.Install(ctx, r, exe)
		},
	}
}

// updateCmd is lazysecrets update [--check]: the newer release's
// changelog, then (unless --check) install it.
func updateCmd(configPath string, args []string) error {
	check := len(args) == 1 && args[0] == "--check"
	if len(args) > 0 && !check {
		return errors.New("usage: lazysecrets update [--check]")
	}
	cfg, err := config.LoadAll(strings.Split(configPath, ","))
	if err != nil {
		return err
	}
	current := versionString()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := &update.Client{Servers: cfg.Updates.UpdateSources}
	r, err := c.Latest(ctx)
	if err != nil {
		return fmt.Errorf("looking for a newer lazysecrets: %w", err)
	}
	if !update.Newer(current, r.Tag) {
		if !update.Newer(current, "v999.0.0") {
			return fmt.Errorf("this is a development build (%s); the latest release is %s: install it with install.sh, or go install …@%s", current, r.Tag, r.Tag)
		}
		fmt.Printf("lazysecrets %s is the latest (%s)\n", current, r.Tag)
		return nil
	}
	fmt.Printf("lazysecrets %s is out (you have %s):\n\n%s\n\n", r.Tag, current, indent(r.Notes()))
	if check {
		fmt.Println("lazysecrets update installs it.")
		return nil
	}
	exe, err := update.Executable()
	if err != nil {
		return err
	}
	fmt.Printf("Updating %s…\n", exe)
	if err := c.Install(ctx, r, exe); err != nil {
		return err
	}
	fmt.Printf("Updated to %s.\n", r.Tag)
	return nil
}

func indent(s string) string {
	if s == "" {
		return "  (no changelog)"
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}
