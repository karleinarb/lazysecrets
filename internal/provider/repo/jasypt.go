package repo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"git.kebcloud.no/keb/lazysecrets/internal/jasypt"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// jasyptEnv is where Spring looks for the password first, before the
// config's jasypt.encryptor.password, and so does lazysecrets.
const jasyptEnv = "JASYPT_ENCRYPTOR_PASSWORD"

// jasyptValue decrypts v when it's a Jasypt value, ENC(...), from a plain
// file; other values are as they are.
func jasyptValue(file string, v provider.SecretValue) (provider.SecretValue, error) {
	s := v.Reveal()
	if !jasypt.Is(s) {
		return v, nil
	}
	password, from, err := jasyptPassword(file)
	if err != nil {
		return provider.SecretValue{}, err
	}
	plain, err := jasypt.Decrypt(s, password)
	if errors.Is(err, jasypt.ErrDecrypt) {
		return provider.SecretValue{}, fmt.Errorf("jasypt: with the password from %s: %w", from, err)
	}
	if err != nil {
		return provider.SecretValue{}, fmt.Errorf("jasypt: %w", err)
	}
	return provider.NewSecretValue([]byte(plain)), nil
}

// jasyptPassword is the password for a file's Jasypt values, and where it's
// from: $JASYPT_ENCRYPTOR_PASSWORD, else the file's own setting
// (jasypt.encryptor.password in a Spring file, JASYPT_ENCRYPTOR_PASSWORD
// in an env file), with a ${VAR} in it read from the environment.
func jasyptPassword(file string) (password, from string, err error) {
	if v := os.Getenv(jasyptEnv); v != "" {
		return v, "$" + jasyptEnv, nil
	}
	setting, read := jasyptEnv, plainValue
	if IsSpring(file) {
		setting, read = "jasypt.encryptor.password", springValue
	}
	v, err := read(file, setting)
	if err != nil {
		return "", "", fmt.Errorf("jasypt: no password: set $%s, or %s in %s", jasyptEnv, setting, filepath.Base(file))
	}
	return resolvePlaceholder(v.Reveal(), setting+" in "+filepath.Base(file))
}

// placeholder is Spring's ${VAR} or ${VAR:default}.
var placeholder = regexp.MustCompile(`^\$\{([^:}]+)(:[^}]*)?\}$`)

// resolvePlaceholder reads a ${VAR} value from the environment, falling
// back to its default; other values are as they are.
func resolvePlaceholder(v, from string) (string, string, error) {
	m := placeholder.FindStringSubmatch(v)
	switch {
	case m == nil:
		return v, from, nil
	case os.Getenv(m[1]) != "":
		return os.Getenv(m[1]), "$" + m[1] + " (" + from + ")", nil
	case m[2] != "":
		return m[2][1:], "the default in " + from, nil
	}
	return "", "", fmt.Errorf("jasypt: no password: %s is %s, and $%s isn't set", from, v, m[1])
}
