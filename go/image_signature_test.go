package isureal

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"strconv"
	"testing"
)

func TestImageSignatureMatchesRepeatedHMAC(t *testing.T) {
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := imageSignature(append([]byte(nil), tc.key...), tc.imageID, tc.exp)
			want := imageSignatureReference(tc.key, tc.imageID, tc.exp)
			if !bytes.Equal(got, want) {
				t.Fatalf("signature = %x, want %x", got, want)
			}
			if !bytes.Equal(imageSignature(tc.key, tc.imageID, tc.exp), got) {
				t.Fatal("signature changed across calls")
			}
		})
	}
}

// imageSignatureReference は変更前の実装。毎回 hmac.New する。
func imageSignatureReference(key []byte, imageID string, exp int64) []byte {
	value := []byte(strconv.FormatInt(exp, 10) + ":" + imageID)
	for range imageSignatureIterations {
		mac := hmac.New(sha256.New, key)
		mac.Write(value)
		value = mac.Sum(nil)
	}
	return value
}
