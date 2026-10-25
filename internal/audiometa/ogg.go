package audiometa

import (
	"bytes"
	"encoding/binary"
	"os"
)

// ------------------------------------------------------------------ Ogg pages

type oggPage struct {
	flags   byte
	granule uint64
	serial  uint32
	seq     uint32
	data    []byte // raw page body (packet segments)
}

type oggPacket struct {
	data    []byte
	pageIdx int
	granule uint64
	flags   byte
}

type oggStream struct {
	pages   []oggPage
	packets []oggPacket
}

func parseOggPages(data []byte) (*oggStream, error) {
	s := &oggStream{}
	pos := 0
	var cur []byte
	packetPage := -1
	for pos+27 <= len(data) {
		if string(data[pos:pos+4]) != "OggS" {
			// resync: find next capture pattern
			idx := bytes.Index(data[pos+1:], []byte("OggS"))
			if idx < 0 {
				break
			}
			pos += idx + 1
			continue
		}
		flags := data[pos+5]
		granule := binary.LittleEndian.Uint64(data[pos+6 : pos+14])
		serial := binary.LittleEndian.Uint32(data[pos+14 : pos+18])
		seq := binary.LittleEndian.Uint32(data[pos+18 : pos+22])
		nseg := int(data[pos+26])
		if pos+27+nseg > len(data) {
			break
		}
		seg := data[pos+27 : pos+27+nseg]
		bodyLen := 0
		for _, v := range seg {
			bodyLen += int(v)
		}
		bodyStart := pos + 27 + nseg
		if bodyStart+bodyLen > len(data) {
			break
		}
		body := data[bodyStart : bodyStart+bodyLen]
		s.pages = append(s.pages, oggPage{flags: flags, granule: granule, serial: serial, seq: seq, data: body})
		pageIdx := len(s.pages) - 1
		off := 0
		for _, v := range seg {
			if packetPage < 0 {
				packetPage = pageIdx
			}
			cur = append(cur, body[off:off+int(v)]...)
			off += int(v)
			if v < 255 {
				cp := make([]byte, len(cur))
				copy(cp, cur)
				s.packets = append(s.packets, oggPacket{data: cp, pageIdx: packetPage, granule: granule, flags: flags})
				cur = cur[:0]
				packetPage = -1
			}
		}
		pos = bodyStart + bodyLen
	}
	if len(s.pages) == 0 {
		return nil, ErrUnsupported
	}
	return s, nil
}

var oggCRCTable = func() [256]uint32 {
	var t [256]uint32
	for i := 0; i < 256; i++ {
		r := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if r&0x80000000 != 0 {
				r = r<<1 ^ 0x04C11DB7
			} else {
				r <<= 1
			}
		}
		t[i] = r
	}
	return t
}()

func oggCRC(data []byte) uint32 {
	var crc uint32
	for _, b := range data {
		crc = crc<<8 ^ oggCRCTable[byte(crc>>24)^b]
	}
	return crc
}

// buildOggPage serialises one page.
func buildOggPage(flags byte, granule uint64, serial, seq uint32, segments []byte, body []byte) []byte {
	out := make([]byte, 27, 27+len(segments)+len(body))
	copy(out, "OggS")
	out[4] = 0
	out[5] = flags
	binary.LittleEndian.PutUint64(out[6:14], granule)
	binary.LittleEndian.PutUint32(out[14:18], serial)
	binary.LittleEndian.PutUint32(out[18:22], seq)
	// crc left zero for now
	out[26] = byte(len(segments))
	out = append(out, segments...)
	out = append(out, body...)
	crc := oggCRC(out)
	binary.LittleEndian.PutUint32(out[22:26], crc)
	return out
}

