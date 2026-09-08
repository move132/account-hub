package security

import (
	"strings"
	"testing"
)

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	if !VerifyPassword("correct-horse-battery-staple", hash) {
		t.Fatal("expected password to verify")
	}
	if VerifyPassword("wrong-password-value", hash) {
		t.Fatal("wrong password verified")
	}
}

func TestValidatePasswordRejectsWeakValues(t *testing.T) {
	for _, value := range []string{"short", "password1234", "admin123456"} {
		if err := ValidatePassword(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}

func TestRedactNestedCredentials(t *testing.T) {
	redacted := Redact(map[string]any{"data": map[string]any{"session_id": "secret", "balance": 42}, "items": []any{map[string]any{"access_token": "secret"}}}).(map[string]any)
	data := redacted["data"].(map[string]any)
	if data["session_id"] != "••••••••" || data["balance"] != 42 {
		t.Fatalf("unexpected redaction: %#v", redacted)
	}
	items := redacted["items"].([]any)
	if items[0].(map[string]any)["access_token"] != "••••••••" {
		t.Fatalf("nested list was not redacted: %#v", redacted)
	}
}
