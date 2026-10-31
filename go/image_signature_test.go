package isureal

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"strconv"
	"testing"
)

func TestComputeImageSignatureMatchesRepeatedHMAC(t *testing.T) {
	key := bytes.Repeat([]byte{0xab}, sha256.Size)
	otherKey := bytes.Repeat([]byte{0xcd}, sha256.Size)
	cases := []struct {
		name    string
		key     []byte
		imageID string
		exp     int64
	}{
		{name: "typical", key: key, imageID: "11111111-1111-1111-1111-111111111111", exp: 1_800_000_000},
		{name: "empty image id", key: key, imageID: "", exp: 0},
		{name: "negative exp", key: otherKey, imageID: "img", exp: -1},
		{name: "long image id", key: otherKey, imageID: string(bytes.Repeat([]byte("x"), 200)), exp: 99},
		{name: "short key", key: []byte("k"), imageID: "img", exp: 1},
		{name: "block size key", key: bytes.Repeat([]byte{0x01}, sha256.BlockSize), imageID: "img", exp: 1},
		{name: "long key", key: bytes.Repeat([]byte{0x02}, sha256.BlockSize+1), imageID: "img", exp: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeImageSignature(tc.key, tc.imageID, tc.exp)
			want := imageSignatureReference(tc.key, tc.imageID, tc.exp)
			if !bytes.Equal(got, want) {
				t.Fatalf("signature = %x, want %x", got, want)
			}
		})
	}
}

func BenchmarkComputeImageSignature(b *testing.B) {
	key := bytes.Repeat([]byte{0xab}, sha256.Size)
	benchmarks := []struct {
		name string
		fn   func([]byte, string, int64) []byte
	}{
		{name: "fast", fn: computeImageSignature},
		{name: "reference", fn: imageSignatureReference},
	}
	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			for b.Loop() {
				bm.fn(key, "11111111-1111-1111-1111-111111111111", 1_800_000_000)
			}
		})
	}
}

// imageSignatureReference は crypto/hmac をそのまま使う実装。
func imageSignatureReference(key []byte, imageID string, exp int64) []byte {
	value := []byte(strconv.FormatInt(exp, 10) + ":" + imageID)
	mac := hmac.New(sha256.New, key)
	for range imageSignatureIterations {
		mac.Reset()
		mac.Write(value)
		value = mac.Sum(value[:0])
	}
	return value
}