// buildPages packs complete packets into one or more pages, splitting only
// when the 255 lacing-value limit of a page is reached (large comment packets
// with embedded artwork).
func buildPages(serial uint32, seq *uint32, startFlags byte, granule uint64, packets [][]byte) []byte {
	var out []byte
	var segs []byte
	var body []byte
	firstPage := true
	emit := func(flags byte, gran uint64) {
		out = append(out, buildOggPage(flags, gran, serial, *seq, segs, body)...)
		(*seq)++
		segs = segs[:0]
		body = body[:0]
	}
	contFlags := func() byte {
		f := byte(0x01) // continuation of a packet
		if firstPage {
			f = startFlags
			firstPage = false
		}
		return f
	}
	for _, p := range packets {
		remaining := p
		for {
			take := len(remaining)
			if take > 255 {
				take = 255
			}
			if len(segs) == 255 {
				emit(contFlags(), ^uint64(0))
			}
			segs = append(segs, byte(take))
			body = append(body, remaining[:take]...)
			remaining = remaining[take:]
			if take < 255 {
				break
			}
			if len(remaining) == 0 {
				// packet length is a multiple of 255: terminate with a 0 lacing
				if len(segs) == 255 {
					emit(contFlags(), ^uint64(0))
				}
				segs = append(segs, 0)
				break
			}
		}
	}
	if len(segs) > 0 {
		f := byte(0)
		if firstPage {
			f = startFlags
		}
		emit(f, granule)
	}
	return out
}

type oggFile struct {
	path    string
	serial  uint32
	vc      *vorbisComment
	format  string
	codec   string
	packets [][]byte
	pages   []oggPage
	stream  *oggStream
	info    Info
}

func (m *oggFile) apply(e Edits) error {
	pics := applyPictureEdits(m.vc.Pics, e)
	for k, v := range e.Set {
		if k == "" {
			continue
		}
		m.vc.set(k, v)
	}
	for _, k := range e.Remove {
		m.vc.remove(k)
	}
	return m.write(pics)
}

func openOgg(path string) (*oggFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := parseOggBytes(data)
	if err != nil {
		return nil, err
	}
	m.path = path
	if st, err := os.Stat(path); err == nil && m.info.Duration > 0 {
		m.info.Bitrate = int(float64(st.Size()) * 8 / m.info.Duration / 1000)
	}
	return m, nil
}

func parseOggBytes(data []byte) (*oggFile, error) {
	st, err := parseOggPages(data)
	if err != nil {
		return nil, err
	}
	m := &oggFile{stream: st, pages: st.pages}
	if len(st.pages) == 0 || len(st.packets) < 1 {
		return nil, ErrUnsupported
	}
	m.serial = st.pages[0].serial
	first := st.packets[0].data
	var commentPacket []byte
	switch {
	case len(first) > 7 && string(first[1:7]) == "vorbis":
		m.format = "OGG"
		m.codec = "Vorbis"
		if len(st.packets) > 1 {
			commentPacket = st.packets[1].data
		}
		if len(first) >= 16 {
			m.info.Channels = int(first[11])
			m.info.SampleRate = int(le32(first[12:16]))
		}
	case len(first) > 8 && string(first[:8]) == "OpusHead":
		m.format = "OPUS"
		m.codec = "Opus"
		if len(first) >= 12 {
			m.info.Channels = int(first[9])
			preskip := int(le16(first[10:12]))
			_ = preskip
			m.info.SampleRate = 48000
		}
		if len(st.packets) > 1 {
			commentPacket = st.packets[1].data
		}
	case len(first) > 8 && string(first[:8]) == "Speex   ":
		m.format = "OGG"
		m.codec = "Speex"
		if len(st.packets) > 1 {
			commentPacket = st.packets[1].data
		}
	default:
		m.format = "OGG"
		m.codec = "Ogg"
		if len(st.packets) > 1 {
			commentPacket = st.packets[1].data
		}
	}
	// strip the codec magic from the comment packet
	if len(commentPacket) > 7 && string(commentPacket[1:7]) == "vorbis" {
		commentPacket = commentPacket[7:]
	} else if len(commentPacket) > 8 && string(commentPacket[:8]) == "OpusTags" {
		commentPacket = commentPacket[8:]
	}
	if len(commentPacket) > 0 {
		if vc, err := parseVorbisComment(commentPacket); err == nil {
			m.vc = vc
		}
	}
	if m.vc == nil {
		m.vc = &vorbisComment{}
	}
	// duration from the last page granule
	last := st.pages[len(st.pages)-1]
	if m.info.SampleRate > 0 {
		m.info.Duration = float64(last.granule) / float64(m.info.SampleRate)
		if m.format == "OPUS" {
			preskip := 0
			if len(first) >= 12 {
				preskip = int(le16(first[10:12]))
			}
			m.info.Duration = float64(int64(last.granule)-int64(preskip)) / 48000
		}
		if m.info.Duration > 0 {
			m.info.Bitrate = int(float64(len(data)) * 8 / m.info.Duration / 1000)
		}
	}
	if m.info.SampleRate == 0 {
		m.info.SampleRate = 44100
	}
	return m, nil
}

