package zmodem

import (
	"bytes"
	"log/slog"
	"strconv"
	"time"
)

/*

Binary16 Header:

ZDLE A TYPE F3/P0 F2/P1 F1/P2 F0/P3 CRC-1 CRC-2

Binary32 Header
ZDLE C TYPE F3/P0 F2/P1	F1/P2 F0/P3 CRC-1 CRC-2	CRC-3 CRC-4

*/

var (
	zSessionEnd = []byte{0x4f, 0x4f}
)

const (
	bin16HeaderLen = 7
	bin32HeaderLen = 9

	sessionHeaderBufferLimit = 256
	maxZFileSubPacketSize    = 64 * 1024
	maxZFileNameSize         = 4 * 1024

	crc16Size = 2
	crc32Size = 4
)

func DecodeHexFrameHeader(p []byte) (h ZmodemHeader, offset int, ok bool) {
	const headerLen = 18 // Prefix and seven hex-encoded bytes.
	if len(p) <= headerLen || !bytes.HasPrefix(p, HexHeaderPrefix) {
		return
	}
	endPos := headerLen
	if p[endPos] == '\r' || p[endPos] == 0x8d {
		endPos++
	}
	if len(p) <= endPos || p[endPos]&0x7f != '\n' {
		return
	}
	encoded := p[len(HexHeaderPrefix):headerLen]
	decoded := make([]byte, 7)
	for i := range decoded {
		hi, hiOK := hexNibble(encoded[i*2])
		lo, loOK := hexNibble(encoded[i*2+1])
		if !hiOK || !loOK {
			return
		}
		decoded[i] = hi<<4 | lo
	}
	if !validCRC16(decoded[:5], decoded[5:]) {
		return
	}
	return ZmodemHeader{Type: decoded[0], ZF0: decoded[1], ZF1: decoded[2], ZF2: decoded[3], ZF3: decoded[4]}, endPos, true
}

func DecodeB16FrameHeader(p []byte) (h ZmodemHeader, offset int, ok bool) {
	index := bytes.Index(p, Binary16HeaderPrefix)
	if index < 0 {
		return ZmodemHeader{}, 0, false
	}
	remain := p[index+len(Binary16HeaderPrefix):]
	offset = index + len(Binary16HeaderPrefix)
	header := make([]byte, 0, bin16HeaderLen)
	var gotZDLE bool
	for i, value := range remain {
		switch value {
		case ZDLE:
			gotZDLE = true
			continue
		default:
			if gotZDLE {
				switch value {
				case ZRUB0:
					value = 0x7f
				case ZRUB1:
					value = 0xff
				default:
					if value&0x60 != 0x40 {
						return ZmodemHeader{}, offset, false
					}
					value ^= 0x40
				}
				header = append(header, value)
			} else {
				header = append(header, value)
			}
			gotZDLE = false
		}

		if len(header) == bin16HeaderLen {
			offset += i + 1
			break
		}
	}
	if len(header) != bin16HeaderLen {
		return ZmodemHeader{}, offset, false
	}
	if !validCRC16(header[:5], header[5:]) {
		return ZmodemHeader{}, offset, false
	}
	return ZmodemHeader{
		Type: header[0],
		ZF0:  header[1],
		ZF1:  header[2],
		ZF2:  header[3],
		ZF3:  header[4],
	}, offset, true

}

func DecodeB32FrameHeader(p []byte) (h ZmodemHeader, offset int, ok bool) {
	index := bytes.Index(p, Binary32HeaderPrefix)
	if index < 0 {
		return ZmodemHeader{}, 0, false
	}
	remain := p[index+len(Binary32HeaderPrefix):]
	offset = index + len(Binary32HeaderPrefix)
	header := make([]byte, 0, bin32HeaderLen)
	var gotZDLE bool
	for i, value := range remain {
		switch value {
		case ZDLE:
			gotZDLE = true
			continue
		default:
			if gotZDLE {
				switch value {
				case ZRUB0:
					value = 0x7f
				case ZRUB1:
					value = 0xff
				default:
					if value&0x60 != 0x40 {
						return ZmodemHeader{}, offset, false
					}
					value ^= 0x40
				}
				header = append(header, value)
			} else {
				header = append(header, value)
			}
			gotZDLE = false
		}

		if len(header) == bin32HeaderLen {
			offset += i + 1
			break
		}
	}
	if len(header) != bin32HeaderLen {
		return ZmodemHeader{}, offset, false
	}
	if !validCRC32(header[:5], header[5:]) {
		return ZmodemHeader{}, offset, false
	}
	return ZmodemHeader{
		Type: header[0],
		ZF0:  header[1],
		ZF1:  header[2],
		ZF2:  header[3],
		ZF3:  header[4],
	}, offset, true
}

