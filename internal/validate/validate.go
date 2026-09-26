// Package validate holds input checks shared by the HTTP handlers.
package validate

import (
	"regexp"
	"strings"
)

var (
	nonDigits    = regexp.MustCompile(`[\s\-()]`)
	indianMobile = regexp.MustCompile(`^(?:\+?91|0)?([6-9]\d{9})$`)
	twelveDigits = regexp.MustCompile(`^\d{12}$`)
	verhoeffD    = [10][10]int{{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, {1, 2, 3, 4, 0, 6, 7, 8, 9, 5}, {2, 3, 4, 0, 1, 7, 8, 9, 5, 6}, {3, 4, 0, 1, 2, 8, 9, 5, 6, 7}, {4, 0, 1, 2, 3, 9, 5, 6, 7, 8}, {5, 9, 8, 7, 6, 0, 4, 3, 2, 1}, {6, 5, 9, 8, 7, 1, 0, 4, 3, 2}, {7, 6, 5, 9, 8, 2, 1, 0, 4, 3}, {8, 7, 6, 5, 9, 3, 2, 1, 0, 4}, {9, 8, 7, 6, 5, 4, 3, 2, 1, 0}}
	verhoeffP    = [8][10]int{{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, {1, 5, 7, 6, 2, 8, 3, 0, 9, 4}, {5, 8, 0, 3, 7, 9, 6, 1, 4, 2}, {8, 9, 1, 6, 0, 4, 3, 5, 2, 7}, {9, 4, 5, 3, 1, 2, 6, 8, 7, 0}, {4, 2, 8, 6, 5, 7, 3, 9, 0, 1}, {2, 7, 9, 3, 8, 0, 6, 4, 1, 5}, {7, 0, 4, 6, 9, 1, 3, 2, 5, 8}}
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

// Aadhaar normalises an Aadhaar number (spaces/dashes removed) and checks
// its format: 12 digits, not starting with 0 or 1, valid Verhoeff checksum.
// This only checks the number is well-formed; it does NOT prove the number
// belongs to the person (that needs UIDAI authentication).
func Aadhaar(s string) (digits string, ok bool) {
	d := nonDigits.ReplaceAllString(strings.TrimSpace(s), "")
	if !twelveDigits.MatchString(d) || d[0] == '0' || d[0] == '1' {
		return "", false
	}
	c := 0
	for i := 0; i < len(d); i++ {
		digit := int(d[len(d)-1-i] - '0')
		c = verhoeffD[c][verhoeffP[i%8][digit]]
	}
	return d, c == 0
}
