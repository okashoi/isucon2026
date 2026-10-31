package isureal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const imageURLLifetime = 24 * time.Hour
const imageSignatureIterations = 1000

func imageSigningKey(secretKeyHash string) ([]byte, error) {
	key, err := hex.DecodeString(secretKeyHash)
	if err != nil || len(key) != sha256.Size {
		return nil, fmt.Errorf("invalid image signing key")
	}
	return key, nil
}

type imageSignatureCacheKey struct {
	exp     int64
	key     [sha256.Size]byte
	imageID string
}

var imageSignatureCache sync.Map

func clearImageSignatureCache() {
	imageSignatureCache.Clear()
}

func imageSignature(key []byte, imageID string, exp int64) []byte {
	cacheKey, ok := newImageSignatureCacheKey(key, imageID, exp)
	if !ok {
		return computeImageSignature(key, imageID, exp)
	}
	if sig, ok := imageSignatureCache.Load(cacheKey); ok {
		return sig.([]byte)
	}
	sig := computeImageSignature(key, imageID, exp)
	stored := make([]byte, len(sig))
	copy(stored, sig)
	imageSignatureCache.Store(cacheKey, stored)
	return stored
}

func newImageSignatureCacheKey(key []byte, imageID string, exp int64) (imageSignatureCacheKey, bool) {
	if len(key) != sha256.Size {
		return imageSignatureCacheKey{}, false
	}
	cacheKey := imageSignatureCacheKey{exp: exp, imageID: imageID}
	copy(cacheKey.key[:], key)
	return cacheKey, true
}

func computeImageSignature(key []byte, imageID string, exp int64) []byte {
	value := []byte(strconv.FormatInt(exp, 10) + ":" + imageID)
	mac := hmac.New(sha256.New, key)
	for range imageSignatureIterations {
		mac.Reset()
		mac.Write(value)
		value = mac.Sum(value[:0])
	}
	return value
}

// imageSignatureExpiry は期限を分境界へ切り上げる。同じ分に発行した URL が同じ exp になり、署名を再利用できる。
// 切り上げなので、有効期限は 24 時間より短くならない。
func imageSignatureExpiry(now time.Time) int64 {
	deadline := now.Add(imageURLLifetime)
	rounded := deadline.Truncate(time.Minute)
	if rounded.Before(deadline) {
		rounded = rounded.Add(time.Minute)
	}
	return rounded.Unix()
}

func signedImageURL(user User, imageID string) string {
	exp := imageSignatureExpiry(time.Now())
	query := url.Values{
		"exp": {strconv.FormatInt(exp, 10)},
		"sig": {base64.RawURLEncoding.EncodeToString(imageSignature(user.ImageKey, imageID, exp))},
	}
	return "/api/images/" + url.PathEscape(imageID) + "?" + query.Encode()
}