type ZmodemHeader struct {
	Type byte
	ZF0  byte
	ZF1  byte
	ZF2  byte
	ZF3  byte
}

const (
	TypeUpload   = "upload"
	TypeDownload = "download"
)

const (
	TransferStatusStart    = "start"
	TransferStatusFinished = "end"
	TransferStatusAbort    = "abort"
)

type ZFileInfo struct {
	filename string
	size     int64

	parserTime   time.Time
	transferType string
}

func (z *ZFileInfo) Type() string {
	return z.transferType
}

func (z *ZFileInfo) Filename() string {
	return z.filename
}

func (z *ZFileInfo) Size() int64 {
	return z.size
}

func (z *ZFileInfo) Time() time.Time {
	return z.parserTime
}

type ZSession struct {
	endCallback         func()
	zFileInfo           *ZFileInfo
	currentHd           *ZmodemHeader
	ZFileHeaderCallback func(zInfo *ZFileInfo)
	zOnHeader           func(hd *ZmodemHeader)
	Type                string
	transferStatus      string
	headerBuf           []byte
	metadata            []byte
	metadataInvalid     bool
	metadataCRC         [crc32Size]byte
	zfinBuf             []byte
	frameCRCSize        int
	dataCRCRemaining    int
	abortSequenceLen    int
	dataGotZDLE         bool
	dataFrameEnds       bool
	haveEnd             bool
	allowEarlyFinish    bool
	stopAtFinish        bool
	pendingFinish       bool
	AbnormalFinish      bool
}

// consume handles both fragmented and coalesced frames without keeping file data.
func (s *ZSession) consume(p []byte) {
	if s.IsEnd() {
		return
	}
	// Parse the bytes before CAN first: a file offer and cancellation can be
	// read from the PTY together. A partial CAN run is also an escape prefix.
	abort := false
	if !s.gotZFin() {
		for i, b := range p {
			if b != CAN {
				s.abortSequenceLen = 0
				continue
			}
			s.abortSequenceLen++
			if s.abortSequenceLen == len(AbortSession) {
				p = p[:max(0, i-len(AbortSession)+1)]
				abort = true
				break
			}
		}
	}
	for len(p) > 0 && !s.IsEnd() {
		switch {
		case s.gotZFin():
			p = s.consumeFinish(p)
		case s.hasSubPacket():
			p = s.consumeDataPacket(p)
		default:
			p = s.consumeHeader(p)
		}
	}
	if abort {
		s.transferStatus = TransferStatusAbort
	}
}

func (s *ZSession) consumeFinish(p []byte) []byte {
	if len(s.zfinBuf) == 0 {
		for len(p) > 0 {
			switch p[0] {
			case 0x8a, 0x8d, '\r', '\n', 0x11, 0x13:
				p = p[1:]
				continue
			}
			break
		}
	}
	s.zfinBuf = append(s.zfinBuf, p...)
	if len(s.zfinBuf) < len(zSessionEnd) {
		return nil
	}
	if bytes.HasPrefix(s.zfinBuf, zSessionEnd) {
		if s.pendingFinish {
			s.pendingFinish = false
			s.onHeader(s.currentHd)
		}
		s.haveEnd = true
		if s.endCallback != nil {
			s.endCallback()
		}
		return nil
	}
	if s.pendingFinish {
		remain := s.zfinBuf
		s.zfinBuf = nil
		s.pendingFinish = false
		s.currentHd = nil
		return remain
	}
	s.haveEnd = true
	s.AbnormalFinish = true
	return nil
}

