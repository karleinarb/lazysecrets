// Command lazysecrets is a terminal UI for browsing secrets in Azure Key
// Vault and Databricks secret scopes.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"git.kebcloud.no/keb/lazysecrets"
	"git.kebcloud.no/keb/lazysecrets/internal/commands"
	"git.kebcloud.no/keb/lazysecrets/internal/config"
	"git.kebcloud.no/keb/lazysecrets/internal/instance"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/azure"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/databricks"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/fake"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/repo"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/app"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/keys"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/styles"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazysecrets:", err)
		os.Exit(1)
	}
}

// looksLikeRef tells a reference given without "open" from a mistyped
// subcommand, so the latter still gets the usage text.
func looksLikeRef(s string) bool {
	return strings.Contains(s, "://") || strings.HasPrefix(s, "@Microsoft.KeyVault(") || strings.HasPrefix(s, "{{secrets/")
}

// starredRepos are the repos the user starred something in: the repo, a
// file or a key. Their records keep the repo's folder.
func starredRepos(st *store.Store) []string {
	var out []string
	for key, n := range st.Nodes() {
		r, err := ref.Parse(key)
		if err == nil && r.Kind == provider.KindRepo && len(n.StarredBy) > 0 && n.Source != "" && !slices.Contains(out, n.Source) {
			out = append(out, n.Source)
		}
	}
	slices.Sort(out)
	return out
}

// checkCommandKeys reports a custom command key that a built-in action or
// another command in an overlapping context takes.
func checkCommandKeys(km *keys.Map, cmds []commands.Command) error {
	for i, c := range cmds {
		if c.Key == "" {
			continue
		}
		name := fmt.Sprintf("custom command %q", cmpOr(c.Description, c.Command))
		if err := km.CheckExtra(c.Key, name); err != nil {
			return err
		}
		for _, d := range cmds[:i] {
			overlap := c.Context == d.Context || c.Context == commands.Global || d.Context == commands.Global
			if d.Key == c.Key && overlap {
				return fmt.Errorf("%s: %q is also the key of custom command %q", name, c.Key, cmpOr(d.Description, d.Command))
			}
		}
	}
	return nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// refArg parses get's and open's arguments: a reference, and optionally
// the account to use when several could (lazysecrets list's column) and,
// for open, what to do with it rather than asking (--action).
func refArg(cmd string, args []string) (r ref.Ref, action string, err error) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	account := fs.String("account", "", "the account to use when several could, as lazysecrets list prints it")
	usage := "usage: lazysecrets get [--account <account>] <ref>"
	act := new(string)
	if cmd == "open" {
		act = fs.String("action", "", "do this rather than ask: go (to it), copy (go, and copy its value) or show (go, and show it)")
		usage = "usage: lazysecrets open [--account <account>] [--action go|copy|show] <ref>"
	}
	if err := fs.Parse(args); err != nil {
		return r, "", err
	}
	if fs.NArg() != 1 {
		return r, "", errors.New(usage)
	}
	if _, ok := app.OpenActions[*act]; *act != "" && !ok {
		return r, "", fmt.Errorf("--action: %q is not go, copy or show", *act)
	}
	r, err = ref.Parse(fs.Arg(0))
	r.Account = *account
	return r, *act, err
}

// defaultConfigPath is $LAZYSECRETS_CONFIG_FILE, else config.yaml in the
// config directory.
func defaultConfigPath() string {
	if env := os.Getenv("LAZYSECRETS_CONFIG_FILE"); env != "" {
		return env
	}
	dir, err := config.Dir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(dir, "config.yaml")
}

