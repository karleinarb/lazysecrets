package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"git.kebcloud.no/keb/lazysecrets/internal/config"
	"git.kebcloud.no/keb/lazysecrets/internal/instance"
	"git.kebcloud.no/keb/lazysecrets/internal/linkhandler"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/app"
)

// handler installs or removes the lazysecrets:// link handler.
func handler(configPath string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: lazysecrets handler install [--terminal NAME] | uninstall")
	}
	switch args[0] {
	case "install":
		fs := flag.NewFlagSet("handler install", flag.ContinueOnError)
		term := fs.String("terminal", "", "terminal to open links in: "+strings.Join(linkhandler.Terminals, ", ")+
			" (default: the one this runs in)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		cfg, err := config.LoadAll(strings.Split(configPath, ","))
		if err != nil {
			return err
		}
		where, opens, err := installHandler(cfg, configPath, *term)
		if err != nil {
			return err
		}
		fmt.Printf("lazysecrets:// links now open in a new %s window.\nInstalled %s\n", opens, where)
		if runtime.GOOS == "darwin" {
			fmt.Println("Try it: open 'lazysecrets://azure/<vault>.vault.azure.net'")
		}
		return nil
	case "uninstall":
		where, err := linkhandler.Uninstall()
		if err != nil {
			return err
		}
		fmt.Printf("Removed %s; lazysecrets:// links no longer open lazysecrets.\n", where)
		return nil
	}
	return fmt.Errorf("unknown handler command %q: install or uninstall", args[0])
}

// installHandler registers this lazysecrets for lazysecrets:// links,
// opening them in term (default: the terminal it runs in). It returns
// where it installed and what links open in.
func installHandler(cfg config.Config, configPath, term string) (string, string, error) {
	exe, err := linkhandler.Executable()
	if err != nil {
		return "", "", err
	}
	if term == "" {
		term = linkhandler.Detect(os.Getenv)
	}
	// Check now that a link could be opened, rather than on the first
	// click.
	if _, err := launchCommand(cfg, configPath, term, exe, "lazysecrets://check"); err != nil {
		return "", "", err
	}
	launch := []string{exe}
	if configPath != defaultConfigPath() {
		launch = append(launch, "--config", configPath)
	}
	launch = append(launch, "launch")
	if term != "" {
		launch = append(launch, "--terminal", term)
	}
	where, err := linkhandler.Install(launch, linkhandler.Env(os.Getenv))
	if err != nil {
		return "", "", err
	}
	opens := term
	if len(cfg.LinkHandler.Command) > 0 {
		opens = strings.Join(cfg.LinkHandler.Command, " ")
	} else if cfg.LinkHandler.Terminal != "" {
		opens = cfg.LinkHandler.Terminal
	}
	return where, opens, nil
}

// handlerSetup offers to set up the link handler at startup, if links
// don't open lazysecrets and it can be: not for a go run binary, which is
// deleted on exit, nor when the config says not to ask.
func handlerSetup(cfg config.Config, configPath string) *app.HandlerSetup {
	if !config.On(cfg.LinkHandler.Prompt) {
		return nil
	}
	if _, err := linkhandler.Executable(); err != nil {
		return nil
	}
	reason := linkhandler.Check()
	if reason == "" {
		return nil
	}
	return &app.HandlerSetup{
		Reason: reason,
		Install: func() (string, error) {
			_, opens, err := installHandler(cfg, configPath, "")
			return opens, err
		},
	}
}

// launch opens link in lazysecrets in a new terminal window. The
// operating system runs it when a lazysecrets:// link is opened.
func launch(configPath string, args []string) error {
	fs := flag.NewFlagSet("launch", flag.ContinueOnError)
	term := fs.String("terminal", "", "terminal to open the window in")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: lazysecrets launch [--terminal NAME] <link>")
	}
	link := fs.Arg(0)
	// Links can come from any web page: open only what lazysecrets can
	// read. Opening never acts by itself; the user picks what to do.
	if strings.ContainsAny(link, "\r\n") {
		return fmt.Errorf("not a link: %q", link)
	}
	if _, err := ref.Parse(link); err != nil {
		return err
	}
	cfg, err := config.LoadAll(strings.Split(configPath, ","))
	if err != nil {
		return err
	}
	// Where the link was clicked: a terminal gets it there, anywhere else
	// the lazysecrets used last does.
	front := linkhandler.FrontApp()
	inTerminal := linkhandler.IsTerminal(front.Bundle)
	if config.On(cfg.LinkHandler.Reuse) && handOver(link, front.Bundle, inTerminal) {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	argv, err := launchCommand(cfg, configPath, *term, exe, link)
	if err != nil {
		return err
	}
	return linkhandler.Launch(argv)
}

// handOver gives link to the running lazysecrets last used that can be
// brought into view, if there is one; clicked in a terminal, only to one
// in that terminal.
func handOver(link, front string, inTerminal bool) bool {
	stateDir, err := config.StateDir()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	best, ok := instance.Best(instance.Running(ctx, instance.Dir(stateDir), front), front, inTerminal)
	if !ok {
		return false
	}
	// A fresh deadline: the hand-over must not fail for time the status
	// round used, or the link would open twice.
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return instance.Open(ctx, best, link, front) == nil
}

// launchCommand is the command opening a new window of the configured
// terminal (or term) running lazysecrets on link.
func launchCommand(cfg config.Config, configPath, term, exe, link string) ([]string, error) {
	if cfg.LinkHandler.Terminal != "" {
		term = cfg.LinkHandler.Terminal
	}
	return linkhandler.Command(runtime.GOOS, term, cfg.LinkHandler.Command, innerCommand(configPath, exe, link))
}

// innerCommand runs lazysecrets on link, with the environment it needs.
func innerCommand(configPath, exe, link string) []string {
	inner := append(append([]string{"/usr/bin/env"}, linkhandler.Env(os.Getenv)...), exe)
	if configPath != defaultConfigPath() {
		inner = append(inner, "--config", configPath)
	}
	return append(inner, "open", link)
}
