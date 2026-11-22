package aimusic

import (
	"errors"
	"strings"
	"testing"
)

func TestFriendlyFailure(t *testing.T) {
	raw := `POST v2/users/8c214229/songs/prompt failed with HTTP 402 (INSUFFICIENT_CREDITS): {"error_code":"INSUFFICIENT_CREDITS","message":"Insufficient credits"}
{"error_code": "INSUFFICIENT_CREDITS"}`

	got := friendlyFailure(errors.New("exit status 1"), raw)
	if !strings.Contains(got, "kredit") {
		t.Fatalf("pesan kredit tidak muncul: %q", got)
	}
	if strings.Contains(got, "402") {
		t.Fatalf("kode mentah masih tampil: %q", got)
	}

	fallback := friendlyFailure(errors.New("exit status 1"), "Node.js not found")
	if !strings.Contains(fallback, "Node.js not found") || !strings.Contains(fallback, "exit status 1") {
		t.Fatalf("fallback kehilangan detail error: %q", fallback)
	}
}
