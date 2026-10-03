package flow

import (
	"errors"
	"regexp"
	"strings"

	"github.com/shopspring/decimal"
)

var numberPattern = regexp.MustCompile(`^[+-]?[0-9]+(\.[0-9]+)?$`)

// ParseNumber bounds work before parsing exact decimals. Exponents and grouping
// separators are deliberately excluded; a keyboard's digits never change value.
func ParseNumber(raw string) (decimal.Decimal, error) {
	value := NormalizeDigits(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "٫", ".")
	if len(value) > 200 || !numberPattern.MatchString(value) {
		return decimal.Decimal{}, errors.New("invalid number")
	}
	return decimal.NewFromString(value)
}

func NormalizeDigits(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= '۰' && r <= '۹' {
			return '0' + r - '۰'
		}
		if r >= '٠' && r <= '٩' {
			return '0' + r - '٠'
		}
		return r
	}, value)
}

func validNumberRules(r *NumberRules) bool {
	if r == nil {
		return true
	}
	var min, max decimal.Decimal
	var err error
	if r.Min != "" {
		min, err = ParseNumber(r.Min)
		if err != nil {
			return false
		}
	}
	if r.Max != "" {
		max, err = ParseNumber(r.Max)
		if err != nil {
			return false
		}
	}
	return r.Min == "" || r.Max == "" || min.LessThanOrEqual(max)
}
