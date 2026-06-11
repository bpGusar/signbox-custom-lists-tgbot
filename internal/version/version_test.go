package version

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"v0.20260611.12", "0.20260611.12"},
		{"0.20260611.12-r1", "0.20260611.12"},
		{" 0.20260611.10 ", "0.20260611.10"},
	}
	for _, tc := range tests {
		if got := Normalize(tc.in); got != tc.want {
			t.Fatalf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.20260611.10", "0.20260611.12", -1},
		{"0.20260611.12", "0.20260611.12-r1", 0},
		{"0.20260611.13", "0.20260611.12", 1},
		{"0.20260610.99", "0.20260611.1", -1},
	}
	for _, tc := range tests {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Fatalf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
