package main

import (
	"time"

	"github.com/marrasen/gunim/audio"
)

// mixPaced mixes buf full through mix, as a sound card asks for it: in
// blocks of 10 ms, and before each, waits up to a second for the deck's
// tracks, the one playing and the one queued next, to have decoded what
// the block reads. Their decoder runs on a goroutine of its own; mixing
// on at once, on a slow computer, it would find nothing decoded and the
// sound would not move. The test's goroutine both mixes and waits, so
// the deck's renders are its own between blocks.
func mixPaced(d *deck, mix *audio.Mixer, buf []float32) {
	const block = audio.SampleRate / 100
	for at := 0; at < len(buf); at += 2 * block {
		d.mu.Lock()
		var cursors []*audio.Cursor
		for _, sw := range []*switcher{d.sw, d.nextSwitcher()} {
			if sw != nil && sw.cur != nil && sw.cur.cursor != nil {
				cursors = append(cursors, sw.cur.cursor)
			}
		}
		d.mu.Unlock()
		for _, c := range cursors {
			c.WaitReady(int64(audio.SampleRate/10), time.Second)
		}
		mix.Mix(buf[at:min(at+2*block, len(buf))])
	}
}

// nextSwitcher is the switcher of the track queued next, or nil. It
// runs with mu held.
func (d *deck) nextSwitcher() *switcher {
	if d.next == nil {
		return nil
	}
	return d.next.sw
}
