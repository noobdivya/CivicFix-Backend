// Package validate holds input checks shared by the HTTP handlers.
package validate

import (
	"regexp"
	"strings"
)

var (
	nonDigits    = regexp.MustCompile(`[\s\-()]`)
	indianMobile = regexp.MustCompile(`^(?:\+?91|0)?([6-9]\d{9})$`)
)

// IndianMobile normalises an Indian mobile number ("+91 98765-43210",
// "09876543210", "9876543210") to its 10 digits. ok is false if invalid.
func IndianMobile(s string) (digits string, ok bool) {
	m := indianMobile.FindStringSubmatch(nonDigits.ReplaceAllString(strings.TrimSpace(s), ""))
	if m == nil {
		return "", false
	}
	return m[1], true
}
