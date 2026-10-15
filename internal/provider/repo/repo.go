// Package repo browses the encrypted secret files in git repositories:
// a repo is a source, its secret files the containers (vault level), and
// their keys the secrets. Secret files are SOPS files, plain env files and
// config files: Spring Boot's and Docker Compose's (configfile.go).
//
// Listing never decrypts: SOPS encrypts values only, so a file's keys and
// metadata are read directly. A value is decrypted when it's revealed or
// copied, one at a time, by the sops binary, with the keys sops itself
// uses (age, Azure Key Vault through az, AWS/GCP KMS, PGP).
//
// Which files: tracked by git, then, in order of preference, the include
// and exclude globs of the repo's .lazysecrets.yaml, those of the config,
// or where the repo's .sops.yaml creation_rules say SOPS files go; a repo
// with none of those is searched whole.
package repo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"

	"git.kebcloud.no/keb/lazysecrets/internal/jasypt"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

type Settings struct {
	// Dir is where lazysecrets started: the repo containing it, if any,
	// is listed first and marked Here, with its files under Dir first.
	Dir string
	// Paths are repos to show.
	Paths []string
	// Roots are folders of repos (like ~/git): their repos, and repos one
	// level further down (org/repo).
	Roots []string
	// Starred are repos the user starred something in, shown wherever
	// they are.
	Starred []string
	// Include and Exclude are globs relative to a repo's root (** matches
	// any depth), for repos without their own .lazysecrets.yaml.
	Include, Exclude []string
	// SecretPatterns say which keys of config files are secrets, Types
	// per type of file (spring), for repos whose .lazysecrets.yaml
	// doesn't.
	SecretPatterns SecretPatterns
	Types          map[string]FileType
	// Sops is the sops binary; "sops" (on PATH) when empty.
	Sops string
}

type Provider struct {
	s     Settings
	repos []repoInfo
	err   error
	once  sync.Once

	// here is the repo containing Dir, found on its own so it can show
	// before the roots are scanned.
	here     *repoInfo
	hereOnce sync.Once
}

var (
	_ provider.Provider = (*Provider)(nil)
	_ provider.Locator  = (*Provider)(nil)
	_ provider.Current  = (*Provider)(nil)
)

type repoInfo struct {
	root   string // absolute
	name   string // the folder, or its path under a root (org/repo)
	id     string // the remote without scheme or .git, else the root path
	web    string // the remote's web page, if it has one
	prefix string // Dir relative to root ("" when Dir isn't in it)
	here   bool   // the repo lazysecrets started in
}

// DefaultRoots are the usual folders of repos, used when the config
// doesn't set roots. Ones that don't exist cost a stat each.
var DefaultRoots = []string{"~/git", "~/repo", "~/repos", "~/projects", "~/code", "~/source", "~/src"}

func New(s Settings) *Provider {
	if s.Sops == "" {
		s.Sops = "sops"
	}
	return &Provider{s: s}
}

func (p *Provider) Kind() provider.Kind { return provider.KindRepo }
func (p *Provider) ID() string          { return "Repos" }
func (p *Provider) Label() string       { return "Repos" }

func (p *Provider) load() {
	p.once.Do(func() {
		seen := map[string]bool{}
		var roots []string
		names := map[string]string{} // path under a root (org/repo)
		add := func(root string) {
			if !seen[root] {
				seen[root] = true
				roots = append(roots, root)
			}
		}
		here, inRepo := p.current()
		if inRepo {
			seen[here.root] = true
		}
		for _, dir := range p.s.Paths {
			root, err := gitRoot(expandHome(dir))
			if err != nil {
				p.err = errors.Join(p.err, fmt.Errorf("%s: not a git repository", dir))
				continue
			}
			add(root)
		}
		// Missing roots are skipped (most of the defaults are), and one
		// that's a symlink to another is scanned once.
		scanned := map[string]bool{}
		for _, dir := range p.s.Roots {
			base, err := filepath.EvalSymlinks(expandHome(dir))
			if err != nil || scanned[base] {
				continue
			}
			scanned[base] = true
			for _, root := range reposIn(base, 2) {
				// A repo under two roots keeps the first one's name.
				if name, err := filepath.Rel(base, root); err == nil && !strings.HasPrefix(name, "..") && names[root] == "" {
					names[root] = filepath.ToSlash(name)
				}
				add(root)
			}
		}
		for _, dir := range p.s.Starred {
			if root, err := gitRoot(dir); err == nil {
				add(root)
			}
		}
		// A git call each for the remote: dozens under ~/git, so several
		// at a time.
		infos := make([]repoInfo, len(roots))
		parallel(len(roots), func(i int) {
			infos[i] = info(roots[i], "")
			if n := names[roots[i]]; n != "" {
				infos[i].name = n
			}
		})
		if inRepo {
			infos = append([]repoInfo{here}, infos...)
		}
		p.repos = infos
	})
}

