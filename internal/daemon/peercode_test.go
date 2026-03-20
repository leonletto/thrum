package daemon

import (
	"testing"
)

func TestFormatConnectionString(t *testing.T) {
	got := FormatConnectionString("leonsmacmini", "100.65.66.84", 9147, "1285")
	want := "leonsmacmini:100.65.66.84:9147:1285"
	if got != want {
		t.Errorf("FormatConnectionString = %q, want %q", got, want)
	}
}

func TestParseConnectionString(t *testing.T) {
	name, ip, port, code, err := ParseConnectionString("leonsmacmini:100.65.66.84:9147:1285")
	if err != nil {
		t.Fatalf("ParseConnectionString: %v", err)
	}
	if name != "leonsmacmini" {
		t.Errorf("name = %q, want %q", name, "leonsmacmini")
	}
	if ip != "100.65.66.84" {
		t.Errorf("ip = %q, want %q", ip, "100.65.66.84")
	}
	if port != 9147 {
		t.Errorf("port = %d, want 9147", port)
	}
	if code != "1285" {
		t.Errorf("code = %q, want %q", code, "1285")
	}
}

func TestParseConnectionString_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"too few fields", "bad"},
		{"three fields", "a:b:c"},
		{"non-numeric port", "a:b:xyz:d"},
		{"empty string", ""},
		{"ipv6 address", "host:fd7a:115c:a1e0::1:9150:1234"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, _, err := ParseConnectionString(tt.input)
			if err == nil {
				t.Errorf("ParseConnectionString(%q) should error", tt.input)
			}
		})
	}
}

func TestParseConnectionString_Roundtrip(t *testing.T) {
	original := FormatConnectionString("myhost", "100.64.0.1", 9155, "4321")
	name, ip, port, code, err := ParseConnectionString(original)
	if err != nil {
		t.Fatalf("roundtrip parse failed: %v", err)
	}
	if name != "myhost" || ip != "100.64.0.1" || port != 9155 || code != "4321" {
		t.Errorf("roundtrip mismatch: got %q %q %d %q", name, ip, port, code)
	}
}
