// Package share seals env files into a lazysecrets://share link, so they
// can be passed on in chat or a ticket. They're encrypted with the value of
// a secret (in Key Vault, Databricks or a repo's SOPS file): whoever can
// read that secret can open the link, nobody else.
//
// A link says in the clear which secret it's encrypted with, the repo the
// files are from and how many there are, which is what's needed to open
// it. The files' paths and contents are encrypted, with age
// (https://age-encryption.org), the secret's value as its passphrase.
//
// The link's payload is unpadded base64url of:
//
//	version (1) | uvarint length of the header | header (JSON) | age file
//
// The age file holds the files as deflated JSON.
//
// A change to the format gets the next version, and Read keeps reading
// every earlier one: links stay in chat and tickets for years. A
// version it doesn't know is from a newer lazysecrets.
package share

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"filippo.io/age"

	"git.kebcloud.no/keb/lazysecrets/internal/provider"
	"git.kebcloud.no/keb/lazysecrets/internal/provider/repo"
	"git.kebcloud.no/keb/lazysecrets/internal/ref"
)

const (
	// version is the format's, written first; see the package doc.
	version = 1
	// MaxLink keeps a link short enough for chat and tickets (Teams takes
	// about 28 KB) and for passing it to a terminal window.
	MaxLink = 16 << 10
	// MinKey is the shortest key the rules can ask for.
	MinKey = 16
	// maxUnpacked caps the bytes opening a link unpacks.
	maxUnpacked = 1 << 20
)

// workFactor is the scrypt work factor for sealing: 2^15, about 40 ms
// each way, instead of age's 2^18 (a third of a second). scrypt makes
// guessing a weak passphrase slow; a key keeping to the rules is random
// and long enough not to need that. maxWorkFactor is the most opening
// accepts, so a link can't make it work for minutes.
var workFactor, maxWorkFactor = 15, 20

// File is a shared file.
type File struct {
	Path string // relative to the repo's root, with slashes
	Data []byte
}

// Header is what a link says in the clear.
type Header struct {
	// Key is the secret whose value the files are encrypted with.
	Key ref.Ref
	// Repo is the repo the files are from: its remote without scheme or
	// .git (a repo link's host).
	Repo string
	// Files is how many files the link holds.
	Files int
}

// Sealed is a share link read, its files still encrypted.
type Sealed struct {
	Header
	box []byte
}

type header struct {
	Key   string `json:"key"`
	Repo  string `json:"repo,omitempty"`
	Files int    `json:"files"`
}

type file struct {
	Path string `json:"path"`
	Data string `json:"data"`
}

// Seal encrypts files with key, the value of h.Key, into a share link,
// if key keeps to rules (*KeyError if not).
func Seal(h Header, key []byte, files []File, rules Rules) (ref.Ref, error) {
	if _, err := ParseKey(h.Key.String()); err != nil {
		return ref.Ref{}, fmt.Errorf("the key: %w", err)
	}
	if err := rules.Check(key); err != nil {
		return ref.Ref{}, &KeyError{Name: h.Key.Name, Err: err}
	}
	if len(files) == 0 {
		return ref.Ref{}, errors.New("no files to share")
	}
	plain := make([]file, len(files))
	for i, f := range files {
		if err := CheckPath(f.Path); err != nil {
			return ref.Ref{}, err
		}
		if !utf8.Valid(f.Data) {
			return ref.Ref{}, fmt.Errorf("%s isn't text", f.Path)
		}
		plain[i] = file{Path: f.Path, Data: string(f.Data)}
	}
	b, err := json.Marshal(plain)
	if err != nil {
		return ref.Ref{}, err
	}

	rcpt, err := age.NewScryptRecipient(string(key))
	if err != nil {
		return ref.Ref{}, err
	}
	rcpt.SetWorkFactor(workFactor)
	var box bytes.Buffer
	enc, err := age.Encrypt(&box, rcpt)
	if err != nil {
		return ref.Ref{}, err
	}
	z, _ := flate.NewWriter(enc, flate.BestCompression)
	if _, err := z.Write(b); err != nil {
		return ref.Ref{}, err
	}
	if err := z.Close(); err != nil {
		return ref.Ref{}, err
	}
	if err := enc.Close(); err != nil {
		return ref.Ref{}, err
	}

	hb, err := json.Marshal(header{Key: h.Key.String(), Repo: h.Repo, Files: len(files)})
	if err != nil {
		return ref.Ref{}, err
	}
	payload := binary.AppendUvarint([]byte{version}, uint64(len(hb)))
	payload = append(append(payload, hb...), box.Bytes()...)
	r := ref.Ref{Share: base64.RawURLEncoding.EncodeToString(payload)}
	if n := len(r.String()); n > MaxLink {
		return ref.Ref{}, fmt.Errorf("the link would be %d KB, more than the %d KB it can be: share fewer files", n>>10, MaxLink>>10)
	}
	return r, nil
}

// KeyError is a key that can't be used: it can't be read, or doesn't
// keep to the rules.
type KeyError struct {
	Name string // the secret's
	Err  error
}

func (e *KeyError) Error() string { return "can't use " + e.Name + ": " + e.Err.Error() }
func (e *KeyError) Unwrap() error { return e.Err }

