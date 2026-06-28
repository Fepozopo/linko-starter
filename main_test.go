package main

import (
	"log/slog"
	"testing"
)

func Test_replaceAttrRedactsSensitiveKeys(t *testing.T) {
	tests := []struct {
		name string
		attr slog.Attr
		want string
	}{
		{name: "password", attr: slog.String("password", "hunter2"), want: "[REDACTED]"},
		{name: "key", attr: slog.String("key", "abc123"), want: "[REDACTED]"},
		{name: "apikey", attr: slog.String("apikey", "secret-value"), want: "[REDACTED]"},
		{name: "secret", attr: slog.String("secret", "shh"), want: "[REDACTED]"},
		{name: "pin", attr: slog.String("pin", "1234"), want: "[REDACTED]"},
		{name: "credit card number", attr: slog.String("creditcardno", "4111111111111111"), want: "[REDACTED]"},
		{name: "user", attr: slog.String("user", "frodo"), want: "[REDACTED]"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := replaceAttr(nil, tc.attr)
			if got.Value.String() != tc.want {
				t.Fatalf("unexpected redacted value: got %q, want %q", got.Value.String(), tc.want)
			}
		})
	}
}

func Test_replaceAttrRedactsURLPasswords(t *testing.T) {
	attr := slog.String("service_url", "https://alice:supersecret@example.com/api/v1?foo=bar")

	got := replaceAttr(nil, attr)
	want := "https://alice:[REDACTED]@example.com/api/v1?foo=bar"

	if got.Value.String() != want {
		t.Fatalf("unexpected redacted URL: got %q, want %q", got.Value.String(), want)
	}
}

func Test_replaceAttrLeavesNonSensitiveStringAlone(t *testing.T) {
	attr := slog.String("service_url", "https://example.com/api/v1?foo=bar")

	got := replaceAttr(nil, attr)
	if got.Value.String() != attr.Value.String() {
		t.Fatalf("unexpected value change: got %q, want %q", got.Value.String(), attr.Value.String())
	}
}
