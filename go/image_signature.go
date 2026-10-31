package isureal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
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

// sha256StateOffset は crypto/sha256 の MarshalBinary の出力で、連鎖値 h が始まる位置（先頭 4 バイトはマジック）。
const sha256StateOffset = 4

// computeImageSignature は HMAC-SHA256 を imageSignatureIterations 回重ねる。値は crypto/hmac と同じ。
// 2 回目以降の入力は直前の 32 バイトの MAC なので、内側と外側のハッシュはどちらもパディング込みで 1 ブロックになる。
// 鍵のブロックを処理した状態を保存しておき、各回は状態の復元とパディング済みブロックの Write だけで圧縮関数を呼ぶ。
// crypto/hmac の Sum が毎回行うダイジェストの複製とパディングの書き込みを省く。
func computeImageSignature(key []byte, imageID string, exp int64) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strconv.FormatInt(exp, 10) + ":" + imageID))
	value := mac.Sum(nil)
	if len(key) > sha256.BlockSize {
		// HMAC は長い鍵をハッシュしてから使う。imageSigningKey の鍵は 32 バイトなのでここは通らない。
		return repeatHMAC(mac, value)
	}
	innerState, outerState, err := hmacSHA256States(key)
	if err != nil {
		return repeatHMAC(mac, value)
	}
	var block [sha256.BlockSize]byte
	copy(block[:], value)
	block[sha256.Size] = 0x80
	binary.BigEndian.PutUint64(block[sha256.BlockSize-8:], (sha256.BlockSize+sha256.Size)*8)
	digest := sha256.New()
	unmarshaler := digest.(encoding.BinaryUnmarshaler)
	appender := digest.(encoding.BinaryAppender)
	state := make([]byte, 0, len(innerState))
	for range imageSignatureIterations - 1 {
		for _, saved := range [2][]byte{innerState, outerState} {
			if err := unmarshaler.UnmarshalBinary(saved); err != nil {
				return repeatHMAC(mac, value)
			}
			digest.Write(block[:])
			if state, err = appender.AppendBinary(state[:0]); err != nil {
				return repeatHMAC(mac, value)
			}
			copy(block[:sha256.Size], state[sha256StateOffset:])
		}
	}
	return block[:sha256.Size:sha256.Size]
}

// hmacSHA256States は、鍵に ipad と opad を掛けた 1 ブロックをそれぞれ処理した SHA-256 の状態を返す。
func hmacSHA256States(key []byte) (inner, outer []byte, err error) {
	var ipad, opad [sha256.BlockSize]byte
	copy(ipad[:], key)
	copy(opad[:], key)
	for i := range ipad {
		ipad[i] ^= 0x36
		opad[i] ^= 0x5c
	}
	digest := sha256.New()
	digest.Write(ipad[:])
	if inner, err = digest.(encoding.BinaryMarshaler).MarshalBinary(); err != nil {
		return nil, nil, err
	}
	digest.Reset()
	digest.Write(opad[:])
	if outer, err = digest.(encoding.BinaryMarshaler).MarshalBinary(); err != nil {
		return nil, nil, err
	}
	return inner, outer, nil
}

// repeatHMAC は 1 回目の MAC を計算済みの mac で、残りの回を crypto/hmac のまま計算する。
func repeatHMAC(mac hash.Hash, value []byte) []byte {
	for range imageSignatureIterations - 1 {
		mac.Reset()
		mac.Write(value)
		value = mac.Sum(value[:0])
	}
	return value
}

// minImageSignatureBatch は 16 レーンでまとめて計算する最小の件数。
// 1 回の呼び出しは 1 件でも 16 件でも同じ時間がかかり、1 件ずつ計算する 2〜3 件分にあたる。
const minImageSignatureBatch = 3

// imageSignatureBatch は応答に入れる画像 URL を集め、キャッシュにない署名を 16 件ずつまとめて計算してキャッシュに入れる。
// 応答を組み立てる前に sign を呼ぶと、signedImageURL はキャッシュを引くだけになる。
type imageSignatureBatch []imageSignatureInput

