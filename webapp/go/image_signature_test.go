package isureal

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
)

func TestImageSignatureMatchesExistingFormat(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	const imageID = "00000000-0000-0000-0000-000000000001"
	const exp = 1760000000
	// Generated independently with Python's standard-library hmac.digest in a 1000-round loop.
	want, err := hex.DecodeString("6a4f46e2f0a6523a3744034ee75fa8fc43977eac8e10ab9abf131b7cff8dd0dc")
	if err != nil {
		t.Fatal(err)
	}

	got := imageSignature(key, imageID, exp)
	if !bytes.Equal(got, want) {
		t.Fatalf("imageSignature() = %x, want %x", got, want)
	}

	// A later signature call must not mutate a signature already returned.
	first := append([]byte(nil), got...)
	imageSignature(key, imageID+"x", exp)
	if !bytes.Equal(got, first) {
		t.Fatalf("a later call mutated the previous signature: got %x, want %x", got, first)
	}
}

func TestImageSignatureMatchesRecreatedHMACForInputs(t *testing.T) {
	keyA := make([]byte, 32)
	for i := range keyA {
		keyA[i] = byte(i)
	}
	keyB := bytes.Repeat([]byte{0xa5}, 32)
	tests := []struct {
		name    string
		key     []byte
		imageID string
		exp     int64
	}{
		{name: "empty ID", key: keyA, imageID: "", exp: 0},
		{name: "different key and ID", key: keyB, imageID: "image-42", exp: 1760000001},
		{name: "input exactly one SHA-256 block", key: keyA, imageID: string(bytes.Repeat([]byte{'x'}, 53)), exp: 1760000000},
		{name: "input crosses SHA-256 block", key: keyB, imageID: string(bytes.Repeat([]byte{'y'}, 54)), exp: 1760000000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := imageSignature(tt.key, tt.imageID, tt.exp)
			want := imageSignatureRecreateHMAC(tt.key, tt.imageID, tt.exp)
			if !bytes.Equal(got, want) {
				t.Fatalf("imageSignature() = %x, want legacy result %x", got, want)
			}
		})
	}
}

func BenchmarkImageSignature(b *testing.B) {
	key := make([]byte, 32)
	const imageID = "00000000-0000-0000-0000-000000000001"
	const exp = 1760000000
	b.Run("reuse", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			imageSignature(key, imageID, exp)
		}
	})
	b.Run("recreate", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			imageSignatureRecreateHMAC(key, imageID, exp)
		}
	})
}

func imageSignatureRecreateHMAC(key []byte, imageID string, exp int64) []byte {
	value := []byte(strconv.FormatInt(exp, 10) + ":" + imageID)
	for range imageSignatureIterations {
		mac := hmac.New(sha256.New, key)
		mac.Write(value)
		value = mac.Sum(nil)
	}
	return value
}
