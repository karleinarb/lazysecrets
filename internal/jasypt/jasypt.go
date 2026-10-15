// Package jasypt decrypts values encrypted with Jasypt the way
// jasypt-spring-boot 3.0 and later does by default: ENC(...) around the
// base64 of a random salt, a random IV and the ciphertext, with
// PBEWITHHMACSHA512ANDAES_256 (PBKDF2-HMAC-SHA512, 1000 iterations,
// AES-256-CBC).
package jasypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf8"
)

const iterations = 1000

// ErrDecrypt is a value that doesn't decrypt with the password: a wrong
// password, or one encrypted with another algorithm.
var ErrDecrypt = errors.New("doesn't decrypt: a wrong password, or not Jasypt's default algorithm (PBEWITHHMACSHA512ANDAES_256)")

// Is reports whether v is a Jasypt value, ENC(...).
func Is(v string) bool {
	return len(v) > len("ENC()") && strings.HasPrefix(v, "ENC(") && strings.HasSuffix(v, ")")
}

// Decrypt decrypts an ENC(...) value.
func Decrypt(v, password string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(v, "ENC("), ")"))
	if err != nil {
		return "", errors.New("not Jasypt: ENC(...) isn't base64")
	}
	const size = aes.BlockSize // the salt's and the IV's too
	if len(b) < 3*size || len(b)%size != 0 {
		return "", ErrDecrypt
	}
	salt, iv, data := b[:size], b[size:2*size], b[2*size:]
	key, err := pbkdf2.Key(sha512.New, password, salt, iterations, 32)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, data)
	// A wrong password decrypts to noise: its padding is almost never
	// valid, and the rest rarely UTF-8.
	n := int(plain[len(plain)-1])
	if n == 0 || n > size {
		return "", ErrDecrypt
	}
	for _, c := range plain[len(plain)-n:] {
		if int(c) != n {
			return "", ErrDecrypt
		}
	}
	plain = plain[:len(plain)-n]
	if !utf8.Valid(plain) {
		return "", ErrDecrypt
	}
	return string(plain), nil
}
