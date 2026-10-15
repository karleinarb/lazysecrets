package share

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Rules are what a key's value must be to share with it: random enough
// that guessing it is out of reach, since anyone with a link can try.
type Rules struct {
	MinLength  int // characters
	MinClasses int // kinds of character: lowercase, uppercase, digits, symbols
}

// DefaultRules take a random base64 or hex value (openssl rand -base64
// 32, or -hex 32) and refuse passwords made up to be remembered.
var DefaultRules = Rules{MinLength: 32, MinClasses: 2}

// Check reports what key lacks, without showing it.
func (r Rules) Check(key []byte) error {
	r = r.withDefaults()
	s := string(key)
	var lower, upper, digit, symbol bool
	for _, c := range s {
		switch {
		case unicode.IsLower(c):
			lower = true
		case unicode.IsUpper(c):
			upper = true
		case unicode.IsDigit(c):
			digit = true
		default:
			symbol = true
		}
	}
	kinds := 0
	for _, has := range []bool{lower, upper, digit, symbol} {
		if has {
			kinds++
		}
	}
	n := utf8.RuneCountInString(s)
	var has, needs []string
	if n < r.MinLength {
		has = append(has, fmt.Sprintf("%d characters", n))
		needs = append(needs, fmt.Sprintf("%d (minLength)", r.MinLength))
	}
	if kinds < r.MinClasses {
		has = append(has, fmt.Sprintf("%d %s of character", kinds, map[bool]string{true: "kind", false: "kinds"}[kinds == 1]))
		needs = append(needs, fmt.Sprintf("%d of lowercase, uppercase, digits and symbols (minClasses)", r.MinClasses))
	}
	if len(has) == 0 {
		return nil
	}
	return fmt.Errorf("its value has %s; sharing.keyRules needs %s",
		strings.Join(has, " and "), strings.Join(needs, " and "))
}

func (r Rules) withDefaults() Rules {
	if r.MinLength == 0 {
		r.MinLength = DefaultRules.MinLength
	}
	if r.MinClasses == 0 {
		r.MinClasses = DefaultRules.MinClasses
	}
	r.MinLength = max(r.MinLength, MinKey)
	return r
}
