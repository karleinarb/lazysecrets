// Package app is the root Bubble Tea model: layout, focus, key routing and
// asynchronous loading from providers.
package app

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"math"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"git.kebcloud.no/keb/lazysecrets/internal/browser"
	"git.kebcloud.no/keb/lazysecrets/internal/clipboard"
	"git.kebcloud.no/keb/lazysecrets/internal/commands"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
	"git.kebcloud.no/keb/lazysecrets/internal/share"
	"git.kebcloud.no/keb/lazysecrets/internal/store"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/icons"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/keys"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/panes"
	"git.kebcloud.no/keb/lazysecrets/internal/ui/styles"
	"git.kebcloud.no/keb/lazysecrets/internal/update"
)

type pane int

const (
	paneSources pane = iota
	paneContainers
	paneSecrets
	paneDetail
	// paneStatus is on top of the left panes; after the right pane in
	// the tab order (status.go).
	paneStatus
	paneCount
)

// slot identifies a kind of request; starting one cancels the previous
// request in the same slot.
type slot int

const (
	slotContainers slot = iota
	slotObjects
	slotValue    // reveal in the detail pane
	slotDescribe // extra metadata for the selected object
	slotCopy     // fetch a value straight to the clipboard
	slotVersions // count the selected object's versions
	slotDetails  // a file's details
	slotCount
)

// Options configures the app.
type Options struct {
	// Timeout bounds every provider call. Defaults to 30s.
	Timeout time.Duration
	// Icons shows Nerd Font glyphs next to backends.
	Icons bool
	// Clipboard defaults to the system clipboard, or OSC52 over SSH.
	Clipboard Clipboard
	// ClipboardClear clears copied values after this long; 0 disables.
	ClipboardClear time.Duration
	// OpenURL opens a web page; defaults to the system browser.
	OpenURL func(url string) error
	// ConfigFiles and StatePath are where lazysecrets reads its config
	// and keeps its state, for the status pane.
	ConfigFiles []string
	StatePath   string
	// Changelog is CHANGELOG.md, for the status pane.
	Changelog string
	// CacheFile keeps what was loaded between runs; "" keeps it for this
	// run only.
	CacheFile string
	// ShareKey is the config's share.key: the secret that encrypts shared
	// env files, for repos whose .lazysecrets.yaml names none.
	ShareKey string
	// ShareRules are what a key's value must be to share with it.
	ShareRules share.Rules
	// Dir is the folder lazysecrets runs in: it goes back to where it was
	// left there, unless it was started on a link. "" doesn't.
	Dir string
	// FlatSources lists sources without provider groups.
	FlatSources bool
	// CollapseGroups starts with the Sources pane's groups folded, when
	// there are several.
	CollapseGroups bool
	// Store keeps stars, notes and the layout; defaults to an unsaved one.
	Store *store.Store
	// LoadDelay is how long the cursor must rest on a row before its
	// contents load (settled); 0 loads at once.
	LoadDelay time.Duration
	// ExpiryWarning marks items expiring within this long; 0 marks only
	// expired ones.
	ExpiryWarning time.Duration
	// RevealTimeout hides a revealed value after this long; 0 never.
	RevealTimeout time.Duration
	// HideOnBlur hides a revealed value when the terminal loses focus.
	HideOnBlur bool
	// OnActive is called on key presses and when the terminal gains
	// focus, so the link handler can pick the instance last used.
	OnActive func()
	// Version is shown at the bottom right ("0.1.0"); empty shows none.
	Version string
	// Commands are the custom commands from the config (commands.go).
	Commands []commands.Command
	// Keys, if set, replaces the default keymap (keys.Map.Apply).
	Keys *keys.Map
	// Updater, if set, finds and installs newer releases (U); with
	// UpdateCheck, lazysecrets also looks once a day on its own.
	Updater     *Updater
	UpdateCheck bool
	// HandlerSetup, if set, offers to set up the link handler at startup.
	HandlerSetup *HandlerSetup
	// NoHistory stops counting uses of items for the Recent list.
	NoHistory bool
	// Open is a reference to open at startup (lazysecrets open <ref>).
	Open *ref.Ref
	// Styles, if set, replaces the default theme (styles.FromTheme), and
	// DefaultFg the terminal's default text colour.
	Styles    *styles.Styles
	DefaultFg color.Color
}

// Clipboard is implemented by clipboard.Clipboard.
type Clipboard interface {
	Write(s string) tea.Cmd
	Clear(fingerprint [32]byte) tea.Cmd
	// Read returns the clipboard's text, or an error where it can't be
	// read (over SSH).
	Read() (string, error)
}

type Model struct {
	providers []provider.Provider
	byID      map[string]provider.Provider
	timeout   time.Duration

	keys keys.Map
	st   styles.Styles
	help help.Model
	spin spinner.Model

	sources    panes.List[sourceRow]
	containers panes.List[provider.Container]
	secrets    panes.List[provider.Object]
	detail     panes.Detail

	focus       pane
	termBlurred bool // the terminal (or tmux pane) doesn't have focus
	w, h        int
	split       int    // columns the user moved the divider from its default
	heights     [3]int // rows added to each left pane's default height
	zoom        zoom
	spinning    bool
	status      string // sticky errors from loading sources
	flash       flash  // transient message, cleared on the next key press

	// auth is set when a backend needs the user to sign in again.
	auth *provider.AuthError

	defaultFg color.Color // from the theme; nil keeps the terminal's
	store     *store.Store
	icons     icons.Set

	cache cache

	clip      Clipboard
	clipClear time.Duration
	openURL   func(string) error
	// fields is the fourth pane for anything but a secret; lastLeft is
	// the left pane it describes while the fourth has focus (fields.go).
	fields   panes.Fields
	lastLeft pane
	details  map[string]detailsState // by containerKey
	// errLog is this session's errors, newest first; loggedFlash the
	// footer's text when last looked at (errlog.go).
	errLog      []loggedErr
	loggedFlash string
	getenv      func(string) string
	nvimOpen    func(server, path string, line int) error // edit.go
	now         func() time.Time
	// The cache file, whether the cache changed since it was saved, and
	// when each provider's Sources list was loaded (persist.go).
	cachePath string
	// configFiles and statePath are for the status pane (status.go).
	configFiles []string
	statePath   string
	statusW     int // the status pane's width: the left column's, zoomed or not
	changelog   []panes.Field
	cacheDirty  bool
	sourcesAt   map[string]time.Time
	placeDir    string
	savedPlace  string // the link last saved as the place

	// A pending clear of a copied value, counted down in the footer.
	// clipToken identifies the latest copy so older ticks are ignored.
	clipToken    int
	clipFP       [32]byte // fingerprint of the copied value
	clipLabel    string
	clipDeadline time.Time // zero when nothing is pending

	collapsed map[string]bool // folded groups in the Sources pane, by provider ID
	// current is the source lazysecrets started in, as soon as its
	// provider finds it (provider.Current), before Sources lists them all.
	current     *provider.Source
	flatSources bool // Sources pane without provider groups
	sourcesFavs bool // Sources pane on its Favourites tab
	history     bool // count uses of items (usage.go)
	onActive    func()

	// A revealed value hides at revealDeadline; revealToken drops ticks
	// of an earlier reveal.
	revealTimeout  time.Duration
	expiryWarning  time.Duration
	hideOnBlur     bool
	revealDeadline time.Time
	revealToken    int

	commands []commands.Command // custom commands (commands.go)
	version  string             // shown at the bottom right
	crawl    crawl              // loading every vault for search (search.go)
	search   *searchView        // the search popup, when open (searchview.go)

	handlerSetup *HandlerSetup

	updater      *Updater
	updateCheck  bool
	fetchOnStart bool            // look for a newer release in Init
	newer        *update.Release // a newer release to offer
	updated      string          // the release installed, waiting for a restart
	askHandler   bool            // the link handler popup is still to show

	// Loads the selection starts after a key press wait settle first, and
	// are dropped if the cursor moves on meanwhile, so holding j doesn't
	// send a request per row. waiting is settle while handling a key.
	settle, waiting time.Duration
	sourcesFiltered bool // the Sources rows were built for a filter
	sourcesTouched  bool // the user has moved in the Sources pane

	// Sources stream in per provider; gen discards results from a
	// superseded load.
	sourcesGen     int
	sourcesPending int
	sourceResults  map[string]sourcesMsg // by provider ID

	// At most one request in flight per slot. Responses whose id doesn't
	// match are stale and dropped.
	seq    int
	reqID  [slotCount]int
	cancel [slotCount]context.CancelFunc

	// Last selection seen, to detect changes after each update.
	selSource, selContainer, selSecret string

	// objType is the active tab of the Secrets pane. wantType is the tab the
	// user last picked, restored when moving to a container that has it.
	objType, wantType provider.ObjectType

	// versionsOf is set while the Secrets pane lists an object's versions.
	versionsOf *provider.Object

	nav        *nav                 // opening a reference, in progress
	pendingCmd tea.Cmd              // a command started by advanceNav
	prompt     *modal               // popup that has the keyboard
	linked     []provider.Container // vaults opened from links, this session
	open       *ref.Ref             // reference to open at startup

	shareKey   string      // the config's share.key
	shareRules share.Rules // and share.keyRules

	// A lazysecrets:// link found on the clipboard, offered in the footer.
	dismissedLink string // the last link offered: not offered again
	clipChecked   time.Time
}

