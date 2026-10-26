package secrets

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	key, err := ParseKey(NewKey())
	if err != nil {
		t.Fatal(err)
	}
	k, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := k.Seal([]byte(`{"accessKeyId":"AKIA"}`))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("AKIA")) {
		t.Fatal("ciphertext contains the plaintext")
	}
	plain, err := k.Open(blob)
	if err != nil || string(plain) != `{"accessKeyId":"AKIA"}` {
		t.Fatalf("open = %q, %v", plain, err)
	}

	other, _ := New(mustKey(t))
	if _, err := other.Open(blob); err == nil {
		t.Fatal("another key opened the blob")
	}
	blob[len(blob)-1] ^= 1
	if _, err := k.Open(blob); err == nil {
		t.Fatal("tampered blob opened")
	}
	if _, err := k.Open([]byte("v1")); err == nil {
		t.Fatal("short blob opened")
	}
}

func TestParseKeyRejectsWrongSizes(t *testing.T) {
	t.Parallel()
	if _, err := ParseKey("c2hvcnQ="); err == nil {
		t.Fatal("short key accepted")
	}
	if _, err := ParseKey("not base64!"); err == nil {
		t.Fatal("garbage accepted")
	}
}

func mustKey(t *testing.T) []byte {
	t.Helper()
	key, err := ParseKey(NewKey())
	if err != nil {
		t.Fatal(err)
	}

	return key
}
