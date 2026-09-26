package handlers

import (
	"strings"
	"testing"
	"time"
)

func TestNormaliseCode(t *testing.T) {
	for in, want := range map[string]string{
		"CF-7KQ2M9XA":  "CF-7KQ2M9XA",
		"cf-7kq2m9xa":  "CF-7KQ2M9XA",
		" 7KQ2M9XA ":   "CF-7KQ2M9XA",
		"CF 7KQ2 M9XA": "CF-7KQ2M9XA",
		"cf7kq2m9xa":   "CF-7KQ2M9XA",
	} {
		got := normaliseCode(in)
		if got != want || !trackingCodeRe.MatchString(got) {
			t.Errorf("normaliseCode(%q) = %q, want %q", in, got, want)
		}
	}
	if trackingCodeRe.MatchString(normaliseCode("CF-123")) {
		t.Error("short code should not match")
	}
}

func TestTrackingCodeFormat(t *testing.T) {
	for i := 0; i < 200; i++ {
		c, err := trackingCode()
		if err != nil {
			t.Fatal(err)
		}
		if !trackingCodeRe.MatchString(c) || strings.ContainsAny(c[3:], "01IO") {
			t.Fatalf("bad tracking code %q", c)
		}
	}
}

func TestSLA(t *testing.T) {
	want := map[string]time.Duration{
		"critical": 24 * time.Hour,
		"high":     72 * time.Hour,
		"medium":   7 * 24 * time.Hour,
		"low":      14 * 24 * time.Hour,
	}
	for p, d := range want {
		if got := slaFor(p); got != d {
			t.Errorf("slaFor(%q) = %v, want %v", p, got, d)
		}
	}
}

func TestMakeTitle(t *testing.T) {
	if got := makeTitle("Short title\nMore details on the next line"); got != "Short title" {
		t.Errorf("first line not used: %q", got)
	}
	long := strings.Repeat("word ", 40)
	got := makeTitle(long)
	if len([]rune(got)) > 81 || !strings.HasSuffix(got, "…") {
		t.Errorf("long title not shortened on a word boundary: %q", got)
	}
}