// currentMsg is the source lazysecrets started in (provider.Current).
type currentMsg struct {
	gen    int
	source provider.Source
}

type sourcesMsg struct {
	gen      int
	provider string // provider ID
	sources  []provider.Source
	err      error
}

type containersMsg struct {
	req   int
	key   string // cache key
	items []provider.Container
	err   error
}

type secretsMsg struct {
	req int
	key string // cache key
	// bound is set when the container's account was found by trying each
	// candidate; it is the container as served by the one that worked.
	bound *provider.Container
	items []provider.Object
	err   error
}

type valueMsg struct {
	req   int
	obj   provider.Object
	value provider.SecretValue
	err   error
}

type describeMsg struct {
	req int
	key string // cache key
	obj provider.Object
	err error
}

type copyMsg struct {
	req   int
	obj   provider.Object
	name  string
	value provider.SecretValue
	err   error
}

type clipTickMsg struct{ token int }

// revealTickMsg counts down a revealed value's display (reveal.go).
type revealTickMsg struct{ token int }

type versionCountMsg struct {
	req   int
	key   string // cache key, shared with the versions view
	id    string // object ID
	items []provider.Object
	err   error
}

type loginDoneMsg struct{ err error }

type flash struct {
	text  string
	isErr bool // a failure: "✗ copy failed: …"
	ok    bool // a confirmation: "✓ copied api-key"
	warn  bool // can't be done here: "⚠ select a vault to share"
}

// done is a confirmation flash, "✓ copied api-key".
func done(text string) flash { return flash{text: text, ok: true} }

// warning says what can't be done here, and why.
func warning(text string) flash { return flash{text: text, warn: true} }

func New(providers []provider.Provider, opts Options) Model {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	iconSet := icons.None
	if opts.Icons {
		iconSet = icons.NerdFont
	}
	m := Model{
		providers:     providers,
		byID:          map[string]provider.Provider{},
		timeout:       opts.Timeout,
		keys:          keys.Default(),
		st:            styles.Default(),
		spin:          spinner.New(spinner.WithSpinner(spinner.Dot)),
		detail:        panes.NewDetail(iconSet),
		icons:         iconSet,
		sourceResults: map[string]sourcesMsg{},
		clip:          opts.Clipboard,
		clipClear:     opts.ClipboardClear,
		now:           time.Now,
		getenv:        os.Getenv,
		nvimOpen:      nvimOpen,
		cache:         newCache(),
		details:       map[string]detailsState{},
		fields:        panes.NewFields(),
	}
	if m.clip == nil {
		m.clip = clipboard.New()
	}
	m.openURL = opts.OpenURL
	if m.openURL == nil {
		m.openURL = browser.Open
	}
	for _, p := range providers {
		m.byID[p.ID()] = p
	}

	// Before the lists: their render functions capture the store.
	m.store = opts.Store
	if m.store == nil {
		m.store, _ = store.Load("")
	}
	m.sources = newSourcesList(iconSet, m.store)
	m.sources.Empty = "no sources found"
	m.collapsed = map[string]bool{}
	m.flatSources = opts.FlatSources
	if opts.CollapseGroups && len(providers) > 1 {
		for _, p := range providers {
			m.collapsed[p.ID()] = true
		}
	}
	m.history = !opts.NoHistory
	m.onActive = opts.OnActive
	m.commands = opts.Commands
	m.version = opts.Version
	if opts.Keys != nil {
		m.keys = *opts.Keys
	}
	m.detail.RevealKey, m.detail.VersionsKey, m.detail.CopyKey = keys.Name(m.keys.Reveal), keys.Name(m.keys.Versions), keys.Name(m.keys.Copy)
	m.revealTimeout, m.hideOnBlur = opts.RevealTimeout, opts.HideOnBlur
	m.expiryWarning = opts.ExpiryWarning
	m.handlerSetup = opts.HandlerSetup
	m.updater, m.updateCheck = opts.Updater, opts.UpdateCheck
	m.startUpdateCheck()
	m.askHandler = opts.HandlerSetup != nil && !m.store.Flag(flagNoHandlerPrompt)
	m.settle = opts.LoadDelay
	if opts.Styles != nil {
		m.st = *opts.Styles
	}
	m.defaultFg = opts.DefaultFg
	m.help = newHelp(m.st)
	m.open = opts.Open
	saved := m.store.Layout()
	m.split, m.heights = saved.Split, saved.Heights

	m.containers = panes.NewList("[2] Vaults", panes.Item[provider.Container]{
		ID:          containerKey,
		Filter:      func(c provider.Container) string { return c.Name },
		FilterExtra: func(c provider.Container) string { return m.store.Node(storeKey(c)).Note },
		Render:      m.containerRender(false),
	})

	m.secrets = panes.NewList("[3] Secrets", panes.Item[provider.Object]{
		// Versions of one object share its ID.
		ID:     func(s provider.Object) string { return s.ID + "@" + s.Version },
		Filter: func(s provider.Object) string { return s.Name },
		// Typing "expir" finds what has expired or soon will.
		FilterExtra: func(o provider.Object) string { return m.store.Node(objectKey(o)).Note + " " + m.expiryWord(o) },
		Render:      m.objectRender(),
	})
	m.secrets.Empty = "no secrets"
	m.objType, m.wantType = provider.TypeSecret, provider.TypeSecret

	m.sources.SetLoading()
	m.sourcesGen = 1
	m.sourcesPending = len(providers)
	m.spinning = true

	m.cachePath, m.placeDir, m.sourcesAt = opts.CacheFile, opts.Dir, map[string]time.Time{}
	m.shareKey, m.shareRules = opts.ShareKey, opts.ShareRules
	m.configFiles, m.statePath, m.changelog = opts.ConfigFiles, opts.StatePath, changelogFields(opts.Changelog)
	if m.cachePath != "" {
		m.loadCacheFile()
		if len(m.sourceResults) > 0 {
			m.rebuildSources()
			m.sources.Loading = true // reloading what the file had
			m.sources.Age = m.cachedAge(m.oldestSources())
		}
	}
	if m.placeDir != "" && opts.Open == nil {
		m.restorePlace()
	}
	return m
}