// current is the repo lazysecrets started in.
func (p *Provider) current() (repoInfo, bool) {
	p.hereOnce.Do(func() {
		root, err := gitRoot(p.s.Dir)
		if err != nil {
			return
		}
		rel, _ := filepath.Rel(root, absPath(p.s.Dir))
		if rel == "." {
			rel = ""
		}
		r := info(root, filepath.ToSlash(rel))
		r.here = true
		p.here = &r
	})
	if p.here == nil {
		return repoInfo{}, false
	}
	return *p.here, true
}

// Current is the repo lazysecrets started in, if it has secret files:
// one repo to check, so it can show before the others are.
func (p *Provider) Current(ctx context.Context) (provider.Source, bool) {
	r, ok := p.current()
	if !ok {
		return provider.Source{}, false
	}
	src := p.source(r)
	files, err := p.Containers(ctx, src)
	if err == nil && len(files) == 0 {
		return provider.Source{}, false
	}
	src.Hidden = err == nil && allHidden(files)
	return src, true
}

// allHidden reports whether a repo's files are all hidden: the repo is too.
func allHidden(files []provider.Container) bool {
	return !slices.ContainsFunc(files, func(c provider.Container) bool { return !c.Hidden })
}

func (p *Provider) source(r repoInfo) provider.Source {
	return provider.Source{
		Kind: provider.KindRepo, Provider: p.ID(), ProviderLabel: p.Label(),
		ID: r.root, Name: r.name, Detail: r.id, URL: r.web, Here: r.here,
	}
}

// parallel runs f for 0..n-1, several at a time.
func parallel(n int, f func(i int)) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(8, runtime.NumCPU()))
	for i := range n {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			f(i)
		})
	}
	wg.Wait()
}

// reposIn finds git repositories in dir, depth levels down at most,
// without looking inside a repo or hidden folders.
func reposIn(dir string, depth int) []string {
	entries, err := os.ReadDir(dir)
	if err != nil || depth == 0 {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			if abs, err := filepath.EvalSymlinks(path); err == nil {
				path = abs
			}
			out = append(out, path)
			continue
		}
		out = append(out, reposIn(path, depth-1)...)
	}
	return out
}

func info(root, prefix string) repoInfo {
	id, web := remoteID(root)
	if id == "" {
		id = root
	}
	return repoInfo{root: root, name: filepath.Base(root), id: id, web: web, prefix: prefix}
}

// Sources lists the repos that have secret files: one without any isn't
// worth a row.
func (p *Provider) Sources(ctx context.Context) ([]provider.Source, error) {
	p.load()
	// Each repo is checked for secret files, several at a time: under
	// ~/git that's dozens of repos.
	keep := make([]bool, len(p.repos))
	srcs := make([]provider.Source, len(p.repos))
	for i, r := range p.repos {
		srcs[i] = p.source(r)
	}
	parallel(len(srcs), func(i int) {
		files, err := p.Containers(ctx, srcs[i])
		keep[i] = err != nil || len(files) > 0
		srcs[i].Hidden = err == nil && allHidden(files)
	})
	var out []provider.Source
	for i, src := range srcs {
		if keep[i] {
			out = append(out, src)
		}
	}
	if len(out) == 0 && p.err != nil {
		return nil, p.err
	}
	return out, nil
}

func (p *Provider) repo(id string) (repoInfo, bool) {
	if r, ok := p.current(); ok && r.root == id {
		return r, true // without waiting for the others
	}
	p.load()
	for _, r := range p.repos {
		if r.root == id {
			return r, true
		}
	}
	return repoInfo{}, false
}

