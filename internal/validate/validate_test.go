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

func TestAadhaar(t *testing.T) {
	// 234123412346 is a well-known valid test number (Verhoeff-correct).
	valid := []string{"234123412346", "2341 2341 2346", "2341-2341-2346"}
	for _, in := range valid {
		if _, ok := Aadhaar(in); !ok {
			t.Errorf("Aadhaar(%q) should be valid", in)
		}
	}
	invalid := []string{
		"234123412345", // bad checksum
		"123412341234", // starts with 1
		"034123412346", // starts with 0
		"23412341234",  // 11 digits
		"23412341234a",
	}
	for _, in := range invalid {
		if _, ok := Aadhaar(in); ok {
			t.Errorf("Aadhaar(%q) should be invalid", in)
		}
	}
}
