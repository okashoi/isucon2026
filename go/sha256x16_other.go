//go:build !amd64

package isureal

const hasSHA256X16 = false

func sha256X16(digests *[8][16]uint32, blocks *[16][64]byte) {
	panic("sha256X16 requires amd64")
}