func (b *imageSignatureBatch) addImage(owner User, imageID string) {
	*b = append(*b, imageSignatureInput{key: owner.ImageKey, imageID: imageID})
}

func (b *imageSignatureBatch) addProfileImage(user User) {
	if user.ProfileImageID != nil {
		b.addImage(user, *user.ProfileImageID)
	}
}

func (b *imageSignatureBatch) addPost(post Post, author User) {
	b.addImage(author, post.ImageID)
	b.addProfileImage(author)
}

func (b imageSignatureBatch) sign() {
	if !hasSHA256X16 || len(b) < minImageSignatureBatch {
		return
	}
	exp := imageSignatureExpiry(time.Now())
	pending := make([]imageSignatureInput, 0, len(b))
	seen := make(map[imageSignatureCacheKey]struct{}, len(b))
	for _, input := range b {
		cacheKey, ok := newImageSignatureCacheKey(input.key, input.imageID, exp)
		if !ok {
			continue
		}
		if _, ok := seen[cacheKey]; ok {
			continue
		}
		seen[cacheKey] = struct{}{}
		if _, ok := imageSignatureCache.Load(cacheKey); ok {
			continue
		}
		input.exp = exp
		pending = append(pending, input)
	}
	for len(pending) >= minImageSignatureBatch {
		n := min(len(pending), 16)
		for i, sig := range computeImageSignaturesX16(pending[:n]) {
			cacheKey, _ := newImageSignatureCacheKey(pending[i].key, pending[i].imageID, exp)
			imageSignatureCache.Store(cacheKey, sig)
		}
		pending = pending[n:]
	}
}

type imageSignatureInput struct {
	key     []byte
	imageID string
	exp     int64
}

var sha256IV = [8]uint32{0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19}

// computeImageSignaturesX16 は最大 16 件の署名を、16 レーンの圧縮関数（sha256X16）でまとめて計算する。
// 値は computeImageSignature と同じ。鍵は sha256.BlockSize 以下で、hasSHA256X16 が真のときだけ呼ぶ。
func computeImageSignaturesX16(inputs []imageSignatureInput) [][]byte {
	var inner, outer [8][16]uint32
	for w := range sha256IV {
		for l := range 16 {
			inner[w][l] = sha256IV[w]
			outer[w][l] = sha256IV[w]
		}
	}
	// 空きのレーンは鍵が空として計算し、結果を捨てる。
	var blocks [16][sha256.BlockSize]byte
	for _, pad := range [2]struct {
		state *[8][16]uint32
		xor   byte
	}{{&inner, 0x36}, {&outer, 0x5c}} {
		for l := range blocks {
			blocks[l] = [sha256.BlockSize]byte{}
			if l < len(inputs) {
				copy(blocks[l][:], inputs[l].key)
			}
			for i := range blocks[l] {
				blocks[l][i] ^= pad.xor
			}
		}
		sha256X16(pad.state, &blocks)
	}

	for l := range blocks {
		blocks[l] = [sha256.BlockSize]byte{}
		if l < len(inputs) {
			mac := hmac.New(sha256.New, inputs[l].key)
			mac.Write([]byte(strconv.FormatInt(inputs[l].exp, 10) + ":" + inputs[l].imageID))
			mac.Sum(blocks[l][:0])
		}
		blocks[l][sha256.Size] = 0x80
		binary.BigEndian.PutUint64(blocks[l][sha256.BlockSize-8:], (sha256.BlockSize+sha256.Size)*8)
	}
	for range imageSignatureIterations - 1 {
		for _, state := range [2]*[8][16]uint32{&inner, &outer} {
			digests := *state
			sha256X16(&digests, &blocks)
			for l := range blocks {
				for w := range digests {
					binary.BigEndian.PutUint32(blocks[l][w*4:], digests[w][l])
				}
			}
		}
	}

	signatures := make([][]byte, len(inputs))
	for l := range signatures {
		signatures[l] = append([]byte(nil), blocks[l][:sha256.Size]...)
	}
	return signatures
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