// renderVersion draws a version row: short ID, date and state. current is
// the newest version's ID.
func renderVersion(now time.Time, current string) func(styles.Styles, provider.Object, int) string {
	return func(st styles.Styles, s provider.Object, _ int) string {
		return panes.VersionLine(st, s, now, current)
	}
}

// setVersions shows a version list, marking the newest as current.
func (m *Model) setVersions(items []provider.Object) {
	current := ""
	if len(items) > 0 {
		current = items[0].Version
	}
	m.secrets.SetRender(renderVersion(m.now(), current))
	m.secrets.SetItems(items)
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.sourcesCmd(), m.spin.Tick, m.checkClipboard(), tea.RequestBackgroundColor}
	if m.cachePath != "" || m.placeDir != "" {
		cmds = append(cmds, tea.Tick(persistEvery, func(time.Time) tea.Msg { return persistTickMsg{} }))
	}
	if m.fetchOnStart {
		cmds = append(cmds, m.fetchLatest(false))
	}
	if m.open != nil {
		r := *m.open
		cmds = append(cmds, func() tea.Msg { return openRefMsg{r} })
	}
	return tea.Batch(cmds...)
}

// openRefMsg starts opening a reference from outside the key handler.
type openRefMsg struct{ ref ref.Ref }

// OpenLink is a message offering r in the Open link popup, for links
// handed over by the link handler (Program.Send).
func OpenLink(r ref.Ref) tea.Msg { return openRefMsg{r} }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.st = m.st.OnBackground(msg.Color, msg.IsDark())
		return m, nil
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height

	case spinner.TickMsg:
		if !m.anyLoading() {
			m.spinning = false
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		m.touched()
		m.flash = flash{}
		m.nav = nil // the user took over
		cmds = append(cmds, m.maybeCheckClipboard())
		cmd, quit := m.handleKey(msg)
		if quit {
			return m, tea.Quit
		}
		cmds = append(cmds, cmd)
		if m.nav == nil { // jumps and links go straight there
			m.waiting = m.settle
		}

	case sourcesMsg:
		cmds = append(cmds, m.handleSources(msg))

	case updateMsg:
		cmds = append(cmds, m.handleUpdate(msg))

	case installedMsg:
		m.handleInstalled(msg)

	case currentMsg:
		if msg.gen == m.sourcesGen {
			m.current = &msg.source
			m.rebuildSources()
			m.queueSource(msg.source, false)
			cmds = append(cmds, m.crawlStart())
		}

	case handlerInstalledMsg:
		m.handlerInstalled(msg)

	case openRefMsg:
		m.deferHandlerSetup() // the link first
		cmds = append(cmds, m.offerRef("Open link", msg.ref, false))

	case resolvedMsg:
		m.handleResolved(msg)

	case shareFilesMsg:
		cmds = append(cmds, m.handleShareFiles(msg))

	case sealedMsg:
		cmds = append(cmds, m.handleSealed(msg))

	case shareOpenedMsg:
		cmds = append(cmds, m.handleShareOpened(msg))

	case tea.PasteMsg:
		cmds = append(cmds, m.handlePaste(msg))

	case tea.BlurMsg:
		m.termBlurred = true
		if m.hideOnBlur && m.detail.State() == panes.ValueShown {
			m.hideValue()
		}

	case commandValueMsg:
		cmds = append(cmds, m.handleCommandValue(msg))

	case commandDoneMsg:
		m.handleCommandDone(msg)

	case editDoneMsg:
		cmds = append(cmds, m.handleEditDone(msg))

	case persistTickMsg:
		cmds = append(cmds, m.persistTick())

	case persistedMsg:
		m.handlePersisted(msg)

	case detailsMsg:
		if msg.req == m.reqID[slotDetails] {
			m.done(slotDetails)
		}
		if errors.Is(msg.err, context.Canceled) || errors.Is(msg.err, context.DeadlineExceeded) {
			delete(m.details, msg.key) // tried again when shown again
		} else {
			m.details[msg.key] = detailsState{attrs: msg.attrs, err: msg.err}
		}

	case crawlMsg:
		cmds = append(cmds, m.handleCrawl(msg))

	case revealTickMsg:
		cmds = append(cmds, m.revealTick(msg))

	case tea.FocusMsg:
		m.touched()
		m.termBlurred = false
		// Back from another app, maybe with a link copied there.
		m.clipChecked = m.now()
		cmds = append(cmds, m.checkClipboard())

	case openPromptFillMsg:
		m.fillOpenPrompt(string(msg))

	case clipboardTextMsg:
		m.offerClipboardLink(string(msg))

	case containersMsg:
		if msg.req != m.reqID[slotContainers] {
			break
		}
		m.done(slotContainers)
		if msg.err != nil {
			m.noteErr(msg.err)
			if src, ok := m.selectedSource(); ok {
				m.logErrOf(src.Name, msg.err)
			}
			delete(m.cache.containers, msg.key)
			m.containers.SetError(msg.err)
		} else {
			m.cache.containers[msg.key] = entry[[]provider.Container]{msg.items, m.now()}
			m.cacheDirty = true
			m.containers.SetItems(m.starredContainersFirst(msg.items))
		}

	case secretsMsg:
		if msg.req != m.reqID[slotObjects] {
			break
		}
		m.done(slotObjects)
		if errors.Is(msg.err, provider.ErrUnsupported) && m.versionsOf != nil {
			m.flash = warning(fmt.Sprintf("versions aren't supported for %s", m.versionsOf.Container.Source.Kind))
			m.closeVersions()
		} else if msg.err != nil {
			m.noteErr(msg.err)
			if c, ok := m.containers.Selected(); ok {
				m.logErrOf(c.Name, msg.err)
			}
			delete(m.cache.objects, msg.key)
			m.secrets.SetError(msg.err)
		} else {
			if msg.bound != nil {
				msg.key = m.rebind(*msg.bound)
			}
			before := m.cache.objects[msg.key].value
			m.cache.objects[msg.key] = entry[[]provider.Object]{msg.items, m.now()}
			m.cacheDirty = true
			if m.versionsOf != nil {
				m.setVersions(msg.items)
			} else {
				m.secrets.SetItems(m.starredFirst(msg.items))
				cmds = append(cmds, m.refreshShown(before))
			}
		}

	case valueMsg:
		if msg.req != m.reqID[slotValue] {
			break
		}
		m.done(slotValue)
		if msg.err != nil {
			m.noteErr(msg.err)
			m.logErrOf(msg.obj.Container.Name+"/"+msg.obj.Name, msg.err)
			m.detail.SetValueError(msg.err)
		} else {
			m.detail.SetValue(msg.value)
			m.usedObject(msg.obj)
			cmds = append(cmds, m.startRevealTimer())
		}

	case versionCountMsg:
		if msg.req != m.reqID[slotVersions] {
			break
		}
		m.done(slotVersions)
		// Errors and unsupported providers just leave the count out.
		if msg.err == nil {
			m.cache.objects[msg.key] = entry[[]provider.Object]{msg.items, m.now()}
			m.cacheDirty = true
			m.detail.SetVersions(msg.id, msg.items)
		}

	case describeMsg:
		if msg.req != m.reqID[slotDescribe] {
			break
		}
		m.done(slotDescribe)
		if msg.err != nil {
			m.noteErr(msg.err)
			m.logErrOf(msg.obj.Container.Name+"/"+msg.obj.Name, msg.err)
			m.detail.SetDescribeError(msg.err)
		} else {
			m.cache.details[msg.key] = entry[provider.Object]{msg.obj, m.now()}
			m.cacheDirty = true
			m.detail.SetDescribed(msg.obj)
		}

	case copyMsg:
		if msg.req != m.reqID[slotCopy] {
			break
		}
		m.done(slotCopy)
		if msg.err != nil {
			m.noteErr(msg.err)
			m.flash = flash{text: "copy failed: " + msg.err.Error(), isErr: true}
		} else {
			cmds = append(cmds, m.copyText(msg.name, msg.value.Reveal(), true))
			if msg.obj.Name != "" {
				m.usedObject(msg.obj)
			}
		}

	case clipTickMsg:
		if msg.token != m.clipToken || m.clipDeadline.IsZero() {
			break
		}
		if m.now().Before(m.clipDeadline) {
			cmds = append(cmds, m.clipTick())
			break
		}
		cmds = append(cmds, m.clip.Clear(m.clipFP))
		m.cancelClipClear()
		m.flash = done("clipboard cleared")

	case clipboard.ErrorMsg:
		m.flash = flash{text: "clipboard: " + msg.Err.Error(), isErr: true}

	case loginDoneMsg:
		if msg.err != nil {
			m.flash = flash{text: "sign-in failed: " + msg.err.Error(), isErr: true}
			break
		}
		m.auth = nil
		m.cache = newCache()
		m.flash = done("signed in; reloading")
		m.sources.Refresh()
		cmds = append(cmds, m.loadSources())
		m.selSource = "\x00" // force the selection cascade to reload everything
	}

	cmds = append(cmds, m.syncSelection())
	// Opening a link steps through panes as lists load; lists served from
	// the cache load at once, so keep going while it makes progress.
	for range 8 {
		if !m.advanceNav() {
			break
		}
		cmds = append(cmds, m.pendingCmd, m.syncSelection())
		m.pendingCmd = nil
	}
	if m.sources.HasFilter() != m.sourcesFiltered {
		m.rebuildSources() // switch between grouped and flat-while-filtering rows
		cmds = append(cmds, m.syncSelection())
	}
	m.waiting = 0
	m.askHandlerSetup()
	m.keys.Login.SetEnabled(m.loginFor() != nil)
	if m.focus != paneDetail {
		m.lastLeft = m.focus
	}
	m.logFlash()
	cmds = append(cmds, m.syncFields())
	if m.anyLoading() && !m.spinning {
		m.spinning = true
		cmds = append(cmds, m.spin.Tick)
	}
	m.layout()
	return m, tea.Batch(cmds...)
}

