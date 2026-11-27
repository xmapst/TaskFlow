package zmodem

import (
	"bytes"
	"errors"
	"fmt"
)

// ErrFileRejected ends the attachment without forwarding the rejected offer.
var ErrFileRejected = errors.New("ZMODEM file transfer rejected")

const maxOfferWireSize = 2*maxZFileSubPacketSize + sessionHeaderBufferLimit

// offerGate follows only the sending side. It retains a candidate header and
// ZFILE metadata, but never buffers file contents across calls.
type offerGate struct {
	parser    ZSession
	header    []byte
	wire      []byte
	offer     *ZFileInfo
	can       int
	ready     bool
	fin       bool
	afterData bool
	emptyAck  []byte
}

func (g *offerGate) filter(data []byte, authorize func(*ZFileInfo) error) ([]byte, error) {
	// CAN is recognized even inside an unfinished offer. Never flush the
	// withheld offer while passing cancellation to the receiver.
	for i, b := range data {
		if b != CAN {
			g.can = 0
			continue
		}
		g.can++
		if g.can == len(AbortSession) {
			*g = offerGate{}
			end := i + 1
			for end < len(data) && (data[end] == CAN || data[end] == '\b') {
				end++
			}
			return append(bytes.Clone(AbortSession), data[i+1:end]...), nil
		}
	}
	var out []byte
	for len(data) != 0 {
		if g.afterData {
			b := data[0]
			if len(g.emptyAck) != 0 || b == ZDLE {
				g.emptyAck = append(g.emptyAck, b)
				data = data[1:]
				ok, complete := g.checkEmptyAck()
				if !ok {
					return nil, invalidOffer("invalid trailing ZCRCQ")
				}
				if complete {
					out = append(out, g.emptyAck...)
					g.emptyAck = nil
				}
				continue
			}
			if g.isFlowByte(b) {
				out = append(out, b)
				data = data[1:]
				continue
			}
			g.afterData = false
		}
		if g.parser.hasSubPacket() {
			file := g.parser.IsNeedSubPacket()
			fileData := g.parser.IsDataPacket()
			rest := g.parser.consumeDataPacket(data)
			g.afterData = fileData && !g.parser.hasSubPacket()
			used := len(data) - len(rest)
			if file {
				if len(g.wire)+used > maxOfferWireSize || g.parser.metadataInvalid {
					return nil, invalidOffer("invalid or oversized ZFILE metadata")
				}
				g.wire = append(g.wire, data[:used]...)
				if !g.parser.IsNeedSubPacket() {
					if g.offer == nil {
						return nil, invalidOffer("invalid ZFILE metadata or checksum")
					}
					if err := authorize(g.offer); err != nil {
						return nil, err
					}
					out = append(out, g.wire...)
					g.wire, g.offer, g.ready = nil, nil, true
				}
			} else {
				out = append(out, data[:used]...)
			}
			data = rest
			continue
		}

		// Skip runs of non-header bytes without copying into the header buffer.
		// After ZFIN the sender can emit OO followed by ordinary terminal output.
		if len(g.wire) == 0 && data[0] != ZPAD {
			n := 0
			for n < len(data) && data[n] != ZPAD {
				b := data[n]
				if !g.fin && !g.isFlowByte(b) && b != '\r' && b != '\n' && b != 0x8d && b != 0x8a && b != CAN && b != '\b' {
					return nil, invalidOffer("unexpected sender bytes outside a ZMODEM frame")
				}
				n++
			}
			out = append(out, data[:n]...)
			data = data[n:]
			continue
		}

		b := data[0]
		data = data[1:]
		g.wire = append(g.wire, b)
		if len(g.wire) > sessionHeaderBufferLimit {
			return nil, invalidOffer("overlong ZMODEM header")
		}
		if g.isFlowByte(b) {
			continue
		}
		g.header = append(g.header, b)
		pad := 0
		for pad < len(g.header) && g.header[pad] == ZPAD {
			pad++
		}
		if pad == len(g.header) || (len(g.header) == pad+1 && b == ZDLE) {
			continue
		}
		if g.header[pad] != ZDLE || !g.isHeaderFormat(g.header[pad+1]) {
			if !g.fin {
				return nil, invalidOffer("unsupported ZMODEM header")
			}
			// Ordinary terminal output after ZFIN may contain '*'.
			out = append(out, g.wire...)
			g.wire, g.header = nil, nil
			continue
		}

		format := g.header[pad+1]
		body := g.header[pad+2:]
		var h ZmodemHeader
		var ok bool
		switch format {
		case ZHEX:
			h, _, ok = DecodeHexFrameHeader(append(bytes.Clone(HexHeaderPrefix), body...))
			g.parser.frameCRCSize = crc16Size
			if !ok && (b&0x7f == '\n' || len(body) > 16) {
				return nil, invalidOffer("invalid hex ZMODEM header")
			}
		case ZBIN:
			h, _, ok = DecodeB16FrameHeader(g.header[pad-1:])
			g.parser.frameCRCSize = crc16Size
		case ZBIN32:
			h, _, ok = DecodeB32FrameHeader(g.header[pad-1:])
			g.parser.frameCRCSize = crc32Size
		}
		if !ok {
			continue
		}
		g.header = nil
		switch h.Type {
		case ZFILE:
			if g.fin {
				return nil, invalidOffer("ZFILE after ZFIN")
			}
			g.offer, g.ready = nil, false
			g.parser.ZFileHeaderCallback = func(info *ZFileInfo) { g.offer = info }
			g.parser.onHeader(&h)
			continue
		case ZDATA:
			if !g.ready || g.fin {
				return nil, invalidOffer("ZDATA without an authorized ZFILE")
			}
		case ZCOMMAND:
			return nil, invalidOffer("ZMODEM remote commands are not supported")
		case ZFIN:
			g.fin = true
		}
		g.parser.onHeader(&h)
		out = append(out, g.wire...)
		g.wire = nil
	}
	return out, nil
}

// lrzsz can request a window acknowledgment with an empty ZCRCQ after
// terminating ZDATA with ZCRCE. Accept only that exact checksummed packet.
func (g *offerGate) checkEmptyAck() (ok, complete bool) {
	if len(g.emptyAck) > sessionHeaderBufferLimit {
		return false, false
	}
	var clean [7]byte
	n := 0
	for _, b := range g.emptyAck {
		if !g.isFlowByte(b) {
			if n == len(clean) {
				return false, false
			}
			clean[n] = b
			n++
		}
	}
	if g.parser.frameCRCSize == crc16Size {
		want := []byte{ZDLE, ZCRCQ, 0xcd, 0xec}
		return bytes.HasPrefix(want, clean[:n]), n == len(want)
	}
	want := []byte{ZDLE, ZCRCQ, 0xcb, 0x67, 0x65, 0x7f}
	if bytes.HasPrefix(want, clean[:n]) {
		return true, n == len(want)
	}
	escaped := []byte{ZDLE, ZCRCQ, 0xcb, 0x67, 0x65, ZDLE, ZRUB0}
	return bytes.HasPrefix(escaped, clean[:n]), n == len(escaped)
}

func (g *offerGate) isFlowByte(b byte) bool { return b == 0x11 || b == 0x13 || b == 0x91 || b == 0x93 }

func (g *offerGate) isHeaderFormat(b byte) bool { return b == ZHEX || b == ZBIN || b == ZBIN32 }

func invalidOffer(reason string) error { return fmt.Errorf("%w: %s", ErrFileRejected, reason) }