func (s *ZSession) consumeHeader(p []byte) []byte {
	if len(s.headerBuf) != 0 {
		p = append(s.headerBuf, p...)
		s.headerBuf = nil
	}
	for len(p) > 0 {
		index, headerType := s.findFrameHeader(p)
		if index == -1 {
			retain := len(HexHeaderPrefix) - 1
			s.headerBuf = bytes.Clone(p[max(0, len(p)-retain):])
			return nil
		}

		candidate := p[index:]
		var (
			hd        ZmodemHeader
			offset    int
			ok        bool
			remainPos int
		)
		switch headerType {
		case ZHEX:
			hd, offset, ok = DecodeHexFrameHeader(candidate)
			remainPos = offset + 1
			s.frameCRCSize = crc16Size
		case ZBIN:
			hd, offset, ok = DecodeB16FrameHeader(candidate)
			remainPos = offset
			s.frameCRCSize = crc16Size
		case ZBIN32:
			hd, offset, ok = DecodeB32FrameHeader(candidate)
			remainPos = offset
			s.frameCRCSize = crc32Size
		}
		if !ok {
			// TCP/WebSocket 均可能把一个 ZMODEM 头拆成多个数据包，保留后继续解析。
			hasInvalidTerminator := headerType == ZHEX &&
				(bytes.IndexByte(candidate, 0x8a) != -1 || bytes.IndexByte(candidate, 0x0a) != -1)
			if hasInvalidTerminator || len(candidate) > sessionHeaderBufferLimit {
				p = candidate[1:]
				continue
			}
			s.headerBuf = bytes.Clone(candidate)
			return nil
		}

		remain := candidate[remainPos:]
		if hd.Type == ZFIN && s.transferStatus != TransferStatusFinished && !s.allowEarlyFinish {
			// Wait for OO before treating an early finish as definitive.
			s.currentHd = &hd
			s.pendingFinish = true
			return remain
		}
		s.onHeader(&hd)
		return remain
	}
	return nil
}

// consumeDataPacket skips file data. Only ZFILE metadata is retained, and only
// up to a fixed limit. The trailer and its escaped CRC can span reads.
func (s *ZSession) consumeDataPacket(p []byte) []byte {
	metadata := s.IsNeedSubPacket()
	for i, value := range p {
		switch value {
		case 0x11, 0x13, 0x91, 0x93:
			continue
		}
		if s.dataGotZDLE {
			s.dataGotZDLE = false
			if s.dataCRCRemaining == 0 && value >= ZCRCE && value <= ZCRCW {
				s.dataFrameEnds = value == ZCRCE || value == ZCRCW
				s.dataCRCRemaining = s.frameCRCSize
				if metadata && !s.metadataInvalid {
					s.metadata = append(s.metadata, value) // CRC includes the end marker.
				}
				continue
			}
			switch value {
			case ZRUB0:
				value = 0x7f
			case ZRUB1:
				value = 0xff
			default:
				if value&0x60 != 0x40 {
					s.metadataInvalid = true
				}
				value ^= 0x40
			}
		} else if value == ZDLE {
			s.dataGotZDLE = true
			continue
		}
		if s.dataCRCRemaining > 0 {
			if metadata {
				s.metadataCRC[s.frameCRCSize-s.dataCRCRemaining] = value
			}
			s.dataCRCRemaining--
			if s.dataCRCRemaining != 0 {
				continue
			}
			if metadata && !s.metadataInvalid && s.dataFrameEnds {
				wire := s.metadataCRC[:s.frameCRCSize]
				if (s.frameCRCSize == crc16Size && validCRC16(s.metadata, wire)) ||
					(s.frameCRCSize == crc32Size && validCRC32(s.metadata, wire)) {
					s.onSubPacket(s.metadata[:len(s.metadata)-1])
				}
			}
			s.metadata = s.metadata[:0]
			if s.dataFrameEnds {
				s.currentHd = nil
				return p[i+1:]
			}
			// A ZFILE offer has exactly one metadata subpacket.
			s.metadataInvalid = true
			continue
		}
		if metadata && !s.metadataInvalid {
			if len(s.metadata) == maxZFileSubPacketSize {
				s.metadataInvalid = true
			} else {
				s.metadata = append(s.metadata, value)
			}
		}
	}
	return nil
}