// handleKey returns quit=true when the program should exit.
func (m *Model) handleKey(msg tea.KeyPressMsg) (cmd tea.Cmd, quit bool) {
	if msg.String() == "ctrl+c" {
		return nil, true
	}
	if m.prompt != nil {
		return m.handleModal(msg), false
	}
	if m.search != nil {
		return m.handleSearch(msg), false
	}
	if l := m.focusedFilter(); l != nil && l.Filtering() {
		switch {
		case key.Matches(msg, m.keys.AcceptFilter):
			l.AcceptFilter()
		case key.Matches(msg, m.keys.ClearFilter):
			l.ClearFilter()
		case key.Matches(msg, m.keys.FilterUp):
			m.move(-1)
		case key.Matches(msg, m.keys.FilterDown):
			m.move(1)
		case key.Matches(msg, m.keys.FilterCopy):
			return m.copyValue(), false
		case key.Matches(msg, m.keys.FilterReveal):
			return m.toggleReveal(), false
		case key.Matches(msg, m.keys.FilterStar):
			return m.toggleFavourite(), false
		default:
			return l.UpdateFilter(msg), false
		}
		return nil, false
	}

	if c, ok := m.commandForKey(msg); ok {
		return m.startCommand(c), false
	}
	k := m.keys
	switch {
	case key.Matches(msg, k.Commands):
		return m.showCommands(), false
	case key.Matches(msg, k.Quit):
		return nil, true
	case key.Matches(msg, k.Help):
		return m.showKeybindings(), false
	case key.Matches(msg, k.Up):
		m.move(-1)
	case key.Matches(msg, k.Down):
		m.move(1)
	case key.Matches(msg, k.FilterCopy):
		return m.copyValue(), false
	case key.Matches(msg, k.FilterReveal):
		return m.toggleReveal(), false
	case key.Matches(msg, k.Top):
		m.jump(true)
	case key.Matches(msg, k.Bottom):
		m.jump(false)
	case key.Matches(msg, k.NextPane):
		// The four main panes, round; the status pane (0) leads into them.
		m.focus = (m.focus + 1) % paneStatus
		if r, ok := m.sources.Selected(); ok && m.focus == paneContainers && r.kind == rowGroup {
			m.focus = paneDetail // an account's row: nothing between
		}
	case key.Matches(msg, k.Details):
		m.focus = paneDetail // what's selected, on the right
	case key.Matches(msg, k.PrevPane):
		if m.focus == paneDetail {
			m.focus = m.lastLeft // back to what it describes
		} else {
			m.focus = (m.focus + paneStatus - 1) % paneStatus
		}
	case key.Matches(msg, k.Pane1):
		m.focus = paneSources
	case key.Matches(msg, k.Pane2):
		m.focus = paneContainers
	case key.Matches(msg, k.Pane3):
		m.focus = paneSecrets
	case key.Matches(msg, k.Pane4):
		m.focus = paneDetail
	case key.Matches(msg, k.Pane0):
		m.focus = paneStatus
	case m.focus == paneStatus && key.Matches(msg, k.Enter):
		m.focus = paneDetail // its rows are on the right
	case m.focus == paneSources && (key.Matches(msg, k.Enter) || key.Matches(msg, k.Reveal)) && m.toggleGroup():
		// folded or unfolded a provider group
	case m.focus == paneDetail && key.Matches(msg, k.Enter) && m.fieldLink() != "":
		r, err := ref.Parse(m.fieldLink())
		if err == nil {
			f, _ := m.fields.Selected()
			m.startNav(r, actGo, f.Row)
		}
	case key.Matches(msg, k.Enter):
		if m.focus < paneDetail {
			m.focus++
		}
	case key.Matches(msg, k.Share):
		return m.share(false), false
	case key.Matches(msg, k.ShareMenu):
		return m.share(true), false
	case key.Matches(msg, k.OpenRef):
		return m.startOpenPrompt(), false
	case key.Matches(msg, k.Note):
		return m.editNote(), false
	case key.Matches(msg, k.Favourites):
		return m.showFavourites(), false
	case key.Matches(msg, k.Search):
		return m.showSearch(), false
	case key.Matches(msg, k.Recent):
		return m.showRecent(), false
	case key.Matches(msg, k.Favourite):
		return m.toggleFavourite(), false
	case key.Matches(msg, k.SplitRight):
		m.moveSplit(splitStep)
		m.saveLayout()
	case key.Matches(msg, k.SplitLeft):
		m.moveSplit(-splitStep)
		m.saveLayout()
	case key.Matches(msg, k.SplitReset):
		m.split, m.heights, m.zoom = 0, [3]int{}, zoomNormal
		m.saveLayout()
	case key.Matches(msg, k.Taller), key.Matches(msg, k.Shorter):
		delta := heightStep
		if key.Matches(msg, k.Shorter) {
			delta = -heightStep
		}
		if m.resizeFocused(delta) {
			m.saveLayout()
		} else {
			m.flash = warning("focus a left pane to change its height")
		}
	case key.Matches(msg, k.ZoomIn):
		m.cycleZoom(1)
	case key.Matches(msg, k.ZoomOut):
		m.cycleZoom(-1)
	case key.Matches(msg, k.SourcesView):
		m.toggleSourcesView()
	case key.Matches(msg, k.Versions):
		if m.versionsOf != nil {
			m.closeVersions()
		} else if o, ok := m.detail.Secret(); ok {
			m.versionsOf = &o
			m.focus = paneSecrets
		}
	case key.Matches(msg, k.Back):
		if f := m.focusedFilter(); f != nil && f.HasFilter() {
			f.ClearFilter()
		} else if m.versionsOf != nil && (m.focus == paneSecrets || m.focus == paneDetail) {
			m.closeVersions()
		} else if m.focus == paneStatus {
			m.focus = paneSources
		} else if m.focus > paneSources {
			m.focus--
		}
	case key.Matches(msg, k.Filter):
		if l := m.focusedFilter(); l != nil {
			return l.StartFilter(), false
		}
	case key.Matches(msg, k.NextTab):
		m.switchTab(1)
	case key.Matches(msg, k.PrevTab):
		m.switchTab(-1)
	case key.Matches(msg, k.Reveal):
		return m.toggleReveal(), false
	case key.Matches(msg, k.Copy):
		return m.copyValue(), false
	case key.Matches(msg, k.CopyRef):
		if o, ok := m.detail.Secret(); ok && o.Reference != "" {
			return m.copyText(o.Name+" reference", o.Reference, false), false
		}
	case key.Matches(msg, k.Open):
		return m.openSelected(), false
	case key.Matches(msg, k.Edit):
		return m.edit(), false
	case key.Matches(msg, k.Login):
		if auth := m.loginFor(); auth != nil {
			c := exec.Command(auth.Login[0], auth.Login[1:]...)
			return tea.ExecProcess(c, func(err error) tea.Msg { return loginDoneMsg{err: err} }), false
		}
	case key.Matches(msg, k.Refresh):
		return m.refresh(), false
	case key.Matches(msg, k.Update):
		return m.update(), false
	}
	return nil, false
}

