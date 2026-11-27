package zmodem

import (
	"encoding/binary"
	"hash/crc32"
)

// zmodemCRC16 computes the wire CRC-16 (CRC-CCITT with a zero initial value).
// XORing each byte into the high byte before shifting already gives the
// equivalent of the lrzsz low-byte update followed by two zero bytes.
func zmodemCRC16(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func hexNibble(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}
func validCRC16(data, wire []byte) bool {
	return len(wire) == 2 && zmodemCRC16(data) == binary.BigEndian.Uint16(wire)
}

func validCRC32(data, wire []byte) bool {
	if len(wire) != 4 {
		return false
	}
	return crc32.ChecksumIEEE(data) == binary.LittleEndian.Uint32(wire)
}
