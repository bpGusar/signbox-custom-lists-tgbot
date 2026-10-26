package config

import (
	"reflect"
	"testing"
)

func TestParseUCIValues(t *testing.T) {
	cases := map[string][]string{
		`'/etc/init.d/podkop restart'`:             {"/etc/init.d/podkop restart"},
		`'/etc/init.d/forkop restart' 'echo done'`: {"/etc/init.d/forkop restart", "echo done"},
		`'echo '\''quoted'\''' 'b'`:                {"echo 'quoted'", "b"},
		`'` + DefaultRestartCmd + `'`:              {DefaultRestartCmd},
		``:                                         nil,
	}
	for in, want := range cases {
		if got := parseUCIValues(in); !reflect.DeepEqual(got, want) {
			t.Errorf("parseUCIValues(%q) = %q, want %q", in, got, want)
		}
	}
}