// filterable is the part of panes.List the app needs regardless of item type.
type filterable interface {
	Filtering() bool
	HasFilter() bool
	StartFilter() tea.Cmd
	AcceptFilter()
	ClearFilter()
	UpdateFilter(tea.Msg) tea.Cmd
}

func (m *Model) focusedFilter() filterable {
	switch m.focus {
	case paneSources:
		return &m.sources
	case paneContainers:
		return &m.containers
	case paneSecrets:
		return &m.secrets
	case paneDetail:
		if m.fields.Shown() {
			return &m.fields
		}
	}
	return nil
}

func (m *Model) move(delta int) {
	switch m.focus {
	case paneSources:
		m.sourcesTouched = true
		m.sources.Move(delta)
	case paneContainers:
		m.containers.Move(delta)
	case paneSecrets:
		m.secrets.Move(delta)
	case paneDetail:
		if m.fields.Shown() {
			m.fields.Move(delta)
		} else {
			m.detail.Scroll(delta)
		}
	}
}

func (m *Model) jump(top bool) {
	switch {
	case m.focus == paneSources && top:
		m.sourcesTouched = true
		m.sources.Top()
	case m.focus == paneSources:
		m.sourcesTouched = true
		m.sources.Bottom()
	case m.focus == paneContainers && top:
		m.containers.Top()
	case m.focus == paneContainers:
		m.containers.Bottom()
	case m.focus == paneSecrets && top:
		m.secrets.Top()
	case m.focus == paneSecrets:
		m.secrets.Bottom()
	case m.focus == paneDetail && m.fields.Shown() && top:
		m.fields.Top()
	case m.focus == paneDetail && m.fields.Shown():
		m.fields.Bottom()
	case m.focus == paneDetail && top:
		m.detail.ScrollTop()
	case m.focus == paneDetail:
		m.detail.ScrollBottom()
	}
}

func (m *Model) toggleReveal() tea.Cmd {
	s, ok := m.detail.Secret()
	if !ok {
		return nil
	}
	switch m.detail.State() {
	case panes.ValueShown:
		m.hideValue()
		return nil
	case panes.ValueLoading:
		m.cancelReq(slotValue)
		m.detail.Hide()
		return nil
	}
	return m.fetchValue(s)
}

func (m *Model) refresh() tea.Cmd {
	switch m.focus {
	case paneSources:
		m.sources.Refresh()
		return m.loadSources()
	case paneContainers:
		if src, ok := m.selectedSource(); ok {
			m.containers.Refresh()
			m.details = map[string]detailsState{}
			return m.loadContainers(src, m.selSource)
		}
	case paneSecrets:
		if c, ok := m.containers.Selected(); ok {
			m.secrets.Refresh()
			return m.loadObjects(c, m.objType, m.selContainer)
		}
	case paneDetail:
		if s, ok := m.detail.Secret(); ok && m.detail.State() != panes.ValueHidden {
			return m.fetchValue(s)
		}
	}
	return nil
}

// syncSelection cascades selection changes down the hierarchy: a new source
// loads its containers, a new container loads its secrets, a new secret
// resets the detail pane. It runs after every update.
func (m *Model) syncSelection() tea.Cmd {
	var cmds []tea.Cmd

	if id := m.sources.SelectedID(); id != m.selSource {
		m.cache.rememberCursor(m.selSource, m.containers.SelectedID())
		m.selSource = id
		m.cancelReq(slotContainers)
		m.containers.Reset()
		// Named after the level for the selected row's provider, also on
		// a group header.
		m.containers.Title, m.containers.Empty = "[2] Vaults", "select a subscription"
		if r, ok := m.sources.Selected(); ok {
			switch r.group {
			case provider.KindDatabricks:
				m.containers.Title, m.containers.Empty = "[2] Scopes", "select a workspace"
			case provider.KindRepo:
				m.containers.Title, m.containers.Empty = "[2] Files", "select a repo"
			}
		}
		m.containers.SetRender(m.containerRender(false))
		if m.onLinks() {
			m.showLinkedContainers()
		} else if src, ok := m.selectedSource(); ok {
			m.containers.Title = "[2] Vaults"
			m.containers.Empty = "no vaults found"
			switch src.Kind {
			case provider.KindDatabricks:
				m.containers.Title = "[2] Scopes"
				m.containers.Empty = "no secret scopes"
			case provider.KindRepo:
				m.containers.Title = "[2] Files"
				m.containers.Empty = "no SOPS or env files here (see docs/repos.md)"
			}
			cmds = append(cmds, m.showContainers(src, id))
		}
	}

	// The active tab is part of the key, so switching tabs reloads too.
	c, hasContainer := m.containers.Selected()
	listKey := m.containers.SelectedID()
	if hasContainer {
		m.syncTabs(c)
		listKey += "|" + string(m.objType)
		// Moving to another vault or tab leaves the versions view.
		if v := m.versionsOf; v != nil && (containerKey(v.Container) != containerKey(c) || v.Type != m.objType) {
			m.closeVersions()
		}
		if v := m.versionsOf; v != nil {
			listKey = versionsKey(*v)
			m.secrets.Title = "[3] Versions of " + v.Name
			m.secrets.Tabs = nil
			m.secrets.Empty = "no versions"
		}
	} else {
		m.secrets.Tabs = nil
		m.secrets.Title = "[3] Secrets"
		m.secrets.Empty = "select a vault or scope"
	}
	if listKey != m.selContainer {
		m.cache.rememberCursor(m.selContainer, m.secrets.SelectedID())
		m.selContainer = listKey
		m.cancelReq(slotObjects)
		m.secrets.Reset()
		switch {
		case hasContainer && m.versionsOf != nil:
			cmds = append(cmds, m.showVersions(*m.versionsOf, listKey))
		case hasContainer:
			m.secrets.SetRender(m.objectRender())
			cmds = append(cmds, m.showObjects(c, listKey))
		}
	}

	if id := m.secrets.SelectedID(); id != m.selSecret || (id == "" && m.detailStale()) {
		m.selSecret = id
		m.cancelReq(slotValue)
		m.cancelReq(slotDescribe)
		m.cancelReq(slotVersions)
		if s, ok := m.secrets.Selected(); ok {
			m.detail.ShowSecret(s)
			cmds = append(cmds, m.describe(s), m.countVersions(s))
		} else if c, ok := m.containers.Selected(); ok {
			m.detail.ShowContainer(c)
		} else {
			m.detail.Clear()
		}
		m.refreshDetailNote()
	}
	return tea.Batch(cmds...)
}

