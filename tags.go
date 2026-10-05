package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"strings"
	"unicode/utf16"
)

// tags are what an MP3 file's ID3 tag says it is: its title and who it
// is by.
type tags struct {
	Title, Artist string
}

// name is what the tags call the sound: "Artist – Title", or the one of
// them it has, or "".
func (t tags) name() string {
	switch {
	case t.Title != "" && t.Artist != "":
		return t.Artist + " – " + t.Title
	case t.Title != "":
		return t.Title
	}
	return t.Artist
}

// readTags reads the title and artist of the MP3 file at path: from its
// ID3v2 tag at the start, or else its ID3v1 tag at the end.
func readTags(path string) tags {
	f, err := os.Open(path)
	if err != nil {
		return tags{}
	}
	defer func() { _ = f.Close() }()
	if t := id3v2(f); t.name() != "" {
		return t
	}
	return id3v1(f)
}

// id3v2 reads an ID3v2.2, 2.3 or 2.4 tag at the start of r.
func id3v2(r io.ReadSeeker) tags {
	var head [10]byte
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return tags{}
	}
	if _, err := io.ReadFull(r, head[:]); err != nil || string(head[:3]) != "ID3" {
		return tags{}
	}
	version, flags := head[3], head[5]
	size := syncsafe(head[6:10])
	if size > 16<<20 {
		return tags{}
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return tags{}
	}
	// Unsynchronised whole, as 2.2 and 2.3 do: each 0xFF 0x00 was 0xFF.
	if flags&0x80 != 0 && version < 4 {
		body = bytes.ReplaceAll(body, []byte{0xff, 0x00}, []byte{0xff})
	}
	// An extended header, before the frames.
	if flags&0x40 != 0 && version >= 3 && len(body) >= 4 {
		n := int(binary.BigEndian.Uint32(body[:4])) + 4
		if version == 4 {
			n = syncsafe(body[:4])
		}
		if n > len(body) {
			return tags{}
		}
		body = body[n:]
	}
	idLen, headLen := 4, 10
	if version == 2 {
		idLen, headLen = 3, 6
	}
	var t tags
	for len(body) >= headLen && body[0] != 0 {
		id := string(body[:idLen])
		var n int
		switch version {
		case 2:
			n = int(body[3])<<16 | int(body[4])<<8 | int(body[5])
		case 3:
			n = int(binary.BigEndian.Uint32(body[4:8]))
		default:
			n = syncsafe(body[4:8])
		}
		if n < 0 || headLen+n > len(body) {
			break
		}
		frame := body[headLen : headLen+n]
		// Unsynchronised by the frame, as 2.4 does.
		if version == 4 && body[9]&0x02 != 0 {
			frame = bytes.ReplaceAll(frame, []byte{0xff, 0x00}, []byte{0xff})
		}
		switch id {
		case "TIT2", "TT2":
			t.Title = frameText(frame)
		case "TPE1", "TP1":
			t.Artist = frameText(frame)
		}
		body = body[headLen+n:]
	}
	return t
}

// syncsafe reads a size of four bytes of seven bits each.
func syncsafe(b []byte) int {
	return int(b[0]&0x7f)<<21 | int(b[1]&0x7f)<<14 | int(b[2]&0x7f)<<7 | int(b[3]&0x7f)
}

// frameText reads a text frame: its encoding, then its text, of which the
// first of the values it may hold.
func frameText(frame []byte) string {
	if len(frame) < 1 {
		return ""
	}
	enc, b := frame[0], frame[1:]
	var s string
	switch enc {
	case 1, 2:
		// UTF-16, with a byte order mark, or big-endian without.
		big := enc == 2
		if len(b) >= 2 && (b[0] == 0xfe && b[1] == 0xff || b[0] == 0xff && b[1] == 0xfe) {
			big = b[0] == 0xfe
			b = b[2:]
		}
		u := make([]uint16, len(b)/2)
		for i := range u {
			if big {
				u[i] = binary.BigEndian.Uint16(b[2*i:])
			} else {
				u[i] = binary.LittleEndian.Uint16(b[2*i:])
			}
		}
		s = string(utf16.Decode(u))
	case 3:
		s = string(b)
	default:
		s = latin1(b)
	}
	if i := strings.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// latin1 reads ISO-8859-1.
func latin1(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

// id3v1 reads an ID3v1 tag, the last 128 bytes of r.
func id3v1(r io.ReadSeeker) tags {
	var tag [128]byte
	if _, err := r.Seek(-128, io.SeekEnd); err != nil {
		return tags{}
	}
	if _, err := io.ReadFull(r, tag[:]); err != nil || string(tag[:3]) != "TAG" {
		return tags{}
	}
	field := func(b []byte) string {
		if i := bytes.IndexByte(b, 0); i >= 0 {
			b = b[:i]
		}
		return strings.TrimSpace(latin1(b))
	}
	return tags{Title: field(tag[3:33]), Artist: field(tag[33:63])}
}
