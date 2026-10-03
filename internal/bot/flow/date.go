package flow

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	jalaali "github.com/jalaali/go-jalaali"
)

var datePattern = regexp.MustCompile(`^([0-9]{4})([/\-])([0-9]{1,2})([/\-])([0-9]{1,2})$`)

// ParseDate returns a canonical Jalali calendar date, never an instant. The
// Tehran calendar convention therefore needs no UTC or timezone conversion.
// go-jalaali uses Borkowski's calendar algorithm, including Esfand leap days.
func ParseDate(raw string) (string, error) {
	parts := datePattern.FindStringSubmatch(NormalizeDigits(strings.TrimSpace(raw)))
	if parts == nil || parts[2] != parts[4] {
		return "", errors.New("invalid Jalali date")
	}
	year, _ := strconv.Atoi(parts[1])
	month, _ := strconv.Atoi(parts[3])
	day, _ := strconv.Atoi(parts[5])
	if year < 1 || !jalaali.IsValidDate(year, month, day) {
		return "", errors.New("invalid Jalali date")
	}
	return fmt.Sprintf("%04d/%02d/%02d", year, month, day), nil
}

func validDateRules(r *DateRules) bool {
	if r == nil {
		return true
	}
	min, max := "", ""
	var err error
	if r.Min != "" {
		min, err = ParseDate(r.Min)
		if err != nil {
			return false
		}
	}
	if r.Max != "" {
		max, err = ParseDate(r.Max)
		if err != nil {
			return false
		}
	}
	return min == "" || max == "" || min <= max
}