// showContainers shows src's containers from the cache, loading them if
// they are missing or stale.
func (m *Model) showContainers(src provider.Source, key string) tea.Cmd {
	e, ok := m.cache.containers[key]
	if !ok {
		m.containers.SetLoading()
		return m.loadContainers(src, key)
	}
	m.containers.SetItems(m.starredContainersFirst(e.value))
	m.containers.Select(m.cache.cursor[key])
	if e.fresh(m.now()) {
		return nil
	}
	m.containers.Refresh()
	m.containers.Age = m.cachedAge(e.at)
	return m.loadContainers(src, key)
}

// showObjects is showContainers for the Secrets pane.
func (m *Model) showObjects(c provider.Container, key string) tea.Cmd {
	e, ok := m.cache.objects[key]
	if !ok {
		m.secrets.SetLoading()
		return m.loadObjects(c, m.objType, key)
	}
	m.secrets.SetItems(m.starredFirst(e.value))
	m.secrets.Select(m.cache.cursor[key])
	if e.fresh(m.now()) {
		return nil
	}
	m.secrets.Refresh()
	m.secrets.Age = m.cachedAge(e.at)
	return m.loadObjects(c, m.objType, key)
}

// showVersions lists o's versions, newest first. They are cached like
// object lists.
func (m *Model) showVersions(o provider.Object, key string) tea.Cmd {
	if e, ok := m.cache.objects[key]; ok && e.fresh(m.now()) {
		m.setVersions(e.value)
		m.secrets.Select(m.cache.cursor[key])
		return nil
	}
	m.secrets.SetLoading()
	p := m.providerFor(o.Container.Source)
	ctx, req := m.begin(slotObjects)
	return func() tea.Msg {
		items, err := p.Versions(ctx, o)
		for i := range items {
			items[i].Container = o.Container
		}
		return secretsMsg{req: req, key: key, items: items, err: err}
	}
}

// versionsKey caches an object's versions; the versions view and the
// version count share it.
func versionsKey(o provider.Object) string {
	return containerKey(o.Container) + "|" + string(o.Type) + "|versions|" + o.ID
}

// countVersions fetches the selected object's versions in the background
// so the detail pane can list them.
func (m *Model) countVersions(o provider.Object) tea.Cmd {
	if m.versionsOf != nil {
		return nil // the list already shows them
	}
	key := versionsKey(o)
	if e, ok := m.cache.objects[key]; ok {
		m.detail.SetVersions(o.ID, e.value)
		if e.fresh(m.now()) {
			return nil
		}
	}
	p := m.providerFor(o.Container.Source)
	ctx, req := m.begin(slotVersions)
	return m.settled(ctx, func() tea.Msg {
		items, err := p.Versions(ctx, o)
		for i := range items {
			items[i].Container = o.Container
		}
		return versionCountMsg{req: req, key: key, id: o.ID, items: items, err: err}
	})
}

// closeVersions returns the Secrets pane to the object list, with the
// cursor on the object whose versions were shown.
func (m *Model) closeVersions() {
	m.versionsOf = nil
}

// syncTabs sets the Secrets pane tabs from the container's object types,
// keeping the user's preferred type when the container has it.
func (m *Model) syncTabs(c provider.Container) {
	types := c.ObjectTypes()
	m.objType = types[0]
	m.secrets.Tabs = nil
	for i, t := range types {
		if t == m.wantType {
			m.objType = t
			m.secrets.Tab = i
		}
		m.secrets.Tabs = append(m.secrets.Tabs, tabLabels[t])
	}
	if m.objType != m.wantType {
		m.secrets.Tab = 0
	}
	m.secrets.Title = "[3] " + tabLabels[m.objType]
	if len(types) > 1 {
		m.secrets.Title = "[3]"
	}
	m.secrets.Empty = "no " + strings.ToLower(pluralLabels[m.objType])
}

var (
	tabLabels = map[provider.ObjectType]string{
		provider.TypeSecret: "Secrets", provider.TypeKey: "Keys", provider.TypeCertificate: "Certs",
	}
	pluralLabels = map[provider.ObjectType]string{
		provider.TypeSecret: "Secrets", provider.TypeKey: "Keys", provider.TypeCertificate: "Certificates",
	}
)

// switchTab moves the Secrets pane to the next (delta=1) or previous
// (delta=-1) object type of the selected container.
func (m *Model) switchTab(delta int) {
	if m.focus == paneSources {
		m.sourcesFavs = !m.sourcesFavs // two tabs: either way flips
		m.sourcesTouched = false       // onto the first source of the tab
		m.rebuildSources()
		return
	}
	c, ok := m.containers.Selected()
	if !ok {
		return
	}
	types := c.ObjectTypes()
	if len(types) < 2 {
		return
	}
	i := 0
	for j, t := range types {
		if t == m.objType {
			i = j
		}
	}
	m.wantType = types[(i+delta+len(types))%len(types)]
}

// detailStale reports whether the detail pane shows a container other than
// the selected one, which happens when the container changes while no
// secret is selected.
func (m *Model) detailStale() bool {
	_, hasSecret := m.detail.Secret()
	shown := ""
	if c, ok := m.detail.Container(); ok {
		shown = containerKey(c)
	}
	return hasSecret || shown != m.containers.SelectedID()
}

// cachedAge is how old a list loaded at at is, if it's older than
// cacheTTL: the panes say it while it reloads.
func (m Model) cachedAge(at time.Time) string {
	if d := m.now().Sub(at); d >= cacheTTL {
		return ago(d)
	}
	return ""
}

// oldestSources is when the oldest of the cached Sources lists loaded.
func (m Model) oldestSources() time.Time {
	var oldest time.Time
	for _, at := range m.sourcesAt {
		if oldest.IsZero() || at.Before(oldest) {
			oldest = at
		}
	}
	return oldest
}

func (m *Model) handleSources(msg sourcesMsg) tea.Cmd {
	if msg.gen != m.sourcesGen {
		return nil
	}
	m.sourcesPending--
	m.sourceResults[msg.provider] = msg
	if msg.err == nil {
		m.sourcesAt[msg.provider], m.cacheDirty = m.now(), true
	}

	var errs []string
	for _, p := range m.providers {
		if r, ok := m.sourceResults[p.ID()]; ok && r.err != nil {
			m.noteErr(r.err)
			m.logErrOf(p.Label(), r.err)
			errs = append(errs, fmt.Sprintf("%s: %v", p.Label(), r.err))
		}
	}
	// Said once, when they change; after that the Details pane says it
	// for the selected account.
	if status := strings.Join(errs, " · "); status != m.status {
		m.status = status
		if status != "" {
			m.flash = flash{text: m.problemText(msg.provider, status), isErr: true}
		}
	}
	m.rebuildSources()
	m.sources.Loading = m.sourcesPending > 0
	return m.autoLoad(msg)
}

