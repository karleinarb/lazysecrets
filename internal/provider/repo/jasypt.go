package repo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"git.kebcloud.no/keb/lazysecrets/internal/jasypt"
	"git.kebcloud.no/keb/lazysecrets/internal/provider"
)

// jasyptEnv is where Spring looks for the password first, before the
// config's jasypt.encryptor.password, and so does lazysecrets.
const jasyptEnv = "JASYPT_ENCRYPTOR_PASSWORD"

// jasyptValue decrypts v when it's a Jasypt value, ENC(...), from a plain
// file; other values are as they are.
func jasyptValue(file, name string, v provider.SecretValue) (provider.SecretValue, error) {
	s := v.Reveal()
	if !jasypt.Is(s) {
		return v, nil
	}
	password, from, err := jasyptPassword(file, name)
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

// jasyptPassword is the password for the Jasypt value of the key named
// name, and where it's from: $JASYPT_ENCRYPTOR_PASSWORD, else the file's
// own setting (jasypt.encryptor.password in a Spring file; the service's
// JASYPT_ENCRYPTOR_PASSWORD in a compose file, the file's in an env file),
// with a ${VAR} in it read from the environment.
func jasyptPassword(file, name string) (password, from string, err error) {
	if v := os.Getenv(jasyptEnv); v != "" {
		return v, "$" + jasyptEnv, nil
	}
	setting, read, placeholder := jasyptEnv, plainValue, composePlaceholder // env files interpolate like compose
	if t, ok := configTypeOf(file); ok {
		if t.jasyptSetting == nil {
			return "", "", fmt.Errorf("jasypt: no password: set $%s", jasyptEnv)
		}
		setting, read, placeholder = t.jasyptSetting(name), t.value, t.placeholder
	}
	v, err := read(file, setting)
	if err != nil {
		return "", "", fmt.Errorf("jasypt: no password: set $%s, or %s in %s", jasyptEnv, setting, filepath.Base(file))
	}
	from = setting + " in " + filepath.Base(file)
	env, def, hasDef, ok := placeholder(v.Reveal())
	switch {
	case !ok:
		return v.Reveal(), from, nil
	case os.Getenv(env) != "":
		return os.Getenv(env), "$" + env + " (" + from + ")", nil
	case hasDef:
		return def, "the default in " + from, nil
	}
	return "", "", fmt.Errorf("jasypt: no password: %s is %s, and $%s isn't set", from, v.Reveal(), env)
}