// Read reads a share link's header.
func Read(r ref.Ref) (Sealed, error) {
	bad := errors.New("share link is cut short or garbled: copy all of it")
	b, err := base64.RawURLEncoding.DecodeString(r.Share)
	if err != nil || len(b) < 2 {
		return Sealed{}, bad
	}
	if b[0] != version {
		return Sealed{}, errors.New("share link is from a newer lazysecrets: update it (lazysecrets update)")
	}
	n, size := binary.Uvarint(b[1:])
	if size <= 0 || n > uint64(len(b)) {
		return Sealed{}, bad
	}
	rest := b[1+size:]
	if n > uint64(len(rest)) {
		return Sealed{}, bad
	}
	var h header
	if err := json.Unmarshal(rest[:n], &h); err != nil {
		return Sealed{}, bad
	}
	key, err := ParseKey(h.Key)
	if err != nil {
		return Sealed{}, fmt.Errorf("share link's key: %w", err)
	}
	return Sealed{Header: Header{Key: key, Repo: h.Repo, Files: h.Files}, box: rest[n:]}, nil
}

// Open decrypts the files with key, the value of s.Key.
func (s Sealed) Open(key []byte) ([]File, error) {
	id, err := age.NewScryptIdentity(string(key))
	if err != nil {
		return nil, err
	}
	id.SetMaxWorkFactor(maxWorkFactor)
	dec, err := age.Decrypt(bytes.NewReader(s.box), id)
	if err != nil {
		var wrong *age.NoIdentityMatchError
		if errors.As(err, &wrong) {
			return nil, s.wrongKey()
		}
		return nil, fmt.Errorf("can't decrypt the link: %w", err)
	}
	b, err := io.ReadAll(io.LimitReader(flate.NewReader(dec), maxUnpacked+1))
	if err != nil {
		return nil, fmt.Errorf("can't decrypt the link: %w", err)
	}
	if len(b) > maxUnpacked {
		return nil, errors.New("the link holds more than lazysecrets unpacks")
	}
	var plain []file
	if err := json.Unmarshal(b, &plain); err != nil {
		return nil, fmt.Errorf("can't read the link's files: %w", err)
	}
	out := make([]File, len(plain))
	for i, f := range plain {
		if err := CheckPath(f.Path); err != nil {
			return nil, err
		}
		out[i] = File{Path: f.Path, Data: []byte(f.Data)}
	}
	return out, nil
}

// wrongKey says why the key, read fine, doesn't decrypt the link: its
// value isn't the one the link was made with.
func (s Sealed) wrongKey() error {
	name := s.Key.Name
	switch {
	case s.Key.Kind == provider.KindAzure && s.Key.Version != "":
		// A version's value never changes.
		return fmt.Errorf("%s at version %s doesn't decrypt it: the link may be damaged", name, s.Key.Version)
	case s.Key.Kind == provider.KindAzure:
		return fmt.Errorf("%s's current version isn't the one the link was made with, so it can't decrypt it: ask for a new link", name)
	}
	// Databricks secrets and SOPS values are changed in place.
	return fmt.Errorf("%s has changed since the link was made, so it can't decrypt it: ask for a new link", name)
}

// CheckPath accepts a path relative to a repo's root, with slashes, of an
// env file (.env, .env.local, prod.env) outside .git: what a link may
// write.
func CheckPath(p string) error {
	switch {
	case p == "" || path.IsAbs(p) || strings.ContainsAny(p, "\\\x00") || path.Clean(p) != p ||
		p == ".." || strings.HasPrefix(p, "../"):
		return fmt.Errorf("%q isn't a path inside a repo", p)
	case p == ".git" || strings.HasPrefix(p, ".git/") || strings.Contains(p, "/.git/"):
		return fmt.Errorf("%q is inside .git", p)
	case !repo.IsEnv(p):
		return fmt.Errorf("%q isn't an env file: only those can be shared", p)
	}
	return nil
}

// DefaultKey is the key for a repo's files: sharing.key in the repo's
// .lazysecrets.yaml, else general (sharing.key in the config). ok is false
// when neither names one.
func DefaultKey(root, general string) (key ref.Ref, ok bool, err error) {
	link, err := repo.ShareKey(root)
	if err != nil {
		return ref.Ref{}, false, err
	}
	where := root + "/.lazysecrets.yaml"
	if link == "" {
		link, where = general, "the config"
	}
	if link == "" {
		return ref.Ref{}, false, nil
	}
	key, err = ParseKey(link)
	if err != nil {
		return ref.Ref{}, false, fmt.Errorf("sharing.key in %s: %w", where, err)
	}
	return key, true, nil
}

// ParseKey reads which secret is the key: its lazysecrets:// link, Key
// Vault URL or @Microsoft.KeyVault(...) reference.
func ParseKey(s string) (ref.Ref, error) {
	key, err := ref.Parse(s)
	if err != nil {
		return ref.Ref{}, err
	}
	switch {
	case key.IsShare() || key.IsSource() || key.IsContainer() || (key.Type != "" && key.Type != provider.TypeSecret):
		return ref.Ref{}, fmt.Errorf("%s isn't a secret", s)
	case key.Kind == provider.KindDatabricks && key.Host == "":
		return ref.Ref{}, fmt.Errorf("%s names no workspace: use its lazysecrets:// link", s)
	}
	return key, nil
}

// Pin is key at version, the version its value was read at, for Azure,
// whose old versions stay readable: a link keeps working when the key
// is rotated, until that version is disabled.
func Pin(key ref.Ref, version string) ref.Ref {
	if key.Kind == provider.KindAzure && key.Version == "" {
		key.Version = version
	}
	return key
}
