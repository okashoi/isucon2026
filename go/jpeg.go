package isureal

import (
	"bytes"
	"image/jpeg"
)

// jpegSize は JPEG の幅と高さを返す。画素はデコードしない。
// jpeg.Decode は投稿の CPU の大半を使っていたので、ヘッダの解析と、最初のスキャンより後に EOI があることの確認で代える。
// 末尾が切れた画像は EOI が無いので弾ける。スキャンデータの中身の破損は検出しない。
func jpegSize(data []byte) (width, height int, ok bool) {
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || !hasJPEGEOIAfterScan(data) {
		return 0, 0, false
	}
	return config.Width, config.Height, true
}

// hasJPEGEOIAfterScan はセグメントを長さで読み飛ばして最初の SOS を探し、その後ろに EOI があるかを返す。
// APP セグメントに埋め込まれたサムネイルの EOI を本体の EOI と取り違えないように、SOS までは長さで飛ばす。
// スキャンデータの中の 0xFF はバイトスタッフィングされるので、0xFF 0xD9 は EOI にしか現れない。
// セグメント間の余計なバイトと 0xFF の詰め物は、image/jpeg と同じく読み飛ばす。
func hasJPEGEOIAfterScan(data []byte) bool {
	i := 2 // SOI
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			i++
			continue
		}
		marker := data[i+1]
		switch {
		case marker == 0xFF:
			i++
			continue
		case marker == 0x00, marker == 0x01, marker >= 0xD0 && marker <= 0xD7:
			// 長さを持たないマーカー。
			i += 2
			continue
		case marker == 0xD9:
			// SOS より前の EOI。image/jpeg は missing SOS marker で失敗する。
			return false
		}
		length := int(data[i+2])<<8 | int(data[i+3])
		if length < 2 {
			return false
		}
		next := i + 2 + length
		if marker == 0xDA {
			return next <= len(data) && bytes.Contains(data[next:], []byte{0xFF, 0xD9})
		}
		i = next
	}
	return false
}