// autoLoad loads local sources (repos) whole as they're found: their
// files are on disk, so listing every file's keys is cheap, and Go to
// can search them without asking.
func (m *Model) autoLoad(msg sourcesMsg) tea.Cmd {
	p, ok := m.byID[msg.provider]
	if !ok || msg.err != nil || p.Kind() != provider.KindRepo {
		return nil
	}
	for _, src := range msg.sources {
		m.queueSource(src, false)
	}
	return m.crawlStart()
}

// begin starts a cancellable request for pane p, cancelling any in flight.
func (m *Model) begin(p slot) (context.Context, int) {
	m.cancelReq(p)
	m.seq++
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	m.reqID[p], m.cancel[p] = m.seq, cancel
	return ctx, m.seq
}

func (m *Model) cancelReq(p slot) {
	if m.cancel[p] != nil {
		m.cancel[p]()
	}
	m.reqID[p], m.cancel[p] = 0, nil
}

func (m *Model) touched() {
	if m.onActive != nil {
		m.onActive()
	}
}

// settled runs load after the wait for the cursor to settle, if a key
// press started it; a request cancelled meanwhile (the cursor moved on)
// is never sent.
func (m *Model) settled(ctx context.Context, load tea.Cmd) tea.Cmd {
	wait := m.waiting
	if wait <= 0 {
		return load
	}
	return func() tea.Msg {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			return load()
		}
	}
}

// done releases the context of a completed request.
func (m *Model) done(p slot) { m.cancelReq(p) }

// loadSources starts a new generation of source loads, superseding any in
// flight.
func (m *Model) loadSources() tea.Cmd {
	m.sourcesGen++
	m.sourcesPending = len(m.providers)
	m.sourceResults = map[string]sourcesMsg{}
	return m.sourcesCmd()
}

