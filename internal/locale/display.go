package locale

import (
	"strings"
)

// Digits changes only display text; identifiers and user-entered names stay intact.
func Digits(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return '۰' + r - '0'
		}
		return r
	}, value)
}