func (s *ZSession) findFrameHeader(p []byte) (int, byte) {
	index := -1
	headerType := byte(0)
	for _, item := range []struct {
		prefix     []byte
		headerType byte
	}{
		{HexHeaderPrefix, ZHEX},
		{Binary16HeaderPrefix, ZBIN},
		{Binary32HeaderPrefix, ZBIN32},
	} {
		current := bytes.Index(p, item.prefix)
		if current != -1 && (index == -1 || current < index) {
			index = current
			headerType = item.headerType
		}
	}
	return index, headerType
}

func (s *ZSession) onSubPacket(p []byte) {
	switch s.currentHd.Type {
	case ZFILE:
		var info ZFileInfo
		filenameIndex := bytes.IndexByte(p, 0x00)
		if filenameIndex == -1 {
			slog.Error("zmodem invalid ZFILE metadata: filename terminator missing", slog.String("type", s.Type))
			break
		}
		filename := p[:filenameIndex]
		if !s.validZFileName(filename) {
			slog.Error("zmodem invalid ZFILE filename", slog.String("type", s.Type))
			break
		}
		remain := p[filenameIndex+1:]
		zFileOptions := bytes.Fields(remain)
		if len(zFileOptions) == 0 {
			slog.Error("zmodem invalid ZFILE metadata: file size missing", slog.String("type", s.Type))
			break
		}
		size, err := strconv.ParseInt(string(zFileOptions[0]), 10, 64)
		if err != nil || size < 0 {
			slog.Error("Zmodem invalid ZFILE size", slog.String("type", s.Type))
			break
		}

		info.filename = string(filename)
		info.size = size
		info.parserTime = time.Now()
		info.transferType = s.Type
		s.zFileInfo = &info
		if s.ZFileHeaderCallback != nil {
			s.ZFileHeaderCallback(&info)
		}
	}
	s.currentHd = nil
}

func (s *ZSession) validZFileName(filename []byte) bool {
	if len(filename) == 0 || len(filename) > maxZFileNameSize {
		return false
	}
	for _, value := range filename {
		if value < 0x20 || value == 0x7f {
			return false
		}
	}
	return true
}

func (s *ZSession) onHeader(hd *ZmodemHeader) {
	switch hd.Type {
	case ZFILE:
		s.transferStatus = TransferStatusStart
	}
	switch hd.Type {
	case ZFILE, ZSINIT, ZDATA, ZCOMMAND, ZSTDERR:
		s.metadata = s.metadata[:0]
		s.metadataInvalid = false
		s.dataGotZDLE = false
		s.dataCRCRemaining = 0
		s.dataFrameEnds = false
	case ZEOF:
		s.transferStatus = TransferStatusFinished
		s.zFileInfo = nil
	case ZFIN:
		s.haveEnd = s.stopAtFinish
		if s.endCallback != nil {
			s.endCallback()
		}

	}
	if s.zOnHeader != nil {
		s.zOnHeader(hd)
	}
	s.currentHd = hd
	slog.Debug("zmodem session receive header", slog.String("type", s.Type), slog.Any("frame_type", FrameType(hd.Type)))
}

func (s *ZSession) IsEnd() bool {
	return s.haveEnd || s.transferStatus == TransferStatusAbort
}

func (s *ZSession) IsNeedSubPacket() bool {
	return s.currentHd != nil && s.currentHd.Type == ZFILE
}

func (s *ZSession) IsDataPacket() bool {
	return s.currentHd != nil && s.currentHd.Type == ZDATA
}

func (s *ZSession) hasSubPacket() bool {
	if s.currentHd == nil {
		return false
	}
	switch s.currentHd.Type {
	case ZFILE, ZSINIT, ZDATA, ZCOMMAND, ZSTDERR:
		return true
	}
	return false
}

func (s *ZSession) gotZFin() bool {
	return s.currentHd != nil && s.currentHd.Type == ZFIN
}
