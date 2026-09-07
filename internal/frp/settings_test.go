package frp

import "testing"

func TestFieldValidate_accepts(t *testing.T) {
	cases := map[Field][]string{
		FieldServerAddr:    {"203.0.113.10", "vps-ru.example.com", "a-b.c_d.example"},
		FieldServerPort:    {"1", "12243", "65535"},
		FieldToken:         {"abcd1234", "A1+/=_.-b2c3d4e5", "0123456789abcdef0123456789abcdef"},
		FieldSSHRemotePort: {"4640", "4641", "4643"},
		FieldLuciDomain:    {"vps-ru.example.com", "a.b.c.d"},
		FieldLuciUser:      {"admin", "luci_user-1"},
		FieldLuciPassword:  {"correct horse", "S0me-Long_Pass!"},
	}
	for f, vals := range cases {
		for _, v := range vals {
			if got, err := f.Validate(v); err != nil {
				t.Errorf("%s.Validate(%q) unexpected error: %v", f, v, err)
			} else if got == "" {
				t.Errorf("%s.Validate(%q) returned empty", f, v)
			}
		}
	}
}

func TestFieldValidate_rejectsInjection(t *testing.T) {
	// Every value ends up inside a TOML string in frpc.toml. None of these may
	// pass: a newline or a quote would let the input open its own table.
	bad := map[Field][]string{
		FieldServerAddr: {
			"", "1.2.3.4\n[[proxies]]", "1.2.3.4\"", "1.2.3.4 x", "host;reboot",
			"2001:db8::1", "-lead.example", "trail.example.", "a..b",
		},
		FieldServerPort:    {"", "0", "70000", "12243x", "-1", "80 90"},
		FieldToken:         {"", "short", "tok en", "tok\"en", "tok\nen", "tok]en", "café-tok-123"},
		FieldSSHRemotePort: {"", "22", "4639", "4644", "8080"},
		FieldLuciDomain: {
			"", "ex\"ample.com", "no-dot", "a_b.example.com", "dom\nain.example.com",
			"] \n [[proxies]] \n name=\"x\"", "-x.example.com", "x-.example.com",
		},
		FieldLuciUser:     {"", "user name", "user\"", "user\nname", "юзер", string(make([]byte, 65))},
		FieldLuciPassword: {"", "short", "has\"quote123", "back\\slash123", "with\nnewline123", "таб\tтут1234"},
	}
	for f, vals := range bad {
		for _, v := range vals {
			if got, err := f.Validate(v); err == nil {
				t.Errorf("%s.Validate(%q) should have failed, got %q", f, v, got)
			}
		}
	}
}

func TestFieldValidate_normalises(t *testing.T) {
	if got, _ := FieldLuciDomain.Validate("  VPS-RU.Example.COM  "); got != "vps-ru.example.com" {
		t.Errorf("domain normalisation = %q", got)
	}
	if got, _ := FieldServerPort.Validate(" 12243 "); got != "12243" {
		t.Errorf("port trim = %q", got)
	}
}
