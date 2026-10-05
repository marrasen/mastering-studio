package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

// id3Frame is a text frame of an ID3v2.3 or 2.4 tag.
func id3Frame(id string, version, enc byte, body []byte) []byte {
	data := append([]byte{enc}, body...)
	var size [4]byte
	if version == 4 {
		n := len(data)
		size = [4]byte{byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}
	} else {
		binary.BigEndian.PutUint32(size[:], uint32(len(data)))
	}
	return append(append(append([]byte(id), size[:]...), 0, 0), data...)
}

// id3Tag is an ID3v2 tag of frames, and a little sound after it.
func id3Tag(version byte, frames ...[]byte) []byte {
	body := bytes.Join(frames, nil)
	n := len(body)
	tag := make([]byte, 0, 10+len(body)+4)
	tag = append(tag, 'I', 'D', '3', version, 0, 0, byte(n>>21&0x7f), byte(n>>14&0x7f), byte(n>>7&0x7f), byte(n&0x7f))
	return append(append(tag, body...), 0xff, 0xfb, 0x90, 0x00)
}

func utf16BOM(s string) []byte {
	b := []byte{0xff, 0xfe}
	for _, u := range utf16.Encode([]rune(s)) {
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	return append(b, 0, 0)
}

func TestAReferenceMP3IsCalledAsItsTagSays(t *testing.T) {
	dir := t.TempDir()
	v1 := make([]byte, 128)
	copy(v1, "TAG")
	copy(v1[3:], "Old Title")
	copy(v1[33:], "Old Artist")
	for _, c := range []struct {
		name string
		file []byte
		want string
	}{
		{"2.3, UTF-16 and Latin-1", id3Tag(3, id3Frame("TIT2", 3, 1, utf16BOM("Smörgås")),
			id3Frame("TPE1", 3, 0, []byte("Bj\xf6rk"))), "Björk – Smörgås"},
		{"2.4, UTF-8, two values", id3Tag(4, id3Frame("TIT2", 4, 3, []byte("Title\x00Other"))), "Title"},
		{"1, at the end", append([]byte{0xff, 0xfb, 0x90, 0x00}, v1...), "Old Artist – Old Title"},
		{"none", []byte{0xff, 0xfb, 0x90, 0x00}, ""},
	} {
		path := filepath.Join(dir, c.name+".mp3")
		if err := os.WriteFile(path, c.file, 0o644); err != nil {
			t.Fatal(err)
		}
		if got := readTags(path).name(); got != c.want {
			t.Errorf("%s: the tag reads %q, want %q", c.name, got, c.want)
		}
	}
}