func readOgg(f *os.File, size int64) (*Tags, error) {
	data, err := readAll(f, size)
	if err != nil {
		return nil, err
	}
	m, err := parseOggBytes(data)
	if err != nil {
		return nil, err
	}
	t := &Tags{Format: m.format, TagType: "Vorbis", Info: m.info}
	t.Fields = append(t.Fields, m.vc.Fields...)
	t.Pictures = append(t.Pictures, m.vc.Pics...)
	if t.Info.Codec == "" {
		t.Info.Codec = m.codec
	}
	return t, nil
}

// write re-paginates the stream: header packets are rebuilt (because the
// comment packet changed size), audio pages keep their original granule
// positions and grouping.
func (m *oggFile) write(pics []Picture) error {
	st := m.stream
	first := st.packets[0].data
	headerCount := 3
	prefix := []byte{}
	switch {
	case len(first) > 7 && string(first[1:7]) == "vorbis":
		headerCount = 3
		prefix = append([]byte{3}, "vorbis"...)
	case len(first) > 8 && string(first[:8]) == "OpusHead":
		headerCount = 2
		prefix = []byte("OpusTags")
	default:
		headerCount = 2
	}
	if headerCount > len(st.packets) {
		headerCount = len(st.packets)
	}
	commentPacket := append(append([]byte{}, prefix...), m.vc.encode(pics)...)
	if headerCount == 3 && len(first) > 7 && string(first[1:7]) == "vorbis" {
		commentPacket = append(commentPacket, 0x01) // Vorbis framing bit
	}

	var out bytes.Buffer
	seq := uint32(0)
	serial := m.serial
	// page 0: identification header only
	out.Write(buildPages(serial, &seq, 0x02, 0, [][]byte{st.packets[0].data}))
	// page 1: comment header (plus the setup header for Vorbis)
	rest := [][]byte{commentPacket}
	for i := 2; i < headerCount; i++ {
		rest = append(rest, st.packets[i].data)
	}
	out.Write(buildPages(serial, &seq, 0, 0, rest))

	// audio packets: reproduce the original page grouping so granule
	// positions stay exactly as the encoder wrote them
	audio := st.packets[headerCount:]
	for i := 0; i < len(audio); {
		pageIdx := audio[i].pageIdx
		var group [][]byte
		for i < len(audio) && audio[i].pageIdx == pageIdx {
			group = append(group, audio[i].data)
			i++
		}
		flags := st.pages[pageIdx].flags &^ 0x02 // never BOS
		if i == len(audio) {
			flags |= 0x04 // EOS on the last page
		}
		out.Write(buildPages(serial, &seq, flags&^0x01, st.pages[pageIdx].granule, group))
	}
	if len(audio) == 0 {
		// nothing else to write; mark the comment page as final
	}
	return writeFileAtomic(m.path, out.Bytes())
}