func run() error {
	configPath := flag.String("config", defaultConfigPath(),
		"config file; several, comma-separated, are layered in order (also LAZYSECRETS_CONFIG_FILE)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	dir := flag.String("dir", "", "start in this directory: its git repo's SOPS files, the ones under it first (default: the current directory)")
	demo := flag.Bool("demo", false, "browse built-in sample data instead of real backends")
	timeout := flag.Duration("timeout", 30*time.Second, "timeout for each backend request, every provider's (overrides the config)")
	icons := flag.Bool("icons", true,
		"show Nerd Font icons (needs a Nerd Font v3+); disable with --icons=false or LAZYSECRETS_ICONS=0")
	clipClear := flag.Duration("clipboard-clear", 30*time.Second, "clear copied values from the clipboard after this long (0 disables)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `Usage:
  lazysecrets [flags]             browse secrets
  lazysecrets [flags] [open [--account <account>] [--action go|copy|show]] <ref>
                                  open a lazysecrets:// link (also a share link),
                                  Key Vault URL, @Microsoft.KeyVault(...) or
                                  {{secrets/scope/key}}
  lazysecrets [flags] get [--account <account>] <ref>
                                  print the value a lazysecrets:// link points at,
                                  for scripts: VAR=$(lazysecrets get <ref>)
  lazysecrets [flags] list [--kind secret,…] [--cached] [--max-age 2m] [--format text|json]
                          [--color always|never] [--icons=false] [--errors=false]
                          [--stars=false] [--notes=false] [--recent] [--expiry=false]
                                  print every source, vault and secret it can
                                  find, one per line, for fzf and pickers:
                                  what's cached at once, then what loads
  lazysecrets [flags] share [--key <secret>] <env file>...
                                  print a link to env files of a repo, encrypted
                                  with a secret's value (default: sharing.key)
  lazysecrets [flags] keybindings print every action and its keys (with your
                                  config applied), as config to copy and edit
  lazysecrets handler install [--terminal NAME]
                                  open lazysecrets:// links from the browser and
                                  chat in a new window of this terminal
  lazysecrets handler uninstall   stop opening them
  lazysecrets launch <ref>        open a link in a new terminal window, as
                                  the link handler does
  lazysecrets update [--check]    install the newest release in place of this
                                  one (--check: just say if there is one)

Flags:
`)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("lazysecrets", versionString())
		return nil
	}

	var open, getRef *ref.Ref
	var openAction string
	var shareArgs, listArgs []string
	printKeys := false
	switch args := flag.Args(); {
	case len(args) > 0 && args[0] == "share":
		shareArgs = args[1:]
		if len(shareArgs) == 0 {
			shareArgs = []string{} // still sharing: says how
		}
	case len(args) > 0 && args[0] == "list":
		listArgs = append([]string{}, args[1:]...) // not nil: listing
	case len(args) == 1 && args[0] == "keybindings":
		printKeys = true
	case len(args) > 0 && args[0] == "get":
		r, _, err := refArg("get", args[1:])
		if err != nil {
			return err
		}
		getRef = &r
	case len(args) > 0 && args[0] == "handler":
		return handler(*configPath, args[1:])
	case len(args) > 0 && args[0] == "launch":
		return launch(*configPath, args[1:])
	case len(args) > 0 && args[0] == "update":
		return updateCmd(*configPath, args[1:])
	case len(args) == 0:
	case args[0] == "open" && len(args) >= 2:
		r, act, err := refArg("open", args[1:])
		if err != nil {
			return err
		}
		open, openAction = &r, act
	case len(args) == 1 && looksLikeRef(args[0]):
		// lazysecrets <link>, like lazysecrets open <link>
		r, err := ref.Parse(args[0])
		if err != nil {
			return err
		}
		open = &r
	default:
		flag.Usage()
		return fmt.Errorf("unexpected arguments: %v", args)
	}

	cfg, err := config.LoadAll(strings.Split(*configPath, ","))
	if err != nil {
		return err
	}
	theme, defaultFg, err := styles.FromTheme(cfg.Theme)
	if err != nil {
		return err
	}
	// Warnings show when the window opens.
	var warnings []string
	if w := styles.Unknown(cfg.Theme); w != "" {
		warnings = append(warnings, w)
	}
	keymap := keys.Default()
	if err := keymap.Apply(cfg.Keybinding); err != nil {
		return err
	}
	if err := checkCommandKeys(&keymap, cfg.CustomCommands); err != nil {
		return err
	}
	if printKeys {
		fmt.Print(keymap.YAML())
		return nil
	}
	// Precedence: flag > environment > config file > default.
	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if *timeout <= 0 {
		return fmt.Errorf("--timeout: %s is not positive", *timeout)
	}
	// Each provider's own timeout, unless --timeout sets them all.
	timeouts := map[provider.Kind]time.Duration{}
	if !set["timeout"] {
		if cfg.Providers.Timeout > 0 {
			*timeout = cfg.Providers.Timeout
		}
		for k, d := range map[provider.Kind]time.Duration{
			provider.KindAzure:      cfg.Providers.Azure.Timeout,
			provider.KindDatabricks: cfg.Providers.Databricks.Timeout,
			provider.KindRepo:       cfg.Providers.Repos.Timeout,
		} {
			if d > 0 {
				timeouts[k] = d
			}
		}
	}
	// timeoutFor is how long a request to a provider of kind k may take.
	timeoutFor := func(k provider.Kind) time.Duration { return cmp.Or(timeouts[k], *timeout) }
	if !set["clipboard-clear"] && cfg.Values.ClipboardClear != nil {
		*clipClear = *cfg.Values.ClipboardClear
	}
	expiryDays := 30
	if cfg.UI.ExpiryWarningDays != nil {
		expiryDays = *cfg.UI.ExpiryWarningDays
	}
	revealTimeout := 60 * time.Second
	if cfg.Values.RevealTimeout != nil {
		revealTimeout = *cfg.Values.RevealTimeout
	}
	loadDelay := 150 * time.Millisecond
	if cfg.UI.LoadDelay != nil {
		loadDelay = *cfg.UI.LoadDelay
	}
	var updateEvery time.Duration // 0: the app's default, a day
	if cfg.Updates.AutoCheckInterval != nil {
		updateEvery = *cfg.Updates.AutoCheckInterval
	}
	if !set["icons"] {
		switch {
		case os.Getenv("LAZYSECRETS_ICONS") != "":
			*icons = os.Getenv("LAZYSECRETS_ICONS") != "0"
		case cfg.UI.Icons != nil:
			*icons = *cfg.UI.Icons
		}
	}

	recentIcon := cfg.UI.RecentIcon != nil && *cfg.UI.RecentIcon

	// Stars, notes and the layout. Demo mode keeps them in memory, so
	// sample vaults never end up in the real file.
	stateDir, err := config.StateDir()
	if err != nil {
		return err
	}
	statePath := ""
	if !*demo {
		statePath = filepath.Join(stateDir, "state.json")
	}
	st, err := store.Load(statePath)
	if errors.Is(err, store.ErrNewer) {
		warnings = append(warnings, err.Error()+": stars, notes and the layout won't be saved; update lazysecrets")
	} else if err != nil {
		return err
	}

	var providers []provider.Provider
	if *demo {
		// Two Azure accounts, like a personal and an admin login in az.
		alice, admin := "Azure:alice@example.com/demo", "Azure:admin.alice@example.com/demo"
		fake.Seed(st, alice, admin)
		az := fake.Azure(400*time.Millisecond).As(alice, "Azure · alice")
		// Due soon whenever the demo runs (the tests' dates are fixed).
		az.Expire("kv-app-prod", "api-key", time.Now().AddDate(0, 0, 9).Truncate(24*time.Hour))
		providers = []provider.Provider{
			az,
			fake.AzureOps(700*time.Millisecond).As(admin, "Azure · admin.alice"),
			fake.Databricks(600 * time.Millisecond),
			fake.Repos(),
		}
	} else {
		pc := cfg.Providers
		// One provider per az account. Created even when disabled: AKV-
		// backed Databricks scopes fall back to reading the vault through
		// the default account.
		accounts, err := azure.NewAccounts(azure.Settings{
			HideSubscriptions: pc.Azure.HideSubscriptions,
			Vaults:            pc.Azure.Vaults,
		})
		if err != nil {
			return fmt.Errorf("azure: %w", err)
		}
		if config.On(pc.Azure.Enabled) {
			for _, a := range accounts {
				providers = append(providers, a)
			}
		}
		if config.On(pc.Databricks.Enabled) {
			dbx, err := databricks.New(accounts[0], pc.Databricks.HideProfiles)
			if err != nil {
				return fmt.Errorf("databricks: %w", err)
			}
			providers = append(providers, dbx)
		}
		if config.On(pc.Repos.Enabled) {
			start := *dir
			if start == "" {
				start, _ = os.Getwd()
			}
			roots := repo.DefaultRoots
			if pc.Repos.Roots != nil {
				roots = *pc.Repos.Roots
			}
			repos := repo.New(repo.Settings{
				Dir: start, Paths: pc.Repos.Paths, Roots: roots, Starred: starredRepos(st),
				Include: pc.Repos.Include, Exclude: pc.Repos.Exclude,
			})
			// The repo it started in (shown on top as Repo) and the
			// others, checked for secret files in the background like
			// the other sources; repos without any aren't listed.
			providers = append(providers, repos)
		}
		if len(providers) == 0 {
			return fmt.Errorf("all providers are disabled in %s", *configPath)
		}
	}

	// Libraries (notably the Databricks SDK) log token refreshes through
	// the standard logger, which would draw over the TUI.
	log.SetOutput(io.Discard)

	if getRef != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeoutFor(getRef.Kind))
		defer cancel()
		return get(ctx, providers, st, *getRef)
	}
	if listArgs != nil {
		cachePath := ""
		if d, err := config.CacheDir(); err == nil && !*demo && config.On(cfg.Storage.Cache) {
			cachePath = filepath.Join(d, "cache.json")
		}
		return listCmd(providers, st, cachePath, timeoutFor, theme, *icons, recentIcon, time.Duration(expiryDays)*24*time.Hour, listArgs, os.Stdout)
	}
	if shareArgs != nil {
		// The files from a repo, the key from whichever provider it's
		// in: the longest timeout.
		d := *timeout
		for _, t := range timeouts {
			d = max(d, t)
		}
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()
		return shareFiles(ctx, providers, st, cfg.Sharing.Key, shareRules(cfg), shareArgs, os.Stdout)
	}

	// Links opened elsewhere (lazysecrets launch) can come here.
	var p *tea.Program
	var srv *instance.Server
	if !*demo {
		srv, err = instance.Listen(instance.Dir(stateDir), func(link, front string) error {
			r, err := ref.Parse(link)
			if err != nil {
				return err
			}
			// Accept at once: launch waits for the answer, and opens a
			// window anyway if it's slow. Coming to the front can be
			// (activating the terminal app).
			go func() {
				p.Send(app.OpenLink(r))
				instance.Raise(front)
			}()
			return nil
		})
		if err == nil {
			defer srv.Close()
		} // else links open in new windows: not worth failing for
	}
	var setup *app.HandlerSetup
	if !*demo {
		setup = handlerSetup(cfg, *configPath)
	}
	onActive := func() {}
	if srv != nil {
		onActive = srv.Touch
	}

	// What was loaded last time, and where it was left in this folder.
	var cacheFile, placeDir string
	if !*demo {
		if d, err := config.CacheDir(); err == nil && config.On(cfg.Storage.Cache) {
			cacheFile = filepath.Join(d, "cache.json")
		}
		placeDir = *dir
		if placeDir == "" {
			placeDir, _ = os.Getwd()
		}
		if abs, err := filepath.Abs(placeDir); err == nil {
			placeDir = abs
		}
	}

	m := app.New(providers, app.Options{
		Timeout:        *timeout,
		Timeouts:       timeouts,
		Icons:          *icons,
		ClipboardClear: *clipClear,
		FlatSources:    cfg.UI.SourcesView == "flat",
		CollapseGroups: config.On(cfg.UI.CollapseGroups),
		LoadDelay:      loadDelay,
		NoHistory:      !config.On(cfg.Storage.History),
		OnActive:       onActive,
		RevealTimeout:  revealTimeout,
		ExpiryWarning:  time.Duration(expiryDays) * 24 * time.Hour,
		HideOnBlur:     config.On(cfg.Values.HideOnBlur),
		HandlerSetup:   setup,
		Updater:        updater(cfg.Updates.Sources),
		UpdateCheck:    config.On(cfg.Updates.AutoCheck),
		UpdateEvery:    updateEvery,
		Keys:           &keymap,
		Commands:       cfg.CustomCommands,
		Version:        versionString(),
		Store:          st,
		Warning:        strings.Join(warnings, " · "),
		CacheFile:      cacheFile,
		ConfigFiles:    configFiles(*configPath),
		StatePath:      statePath,
		Changelog:      lazysecrets.Changelog,
		Dir:            placeDir,
		ShareKey:       cfg.Sharing.Key,
		ShareRules:     shareRules(cfg),
		Open:           open,
		OpenAction:     openAction,
		RecentIcon:     recentIcon,
		Styles:         &theme,
		DefaultFg:      defaultFg,
	})
	blockingStdin()
	p = tea.NewProgram(m)
	final, err := p.Run()
	if fm, ok := final.(app.Model); ok {
		_ = fm.Persist() // a cache it couldn't save is no reason to fail
	}
	return err
}

// configFiles are the config files given, for the status pane: those
// that exist, or the first if none does (where to create one).
func configFiles(list string) []string {
	var all, found []string
	for _, f := range strings.Split(list, ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		all = append(all, f)
		if _, err := os.Stat(f); err == nil {
			found = append(found, f)
		}
	}
	if len(found) == 0 && len(all) > 0 {
		return all[:1]
	}
	return found
}
