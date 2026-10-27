package secrets

import (
	"bytes"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	k, err := New(mustKey(t))
	if err != nil {
		t.Fatal(err)
	}
	blob, err := k.Seal([]byte(`{"accessKeyId":"AKIA"}`), "credential:42:aws")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("AKIA")) {
		t.Fatal("ciphertext contains the plaintext")
	}
	plain, err := k.Open(blob, "credential:42:aws")
	if err != nil || string(plain) != `{"accessKeyId":"AKIA"}` {
		t.Fatalf("open = %q, %v", plain, err)
	}

	// A blob is bound to its row: moved to another, it does not open.
	if _, err := k.Open(blob, "credential:43:aws"); err == nil {
		t.Fatal("blob opened under another row's context")
	}
	other, _ := New(mustKey(t))
	if _, err := other.Open(blob, "credential:42:aws"); err == nil {
		t.Fatal("another key opened the blob")
	}
	tampered := bytes.Clone(blob)
	tampered[len(tampered)-1] ^= 1
	if _, err := k.Open(tampered, "credential:42:aws"); err == nil {
		t.Fatal("tampered blob opened")
	}
	if _, err := k.Open([]byte("v1"), "credential:42:aws"); err == nil {
		t.Fatal("short blob opened")
	}
}

// Rotation: the first key seals, older ones still open what they sealed.
func TestKeyRotation(t *testing.T) {
	t.Parallel()
	oldKey, newKey := NewKey(), NewKey()
	oldKeys, err := ParseKeys(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := New(oldKeys...)
	blob, _ := before.Seal([]byte("secret"), "row")

	keys, err := ParseKeys(newKey + ", " + oldKey)
	if err != nil || len(keys) != 2 {
		t.Fatalf("parse keys = %d, %v", len(keys), err)
	}
	after, _ := New(keys...)
	if plain, err := after.Open(blob, "row"); err != nil || string(plain) != "secret" {
		t.Fatalf("old blob under the rotated ring = %q, %v", plain, err)
	}
	fresh, _ := after.Seal([]byte("secret"), "row")
	if _, err := before.Open(fresh, "row"); err == nil {
		t.Fatal("the old key opened a blob sealed with the new one")
	}
	onlyNew, _ := New(keys[0])
	if _, err := onlyNew.Open(fresh, "row"); err != nil {
		t.Fatal("the new key alone cannot open what it sealed")
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
	if _, err := ParseKeys(",,"); err == nil {
		t.Fatal("empty list accepted")
	}
	if _, err := ParseKeys(NewKey() + ",short"); err == nil || !strings.Contains(err.Error(), "key 2") {
		t.Fatalf("bad second key = %v", err)
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