func (m Model) sourcesCmd() tea.Cmd {
	gen := m.sourcesGen
	var cmds []tea.Cmd
	for _, p := range m.providers {
		ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
		cmds = append(cmds, func() tea.Msg {
			defer cancel()
			s, err := p.Sources(ctx)
			for i := range s {
				s[i].Provider, s[i].ProviderLabel = p.ID(), p.Label()
			}
			return sourcesMsg{gen: gen, provider: p.ID(), sources: s, err: err}
		})
		// The source it started in, quickly, on its own.
		if c, ok := p.(provider.Current); ok {
			ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
			cmds = append(cmds, func() tea.Msg {
				defer cancel()
				src, ok := c.Current(ctx)
				if !ok {
					return nil
				}
				src.Provider, src.ProviderLabel = p.ID(), p.Label()
				return currentMsg{gen: gen, source: src}
			})
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) loadContainers(src provider.Source, key string) tea.Cmd {
	p := m.providerFor(src)
	ctx, req := m.begin(slotContainers)
	return m.settled(ctx, func() tea.Msg {
		items, err := p.Containers(ctx, src)
		// Carry the source as given, with its provider ID, so later
		// requests reach the same account.
		for i := range items {
			items[i].Source = src
		}
		return containersMsg{req: req, key: key, items: items, err: err}
	})
}

// loadObjects lists a container's objects. If the container's account
// isn't known (an old favourite), each account that can see its source is
// tried in turn, and the first that succeeds is reported in bound so the
// favourite can remember it.
func (m *Model) loadObjects(c provider.Container, t provider.ObjectType, key string) tea.Cmd {
	cands := m.candidates(c.Source)
	known := len(cands) == 1 && cands[0].ID() == c.Source.Provider
	ctx, req := m.begin(slotObjects)
	return m.settled(ctx, func() tea.Msg {
		var errs []error
		for _, p := range cands {
			cc := c
			cc.Source = boundTo(c.Source, p)
			items, err := p.Objects(ctx, cc, t)
			if err == nil {
				for i := range items {
					items[i].Container = cc
				}
				msg := secretsMsg{req: req, key: key, items: items}
				if !known {
					msg.bound = &cc
				}
				return msg
			}
			if ctx.Err() != nil || len(cands) == 1 {
				return secretsMsg{req: req, key: key, err: err}
			}
			errs = append(errs, fmt.Errorf("%s: %w", p.Label(), err))
		}
		return secretsMsg{req: req, key: key, err: errors.Join(errs...)}
	})
}

func (m *Model) fetchValue(s provider.Object) tea.Cmd {
	p := m.providerFor(s.Container.Source)
	ctx, req := m.begin(slotValue)
	m.detail.SetLoading()
	return func() tea.Msg {
		v, err := p.Value(ctx, s)
		return valueMsg{req: req, obj: s, value: v, err: err}
	}
}

func (m *Model) describe(o provider.Object) tea.Cmd {
	key := detailKey(o)
	if e, ok := m.cache.details[key]; ok {
		m.detail.SetDescribed(e.value)
		if e.fresh(m.now()) {
			return nil
		}
	} else {
		m.detail.SetDescribing()
	}
	p := m.providerFor(o.Container.Source)
	ctx, req := m.begin(slotDescribe)
	return m.settled(ctx, func() tea.Msg {
		obj, err := p.Describe(ctx, o)
		return describeMsg{req: req, key: key, obj: obj, err: err}
	})
}

// copyValue copies the selected object's value, fetching it without
// revealing it if it isn't shown already.
func (m *Model) copyValue() tea.Cmd {
	if m.focus == paneDetail && m.fields.Shown() {
		if f, ok := m.fields.Selected(); ok {
			return m.copyText(fieldName(f), cmpOr(f.URL, f.Value), false)
		}
		return nil
	}
	if a, ok := m.selectedAccount(); ok && a.Copy != "" && m.focus == paneSources {
		return m.copyText(a.CopyLabel, a.Copy, false)
	}
	o, ok := m.detail.Secret()
	if !ok {
		return nil
	}
	if v, ok := m.detail.Value(); ok {
		m.usedObject(o)
		return m.copyText(o.Name, v.Reveal(), true)
	}
	p := m.providerFor(o.Container.Source)
	ctx, req := m.begin(slotCopy)
	m.flash = flash{text: "copying " + o.Name + "…"}
	return func() tea.Msg {
		v, err := p.Value(ctx, o)
		return copyMsg{req: req, obj: o, name: o.Name, value: v, err: err}
	}
}

// copyText puts text on the clipboard. Sensitive text is cleared again
// after the configured delay.
func (m *Model) copyText(label, text string, sensitive bool) tea.Cmd {
	cmds := []tea.Cmd{m.clip.Write(text)}
	// This copy replaces whatever a pending clear was waiting for.
	m.cancelClipClear()
	if !sensitive || m.clipClear <= 0 {
		m.flash = done("copied " + label)
		return tea.Batch(cmds...)
	}
	m.flash = flash{} // the countdown line takes over from "copying…"
	m.clipFP = clipboard.Fingerprint(text)
	m.clipLabel = label
	m.clipDeadline = m.now().Add(m.clipClear)
	return tea.Batch(append(cmds, m.clipTick())...)
}

// clipTick fires when the countdown's displayed second next changes.
func (m *Model) clipTick() tea.Cmd {
	wait := m.clipDeadline.Sub(m.now()) % time.Second
	if wait <= 0 {
		wait = time.Second
	}
	token := m.clipToken
	return tea.Tick(wait, func(time.Time) tea.Msg { return clipTickMsg{token: token} })
}

func (m *Model) cancelClipClear() {
	m.clipToken++
	m.clipFP, m.clipLabel, m.clipDeadline = [32]byte{}, "", time.Time{}
}

// openSelected opens the focused pane's selected item in the browser. Over
// SSH it copies the URL instead.
func (m *Model) openSelected() tea.Cmd {
	var name, url string
	var used func()
	switch {
	case m.focus == paneDetail && m.fields.Shown():
		f, _ := m.fields.Selected()
		if f.URL == "" {
			m.flash = warning("nothing to open in the browser here")
			return nil
		}
		name, url, used = f.Label, f.URL, func() {}
	default:
		name, url, used = m.openTarget()
	}
	if name == "" {
		return nil
	}
	return m.openPage(name, url, used)
}

// openTarget is the focused pane's selected item, for the browser.
func (m *Model) openTarget() (name, url string, used func()) {
	switch m.focus {
	case paneSources:
		s, _ := m.selectedSource()
		name, url, used = s.Name, s.URL, func() { m.usedSource(s) }
	case paneContainers:
		c, _ := m.containers.Selected()
		name, url, used = c.Name, c.URL, func() { m.usedContainer(c) }
	default:
		o, _ := m.detail.Secret()
		name, url, used = o.Name, o.URL, func() { m.usedObject(o) }
	}
	return name, url, used
}

// openPage opens url in the browser, or copies it over SSH.
func (m *Model) openPage(name, url string, used func()) tea.Cmd {
	if url == "" {
		m.flash = warning("no web page for " + name)
		return nil
	}
	err := m.openURL(url)
	if err == nil || errors.Is(err, browser.ErrRemote) {
		used()
	}
	switch {
	case errors.Is(err, browser.ErrRemote):
		cmd := m.copyText("link to "+name, url, false)
		m.flash.text += " (can't open a browser over SSH)"
		return cmd
	case err != nil:
		m.flash = flash{text: "open: " + err.Error(), isErr: true}
	default:
		m.flash = done("opened " + name + " in the browser")
	}
	return nil
}

// noteErr remembers authentication failures so the UI can offer sign-in,
// saying so once for each account.
func (m *Model) noteErr(err error) {
	var auth *provider.AuthError
	if errors.As(err, &auth) {
		if m.auth == nil || m.auth.Who() != auth.Who() {
			m.flash = flash{text: m.authText(auth), isErr: true}
		}
		m.auth = auth
	}
}

// authText says which account needs signing in, and how.
func (m Model) authText(auth *provider.AuthError) string {
	if len(auth.Login) == 0 {
		return fmt.Sprintf("%s: sign-in required: %v", auth.Who(), auth.Err)
	}
	return fmt.Sprintf("%s: sign-in required (not signed in or session expired)%s",
		auth.Who(), withKey(m.keys.Login, " · press %s to run "+strings.Join(auth.Login, " ")))
}

func (m Model) anyLoading() bool {
	return m.sources.Loading || m.containers.Loading || m.secrets.Loading ||
		m.detail.State() == panes.ValueLoading
}

const (
	minWidth  = 50
	minHeight = 12
)

func (m Model) footerView() string {
	var help string
	if l := m.focusedFilter(); m.search != nil || m.prompt != nil {
		help = m.helpBar(m.popupKeys(), 1) // the popup's keys, ? help last
	} else if l != nil && l.Filtering() {
		help = m.helpBar(keys.FilterHelp{Map: m.keys}.ShortHelp(), 2)
	} else {
		revealed := m.detail.State() == panes.ValueShown
		help = m.helpBar(contextHelp{k: m.keys, focus: m.focus, of: m.detailOf(), commands: len(m.commandsHere()) > 0, account: m.fields.Shown(), revealed: &revealed}.ShortHelp(), 2)
	}
	var lines []string
	if !m.clipDeadline.IsZero() {
		left := max(0, int(math.Ceil(m.clipDeadline.Sub(m.now()).Seconds())))
		lines = append(lines, m.flashLine(done(fmt.Sprintf("copied %s · clipboard clears in %ds", m.clipLabel, left))))
	}
	if m.flash.text != "" {
		lines = append(lines, m.flashLine(m.flash))
	}
	return strings.Join(append(lines, " "+help), "\n")
}

// flashLine draws a message: errors in red, confirmations after a ✓.
func (m Model) flashLine(f flash) string {
	// One line here: a longer message (sops' errors) keeps its first, and
	// says where the rest is.
	if first, _, multi := strings.Cut(f.text, "\n"); multi {
		f.text = first + " … (the whole message: " + withKey(m.keys.Reveal, "%s on the secret") + ")"
	}
	var s string
	switch {
	case f.isErr:
		s = m.st.Error.Render("✗ " + f.text)
	case f.warn:
		s = m.st.Warning.Render("⚠ " + f.text)
	case f.ok:
		s = m.st.Success.Render("✓") + " " + m.st.Notice.Render(f.text)
	default:
		s = m.st.Notice.Render(f.text)
	}
	return lipgloss.NewStyle().MaxWidth(m.w).Render(" " + s) // off the edge
}

func (m Model) View() tea.View {
	var content string
	if m.w < minWidth || m.h < minHeight {
		content = fmt.Sprintf("Terminal too small (%dx%d); need at least %dx%d.", m.w, m.h, minWidth, minHeight)
	} else {
		content = m.panesView() + "\n" + m.footerView()
		if m.prompt != nil || m.search != nil {
			content = m.overlay(content)
		}
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.ForegroundColor = m.defaultFg
	v.ReportFocus = true // to look for links on the clipboard
	v.WindowTitle = "lazysecrets"
	return v
}

// selectedProblem is why the selected Sources row's account has nothing
// to show, if it failed: for the Details pane.
func (m Model) selectedProblem() string {
	r, ok := m.sources.Selected()
	if !ok || (r.kind != rowGroup && r.kind != rowSource) {
		return ""
	}
	res, done := m.sourceResults[r.provider]
	if !done || res.err == nil {
		return ""
	}
	return m.problemText(r.provider, res.err.Error())
}

// problemText says what went wrong for a provider: for a sign-in, which
// account and how to sign in again; else err as given.
func (m Model) problemText(providerID, err string) string {
	var auth *provider.AuthError
	if r, ok := m.sourceResults[providerID]; ok && errors.As(r.err, &auth) {
		return m.authText(auth)
	}
	return err
}

// fieldLink is the link of the selected Fields row, if it has one.
func (m Model) fieldLink() string {
	if f, ok := m.fields.Selected(); ok && m.fields.Shown() {
		return f.Link
	}
	return ""
}

// loginFor is the sign-in L runs: the selected account's, if it needs
// one, else the last one asked for.
func (m Model) loginFor() *provider.AuthError {
	if f, ok := m.fields.Selected(); ok && m.focus == paneDetail && m.fields.Shown() && f.Provider != "" {
		var auth *provider.AuthError
		if res := m.sourceResults[f.Provider]; errors.As(res.err, &auth) && len(auth.Login) > 0 {
			return auth
		}
	}
	if r, ok := m.sources.Selected(); ok {
		var auth *provider.AuthError
		if res := m.sourceResults[r.provider]; errors.As(res.err, &auth) && len(auth.Login) > 0 {
			return auth
		}
	}
	if m.auth != nil && len(m.auth.Login) > 0 {
		return m.auth
	}
	return nil
}

// selectedAccount describes the account of the selected provider row, if
// its provider can.
func (m Model) selectedAccount() (provider.Account, bool) {
	r, ok := m.sources.Selected()
	if !ok || r.kind != rowGroup {
		return provider.Account{}, false
	}
	if res := m.sourceResults[r.provider]; res.err != nil {
		return provider.Account{}, false // the problem shows instead
	}
	a, ok := m.byID[r.provider].(provider.Accounts)
	if !ok {
		return provider.Account{}, false
	}
	return a.Account()
}
