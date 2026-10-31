package isureal

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"strconv"
	"testing"
	"time"
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

func TestComputeImageSignaturesX16MatchesRepeatedHMAC(t *testing.T) {
	if !hasSHA256X16 {
		t.Skip("AVX-512 がない")
	}
	cases := []struct {
		name string
		n    int
	}{
		{name: "one lane", n: 1},
		{name: "partial lanes", n: 5},
		{name: "all lanes", n: 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inputs := make([]imageSignatureInput, tc.n)
			for i := range inputs {
				key := bytes.Repeat([]byte{byte(i + 1)}, sha256.Size)
				if i%4 == 3 {
					key = []byte("short")
				}
				inputs[i] = imageSignatureInput{key: key, imageID: strconv.Itoa(i) + "-11111111-1111-1111-1111-111111111111", exp: int64(1_800_000_000 + i)}
			}
			got := computeImageSignaturesX16(inputs)
			if len(got) != len(inputs) {
				t.Fatalf("len = %d, want %d", len(got), len(inputs))
			}
			for i, in := range inputs {
				if want := imageSignatureReference(in.key, in.imageID, in.exp); !bytes.Equal(got[i], want) {
					t.Fatalf("lane %d: signature = %x, want %x", i, got[i], want)
				}
			}
		})
	}
}

func TestImageSignatureBatchFillsCache(t *testing.T) {
	if !hasSHA256X16 {
		t.Skip("AVX-512 がない")
	}
	clearImageSignatureCache()
	t.Cleanup(clearImageSignatureCache)
	var batch imageSignatureBatch
	for i := range 20 {
		profileImageID := "profile-" + strconv.Itoa(i)
		user := User{ImageKey: bytes.Repeat([]byte{byte(i + 1)}, sha256.Size), ProfileImageID: &profileImageID}
		batch.addPost(Post{ImageID: "post-" + strconv.Itoa(i)}, user)
		batch.addProfileImage(user)
	}
	batch.sign()
	exp := imageSignatureExpiry(time.Now())
	for _, input := range batch {
		cacheKey, _ := newImageSignatureCacheKey(input.key, input.imageID, exp)
		cached, ok := imageSignatureCache.Load(cacheKey)
		if !ok {
			// 分の境目をまたいだときは exp が変わるので、確かめられない。
			t.Skipf("cache miss for %s (exp changed)", input.imageID)
		}
		if want := imageSignatureReference(input.key, input.imageID, exp); !bytes.Equal(cached.([]byte), want) {
			t.Fatalf("%s: signature = %x, want %x", input.imageID, cached, want)
		}
	}
}

func BenchmarkComputeImageSignatures16(b *testing.B) {
	inputs := make([]imageSignatureInput, 16)
	for i := range inputs {
		inputs[i] = imageSignatureInput{key: bytes.Repeat([]byte{byte(i + 1)}, sha256.Size), imageID: strconv.Itoa(i), exp: 1_800_000_000}
	}
	b.Run("x16", func(b *testing.B) {
		if !hasSHA256X16 {
			b.Skip("AVX-512 がない")
		}
		for b.Loop() {
			computeImageSignaturesX16(inputs)
		}
	})
	b.Run("sequential", func(b *testing.B) {
		for b.Loop() {
			for _, in := range inputs {
				computeImageSignature(in.key, in.imageID, in.exp)
			}
		}
	})
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