// Containers lists a repo's secret files: those under where lazysecrets
// started first, then the rest, each by path.
func (p *Provider) Containers(ctx context.Context, src provider.Source) ([]provider.Container, error) {
	r, ok := p.repo(src.ID)
	if !ok {
		return nil, fmt.Errorf("unknown repo %s", src.ID)
	}
	files, err := tracked(ctx, r.root)
	if err != nil {
		return nil, err
	}
	user, sopsWhere, err := p.selector(r.root, files)
	if err != nil {
		return nil, err
	}
	// SOPS files are tracked, where .sops.yaml says they go. Env files
	// usually aren't tracked (they're in .gitignore), so they're looked
	// for by name among untracked and ignored files too.
	envs := envFiles(r.root, files)
	seen := map[string]bool{}
	var out []provider.Container
	add := func(f string) {
		if seen[f] || !user(f) {
			return
		}
		seen[f] = true
		path := filepath.Join(r.root, f)
		c := provider.Container{Source: src, ID: path, Name: f, Types: []provider.ObjectType{provider.TypeSecret}}
		switch meta, isSops := readMeta(path); {
		case isSops && sopsWhere(f):
			c.Detail = "sops · " + strings.Join(meta.methods, ", ")
		case isSops:
			return // a SOPS file where .sops.yaml says SOPS files don't go
		case isEnvNamed(f):
			c.Detail = provider.DetailPlainEnv
			c.Hidden = isExample(f)
		case isConfig(f):
			t, _ := configTypeOf(f)
			c.Detail = t.detail(f)
			c.Hidden = isExample(f) || p.configHidden(t, r.root, path)
		default:
			return
		}
		out = append(out, c)
	}
	for _, f := range envs {
		add(f)
	}
	for _, f := range files {
		if !inPackages(f) {
			add(f)
		}
	}
	under := func(c provider.Container) bool {
		return r.prefix == "" || strings.HasPrefix(c.Name, r.prefix+"/")
	}
	slices.SortStableFunc(out, func(a, b provider.Container) int {
		if ua, ub := under(a), under(b); ua != ub {
			if ua {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

func (p *Provider) Objects(_ context.Context, c provider.Container, t provider.ObjectType) ([]provider.Object, error) {
	if t != provider.TypeSecret {
		return nil, nil
	}
	b, err := os.ReadFile(c.ID)
	if err != nil {
		return nil, err
	}
	meta, isSops := parseMeta(c.ID, b)
	if t, ok := configTypeOf(c.ID); ok && !isSops {
		return p.configObjects(t, c, b)
	}
	keys, err := parseKeys(c.ID, b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.Name, err)
	}
	attrs := []provider.Attr{{Label: "format", Value: "env"}, {Label: "encrypted", Value: "no: plain text in the file"}}
	if isSops {
		attrs = []provider.Attr{{Label: "format", Value: "sops " + formatOf(c.ID)}, {Label: "encryption", Value: strings.Join(meta.methods, ", ")}}
	}
	out := make([]provider.Object, 0, len(keys))
	for _, k := range keys {
		o := provider.Object{
			Container: c, Type: provider.TypeSecret,
			ID: c.ID + "\x00" + k.extract, Name: k.name, Updated: meta.modified, Attrs: attrs,
		}
		switch {
		case isSops && !k.encrypted:
			o.Attrs = append(slices.Clip(attrs), provider.Attr{Label: "encrypted", Value: "no (left in plain text by the file's sops rules)"})
		case !isSops && k.jasypt:
			o.Attrs, o.Detail = []provider.Attr{attrs[0], {Label: "encrypted", Value: "jasypt"}}, "jasypt"
		}
		out = append(out, o)
	}
	return out, nil
}

func (p *Provider) Describe(_ context.Context, o provider.Object) (provider.Object, error) {
	return o, nil
}

func (p *Provider) Versions(context.Context, provider.Object) ([]provider.Object, error) {
	return nil, provider.ErrUnsupported
}

// Value reads one key: from a plain file, decrypting a Jasypt value; from
// a SOPS file, with sops.
func (p *Provider) Value(ctx context.Context, o provider.Object) (provider.SecretValue, error) {
	file, extract, ok := strings.Cut(o.ID, "\x00")
	if !ok { // made from a link: the repo's root, the file's path, the key
		file = filePath(o.Container)
	}
	if _, isSops := readMeta(file); !isSops {
		read := plainValue
		if t, ok := configTypeOf(file); ok {
			read = t.value
		}
		v, err := read(file, o.Name)
		if err != nil {
			return v, err
		}
		return jasyptValue(file, o.Name, v)
	}
	if !ok {
		var err error
		if extract, err = extractOf(file, o.Name); err != nil {
			return provider.SecretValue{}, err
		}
	}
	cmd := exec.CommandContext(ctx, p.s.Sops, "decrypt", "--extract", extract, file)
	cmd.Dir = filepath.Dir(file) // sops looks for .sops.yaml from here
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return provider.SecretValue{}, errors.New("sops isn't installed: decrypting needs it (brew install sops)")
		}
		return provider.SecretValue{}, errors.New(sopsError(stderr.String(), err))
	}
	return provider.NewSecretValue(stdout.Bytes()), nil
}

// Contents reads the file c is.
func (p *Provider) Contents(_ context.Context, c provider.Container) ([]byte, error) {
	f, err := p.File(c, nil)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(f.Path)
}

// filePath is the file c is. A container made from a link has no ID: the
// file is its path in the repo.
func filePath(c provider.Container) string {
	if c.ID != "" {
		return c.ID
	}
	return filepath.Join(c.Source.ID, filepath.FromSlash(c.Name))
}

// File is the file c is, and the line o's key is on.
func (p *Provider) File(c provider.Container, o *provider.Object) (provider.File, error) {
	path := filePath(c)
	f := provider.File{Path: path}
	if _, isSops := readMeta(path); isSops {
		f.Sops = p.s.Sops
	}
	if o == nil {
		return f, nil
	}
	if t, ok := configTypeOf(path); ok && f.Sops == "" {
		if k, err := t.keyNamed(path, o.Name); err == nil {
			f.Line = k.line
		}
		return f, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	keys, err := parseKeys(path, b)
	if err != nil {
		return f, nil // still worth opening, at the top
	}
	_, extract, _ := strings.Cut(o.ID, "\x00")
	for _, k := range keys {
		if k.extract == extract || (extract == "" && k.name == o.Name) {
			f.Line = k.line
			break
		}
	}
	return f, nil
}

// sopsError is sops' own message, whole: which keys it tried and why
// each failed can all matter.
func sopsError(stderr string, err error) string {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = err.Error()
	}
	return "sops couldn't decrypt it:\n" + msg
}

// Locate finds the repo a link names, by its remote (or root path).
func (p *Provider) Locate(_ context.Context, host string) (string, bool, error) {
	p.load()
	for _, r := range p.repos {
		if strings.EqualFold(r.id, host) {
			return r.root, true, nil
		}
	}
	return "", false, nil
}

// Root is the root of the git repo dir is in, and the repo's ID: its
// remote without scheme or .git (a repo link's host), else the root.
func Root(dir string) (root, id string, err error) {
	root, err = gitRoot(dir)
	if err != nil {
		return "", "", fmt.Errorf("%s isn't in a git repository", dir)
	}
	return root, info(root, "").id, nil
}

// --- git ---

func gitRoot(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("no directory")
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func tracked(ctx context.Context, root string) ([]string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files in %s: %w", root, err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// remoteID turns the origin remote into an ID ("git.kebcloud.no/keb/repo")
// and a web URL, both empty without a remote.
func remoteID(root string) (string, string) {
	// From .git/config directly: a git process per repo is most of what
	// listing dozens of them costs. git answers for worktrees and
	// submodules (.git is a file) and remotes it rewrites (insteadOf).
	if url, ok := originURL(filepath.Join(root, ".git", "config")); ok {
		if url == "" {
			return "", ""
		}
		return normalizeRemote(url)
	}
	out, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", ""
	}
	return normalizeRemote(strings.TrimSpace(string(out)))
}

// originURL reads [remote "origin"] url from a git config file. It isn't
// ok when the file is missing or uses insteadOf, which only git resolves.
func originURL(config string) (string, bool) {
	b, err := os.ReadFile(config)
	if err != nil || bytes.Contains(b, []byte("insteadOf")) || bytes.Contains(b, []byte("insteadof")) {
		return "", false
	}
	inOrigin := false
	for line := range strings.SplitSeq(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inOrigin = line == `[remote "origin"]`
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && inOrigin && strings.TrimSpace(k) == "url" {
			return strings.TrimSpace(v), true
		}
	}
	return "", true // no origin
}

// normalizeRemote handles https://host/path(.git), ssh://user@host[:port]/path
// and scp-style user@host:path.
func normalizeRemote(remote string) (string, string) {
	u := strings.TrimSuffix(remote, ".git")
	scheme := false
	for _, s := range []string{"https://", "http://", "ssh://", "git://"} {
		if rest, ok := strings.CutPrefix(u, s); ok {
			u, scheme = rest, true
		}
	}
	host, rest, _ := strings.Cut(u, "/")
	if !scheme {
		// scp-style: user@host:path
		if h, r, ok := strings.Cut(u, ":"); ok {
			host, rest = h, r
		}
	}
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	host, _, _ = strings.Cut(host, ":") // a port
	if host == "" || rest == "" {
		return "", ""
	}
	id := strings.ToLower(host + "/" + strings.Trim(rest, "/"))
	if strings.Contains(host, ".") {
		return id, "https://" + host + "/" + strings.Trim(rest, "/")
	}
	return id, ""
}

// --- which files ---

// lazysecretsFile is a repo's own .lazysecrets.yaml.
type lazysecretsFile struct {
	Include        []string       `yaml:"include"`
	Exclude        []string       `yaml:"exclude"`
	SecretPatterns SecretPatterns `yaml:"secretPatterns"`
	Spring         FileType       `yaml:"spring"`
	Compose        FileType       `yaml:"compose"`
	Sharing        struct {
		// Key is the link to the secret that encrypts the repo's
		// shared env files (package share).
		Key string `yaml:"key"`
	} `yaml:"sharing"`
}

func (f lazysecretsFile) types() map[string]FileType {
	return map[string]FileType{"spring": f.Spring, "compose": f.Compose}
}

// ShareKey is the sharing.key of the repo's .lazysecrets.yaml, if any.
func ShareKey(root string) (string, error) {
	f, err := readLazysecretsFile(root)
	return f.Sharing.Key, err
}

// readLazysecretsFile reads the repo's .lazysecrets.yaml; without one, it's
// empty.
func readLazysecretsFile(root string) (lazysecretsFile, error) {
	var f lazysecretsFile
	b, err := os.ReadFile(filepath.Join(root, ".lazysecrets.yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("%s/.lazysecrets.yaml: %w", root, err)
	}
	return f, nil
}

// selector says which files to look at: user (the include/exclude of the
// repo's .lazysecrets.yaml or the config) for every file, and sopsWhere
// (.sops.yaml's creation_rules, when there's no include) for SOPS files.
func (p *Provider) selector(root string, files []string) (user, sopsWhere func(string) bool, err error) {
	f, err := readLazysecretsFile(root)
	if err != nil {
		return nil, nil, err
	}
	include, exclude := p.s.Include, p.s.Exclude
	if len(f.Include) > 0 || len(f.Exclude) > 0 {
		include, exclude = f.Include, f.Exclude
	}
	inc, err := globs(include)
	if err != nil {
		return nil, nil, err
	}
	exc, err := globs(exclude)
	if err != nil {
		return nil, nil, err
	}
	var rules []sopsRule
	if len(inc) == 0 {
		rules = sopsRules(root, files)
	}
	user = func(f string) bool {
		return !anyMatch(exc, f) && (len(inc) == 0 || anyMatch(inc, f))
	}
	sopsWhere = func(f string) bool {
		return len(rules) == 0 || slices.ContainsFunc(rules, func(r sopsRule) bool { return r.matches(f) })
	}
	return user, sopsWhere, nil
}

// envFiles lists env files by name (.env, .env.*, *.env), tracked or
// not, ignored ones included, leaving out examples and dependencies.
// tracked is the repo's tracked files. Untracked ones are looked for in
// the folders that have tracked files, not by walking the whole tree: an
// ignored .env sits next to the code it's for, and the walk would go
// through node_modules and the like, which is most of the time a scan
// takes.
func envFiles(root string, tracked []string) []string {
	var out []string
	isTracked := make(map[string]bool, len(tracked))
	dirs := map[string]bool{".": true}
	for _, f := range tracked {
		isTracked[f] = true
		if isEnvNamed(f) {
			out = append(out, f)
		}
		if d := path.Dir(f); !dirs[d] && !inPackages(f) {
			dirs[d] = true
		}
	}
	for d := range dirs {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(d)))
		if err != nil {
			continue
		}
		for _, e := range entries {
			f := path.Join(d, e.Name())
			if e.Type().IsRegular() && !isTracked[f] && isEnvNamed(f) {
				out = append(out, f)
			}
		}
	}
	return out
}

// packages are folders of dependencies, caches and build output: never
// looked in for secret files.
var packages = map[string]bool{
	"node_modules": true, "bower_components": true, "jspm_packages": true,
	"vendor": true, "Pods": true, "Carthage": true,
	".venv": true, "venv": true, "__pycache__": true, ".tox": true, ".nox": true,
	".mypy_cache": true, ".pytest_cache": true, ".ruff_cache": true,
	".terraform": true, ".terragrunt-cache": true,
	".gradle": true, ".m2": true, ".yarn": true, ".pnpm-store": true,
	".npm": true, ".cache": true, ".next": true, ".nuxt": true, ".svelte-kit": true,
	".git": true,
}

// inPackages reports whether f is inside one of the packages folders.
func inPackages(f string) bool {
	for d := range strings.SplitSeq(path.Dir(f), "/") {
		if packages[d] {
			return true
		}
	}
	return false
}

// IsEnv reports whether a file is named like an env file with real values:
// .env, .env.local, prod.env; not .env.example and the like, nor one in
// dependencies.
func IsEnv(f string) bool {
	return isEnvNamed(f) && !isExample(f)
}

// isEnvNamed reports whether a file outside dependencies is named like an
// env file, examples too.
func isEnvNamed(f string) bool {
	base := strings.ToLower(path.Base(f))
	return !inPackages(f) && (base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env"))
}

var exampleWords = []string{"example", "sample", "template", "dist", "tmpl"}

// isExample reports whether a file is an example or template of another:
// .env.example, docker-compose.example.yml. They're listed hidden: someone
// may have put a real value in one.
func isExample(f string) bool {
	base := strings.ToLower(path.Base(f))
	for _, w := range exampleWords {
		if strings.HasSuffix(base, "."+w) || strings.Contains(base, "."+w+".") {
			return true
		}
	}
	return false
}

// plainValue reads a key from a plain env file.
func plainValue(file, name string) (provider.SecretValue, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return provider.SecretValue{}, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := envLine(line); ok && k == name {
			return provider.NewSecretValue([]byte(v)), nil
		}
	}
	return provider.SecretValue{}, fmt.Errorf("%s isn't in %s", name, filepath.Base(file))
}

// envLine reads KEY=value (or export KEY=value), unquoting the value and
// dropping a comment after an unquoted one.
func envLine(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")
	k, v, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	k, v = strings.TrimSpace(k), strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		if end := strings.LastIndexByte(v, v[0]); end > 0 {
			inner := v[1:end]
			if v[0] == '"' {
				inner = strings.NewReplacer(`\n`, "\n", `\"`, `"`, `\\`, `\`).Replace(inner)
			}
			return k, inner, true
		}
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return k, v, true
}

// sopsRule is a creation rule of a .sops.yaml, which applies to files
// under the directory it's in.
type sopsRule struct {
	dir string // relative to the repo root, "" for the root
	re  *regexp.Regexp
}

func (r sopsRule) matches(f string) bool {
	rel := f
	if r.dir != "" {
		var ok bool
		if rel, ok = strings.CutPrefix(f, r.dir+"/"); !ok {
			return false
		}
	}
	return r.re == nil || r.re.MatchString(rel)
}

func sopsRules(root string, files []string) []sopsRule {
	var rules []sopsRule
	for _, f := range files {
		if path.Base(f) != ".sops.yaml" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		var cfg struct {
			CreationRules []struct {
				PathRegex string `yaml:"path_regex"`
			} `yaml:"creation_rules"`
		}
		if yaml.Unmarshal(b, &cfg) != nil {
			continue
		}
		dir := path.Dir(f)
		if dir == "." {
			dir = ""
		}
		for _, cr := range cfg.CreationRules {
			r := sopsRule{dir: dir}
			if cr.PathRegex != "" {
				re, err := regexp.Compile(cr.PathRegex)
				if err != nil {
					continue
				}
				r.re = re
			}
			rules = append(rules, r)
		}
	}
	return rules
}

// globs compiles globs: * and ? within a path segment, ** across them.
func globs(patterns []string) ([]*regexp.Regexp, error) {
	var out []*regexp.Regexp
	for _, g := range patterns {
		var b strings.Builder
		b.WriteString("^")
		for i := 0; i < len(g); i++ {
			switch c := g[i]; {
			case strings.HasPrefix(g[i:], "**/"):
				b.WriteString("(.*/)?")
				i += 2
			case strings.HasPrefix(g[i:], "**"):
				b.WriteString(".*")
				i++
			case c == '*':
				b.WriteString("[^/]*")
			case c == '?':
				b.WriteString("[^/]")
			default:
				b.WriteString(regexp.QuoteMeta(string(c)))
			}
		}
		b.WriteString("$")
		re, err := regexp.Compile(b.String())
		if err != nil {
			return nil, fmt.Errorf("glob %q: %w", g, err)
		}
		out = append(out, re)
	}
	return out, nil
}

func anyMatch(res []*regexp.Regexp, f string) bool {
	return slices.ContainsFunc(res, func(re *regexp.Regexp) bool { return re.MatchString(f) })
}

// --- sops files ---

type sopsMeta struct {
	methods    []string // age, azure_kv, …
	recipients []string // "age age1…", "azure_kv https://kv…/keys/sops"
	modified   time.Time
}

// recipient names a key a file is encrypted for, from its sops metadata:
// public, never a secret.
func recipient(method string, f map[string]string) string {
	var v string
	switch method {
	case "age":
		v = f["recipient"]
	case "pgp":
		v = f["fp"]
	case "kms":
		v = f["arn"]
	case "gcp_kms":
		v = f["resource_id"]
	case "azure_kv":
		v = strings.TrimSuffix(f["vault_url"], "/") + "/keys/" + f["name"]
	case "hc_vault":
		v = strings.TrimSuffix(f["vault_address"], "/") + " " + f["engine_path"] + "/" + f["key_name"]
	}
	return method + " " + v
}

const maxFileSize = 4 << 20

// readMeta reads a file's sops metadata; ok is false if it isn't a sops
// file of a supported format.
func readMeta(file string) (sopsMeta, bool) {
	fi, err := os.Stat(file)
	if err != nil || fi.Size() > maxFileSize {
		return sopsMeta{}, false
	}
	if formatOf(file) == "" {
		return sopsMeta{}, false // not a format sops writes: skip reading
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return sopsMeta{}, false
	}
	return parseMeta(file, b)
}

// parseMeta is readMeta, of the file's contents b.
func parseMeta(file string, b []byte) (sopsMeta, bool) {
	format := formatOf(file)
	if len(b) > maxFileSize || format == "" || !bytes.Contains(b, []byte("sops")) {
		return sopsMeta{}, false // cheap: most files aren't sops, no parsing
	}
	var m sopsMeta
	known := []string{"age", "azure_kv", "kms", "gcp_kms", "hc_vault", "pgp"}
	switch format {
	case "yaml", "json":
		var doc struct {
			Sops map[string]any `yaml:"sops"`
		}
		if yaml.Unmarshal(b, &doc) != nil || doc.Sops == nil || doc.Sops["mac"] == nil {
			return m, false
		}
		for _, k := range known {
			if v, ok := doc.Sops[k].([]any); ok && len(v) > 0 {
				m.methods = append(m.methods, k)
				for _, e := range v {
					fields := map[string]string{}
					if em, ok := e.(map[string]any); ok {
						for fk, fv := range em {
							fields[fk] = fmt.Sprint(fv)
						}
					}
					m.recipients = append(m.recipients, recipient(k, fields))
				}
			}
		}
		if s, ok := doc.Sops["lastmodified"].(string); ok {
			m.modified, _ = time.Parse(time.RFC3339, s)
		}
		if t, ok := doc.Sops["lastmodified"].(time.Time); ok {
			m.modified = t
		}
	case "dotenv", "ini":
		text := string(b)
		if !strings.Contains(text, "sops_mac") && !strings.Contains(text, "sops_version") {
			return m, false
		}
		for _, k := range known {
			if strings.Contains(text, "sops_"+k+"__") {
				m.methods = append(m.methods, k)
				m.recipients = append(m.recipients, envRecipients(text, k)...)
			}
		}
		if i := strings.Index(text, "sops_lastmodified"); i >= 0 {
			line, _, _ := strings.Cut(text[i:], "\n")
			if _, v, ok := strings.Cut(line, "="); ok {
				m.modified, _ = time.Parse(time.RFC3339, strings.TrimSpace(v))
			}
		}
	default:
		return m, false
	}
	if len(m.methods) == 0 {
		m.methods = []string{"unknown keys"}
	}
	return m, true
}

// envRecipients are method's keys in a dotenv or ini sops file, whose
// metadata is flattened: sops_age__list_0__map_recipient=age1….
func envRecipients(text, method string) []string {
	prefix := "sops_" + method + "__list_"
	var order []string
	keys := map[string]map[string]string{}
	for line := range strings.Lines(text) {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		rest, found := strings.CutPrefix(strings.TrimSpace(k), prefix)
		if !ok || !found {
			continue
		}
		n, field, ok := strings.Cut(rest, "__map_")
		if !ok {
			continue
		}
		if keys[n] == nil {
			keys[n] = map[string]string{}
			order = append(order, n)
		}
		keys[n][field] = strings.TrimSpace(v)
	}
	out := make([]string, len(order))
	for i, n := range order {
		out[i] = recipient(method, keys[n])
	}
	return out
}

func formatOf(file string) string {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".env":
		return "dotenv"
	case ".ini":
		return "ini"
	}
	if strings.HasPrefix(filepath.Base(file), ".env") {
		return "dotenv"
	}
	return ""
}

type key struct {
	name      string // db.password, list[0], with . [ ] \ in keys escaped
	extract   string // sops --extract: ["db"]["password"]
	encrypted bool
	jasypt    bool // a plain env file's ENC(...) value
	line      int  // 1-based
}

// parseKeys lists a sops file's keys without decrypting anything.
func parseKeys(file string, b []byte) ([]key, error) {
	switch formatOf(file) {
	case "yaml", "json":
		var doc yaml.Node
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return nil, err
		}
		if len(doc.Content) == 0 {
			return nil, nil
		}
		var out []key
		walk(doc.Content[0], "", "", true, &out)
		return out, nil
	case "dotenv":
		var out []key
		for i, line := range strings.Split(string(b), "\n") {
			k, v, ok := envLine(line)
			if !ok || strings.HasPrefix(k, "sops_") {
				continue
			}
			sopsKey, _, _ := strings.Cut(line, "=") // sops keeps export and spaces in the key
			out = append(out, key{name: k, extract: quote(sopsKey), encrypted: strings.HasPrefix(v, "ENC["), jasypt: jasypt.Is(v), line: i + 1})
		}
		return out, nil
	case "ini":
		var out []key
		section := ""
		for i, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";"):
			case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
				section = strings.TrimSpace(line[1 : len(line)-1])
			case section != "sops":
				k, v, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				k, v = strings.TrimSpace(k), strings.TrimSpace(v)
				out = append(out, key{name: escape(section) + "." + escape(k), extract: quote(section) + quote(k), encrypted: strings.HasPrefix(v, "ENC["), line: i + 1})
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported format")
}

// walk collects the leaves of a YAML/JSON tree, skipping sops' own block.
func walk(n *yaml.Node, name, extract string, top bool, out *[]key) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			if top && k == "sops" {
				continue
			}
			child := escape(k)
			if name != "" {
				child = name + "." + child
			}
			walk(n.Content[i+1], child, extract+quote(k), false, out)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			walk(c, fmt.Sprintf("%s[%d]", name, i), fmt.Sprintf("%s[%d]", extract, i), false, out)
		}
	case yaml.AliasNode:
		if n.Alias != nil {
			walk(n.Alias, name, extract, false, out)
		}
	default:
		*out = append(*out, key{name: name, extract: extract, encrypted: strings.HasPrefix(n.Value, "ENC["), line: n.Line})
	}
}

// quote is a sops --extract path element.
func quote(k string) string {
	return `["` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(k) + `"]`
}

// escape keeps a key's dots and brackets from reading as nesting in a
// name: app\.name is one key, app.name two.
func escape(k string) string {
	return strings.NewReplacer(`\`, `\\`, `.`, `\.`, `[`, `\[`, `]`, `\]`).Replace(k)
}

// extractOf is the sops --extract path of the key named name, for objects
// made from a link.
func extractOf(file, name string) (string, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	keys, err := parseKeys(file, b)
	if err != nil {
		return "", err
	}
	for _, k := range keys {
		if k.name == name {
			return k.extract, nil
		}
	}
	return "", fmt.Errorf("%s isn't in %s", name, filepath.Base(file))
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}
