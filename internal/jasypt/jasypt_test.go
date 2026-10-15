package jasypt

import (
	"errors"
	"testing"
)

// Encrypted by Jasypt 1.9.3 itself, set up as jasypt-spring-boot's default
// encryptor, with the password "test-password".
var vectors = map[string]string{
	"ENC(5IQUKT4B7j38TnZJ+BlWqo/zSkKHTjl4XRJyaQEHgkCy4r00avLt4dWqFB74X0Zw)":                         "hunter2",
	"ENC(w4pm2JVZcbG5R+Ud9uKHg8VhJ/0aAK7sFXtNBv+Vo450SAchTQtfhw0TnIJ9gN+D0VFbwqJzfXU369coTJz2rw==)": "blåbærsyltetøy ✓",
}

func TestDecrypt(t *testing.T) {
	for enc, want := range vectors {
		if !Is(enc) {
			t.Errorf("%s: not Jasypt", enc)
		}
		if got, err := Decrypt(enc, "test-password"); err != nil || got != want {
			t.Errorf("%s: %q %v, want %q", enc, got, err, want)
		}
		if _, err := Decrypt(enc, "wrong"); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s with a wrong password: %v", enc, err)
		}
	}
	for _, v := range []string{"ENC(not base64!)", "ENC(aGVsbG8=)"} {
		if _, err := Decrypt(v, "test-password"); err == nil {
			t.Errorf("%s: no error", v)
		}
	}
	for _, v := range []string{"ENC()", "hunter2", "ENC[AES256_GCM,data:x]", "${DB_PASSWORD}"} {
		if Is(v) {
			t.Errorf("%s: Jasypt", v)
		}
	}
}
