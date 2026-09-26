package validate

import "testing"

func TestIndianMobile(t *testing.T) {
	cases := map[string]string{
		"9876543210":      "9876543210",
		"+91 98765 43210": "9876543210",
		"+91-98765-43210": "9876543210",
		"09876543210":     "9876543210",
		"919876543210":    "9876543210",
		"5876543210":      "", // must start with 6-9
		"98765":           "",
		"98765432101":     "",
		"abcdefghij":      "",
	}
	for in, want := range cases {
		got, ok := IndianMobile(in)
		if (want == "") == ok || got != want {
			t.Errorf("IndianMobile(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}
