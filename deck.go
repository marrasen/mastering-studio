package main

import (
	"math"
	"sync"
	"time"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/speaker"
)

// A deck plays one track at a time, as rendered for export, at its
// file's rate, resampled only on its way to the speakers. Switching
// tracks keeps the time, so the ear compares the same moment of each.
// Its methods are safe from any goroutine.
type deck struct {
	mix *audio.Mixer
	an  *audio.Analyzer
	spk *speaker.Speaker

	mu     sync.Mutex
	voice  *audio.Voice
	closer func()
	// id is the track playing, and volume the listening level, as a
	// ratio, with match, the gain that brings the track to the target
	// loudness while levels are matched.
	id     int
	volume float32
	match  float32
}

func newDeck(mix *audio.Mixer) *deck {
	an := audio.NewAnalyzer(mix, 32)
	return &deck{mix: mix, an: an, volume: 0.8, match: 1}
}

// play plays track id, rendered from the file at path with the album's
// gap and the edit e, from at, paused or not; the track before fades out
// over fade, and this one in.
func (d *deck) play(id int, path string, gap time.Duration, e Edit, at time.Duration, paused bool, fade time.Duration) error {
	src, format, closer, err := openTrack(path)
	if err != nil {
		return err
	}
	r := newRender(src, format.SampleRate, gap, e)
	out := audio.Resample(r, format.SampleRate)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopLocked(fade)
	d.voice = d.mix.Play(out, audio.Options{Volume: max(d.volume*d.match, 1e-6), FadeIn: fade, Paused: paused})
	// Seeking through the voice, so it says where it is from the start.
	_ = d.voice.Seek(at)
	d.closer, d.id = closer, id
	return nil
}

// stopLocked fades the voice playing out over fade, and closes its file
// once it is done. It runs with mu held.
func (d *deck) stopLocked(fade time.Duration) {
	if d.voice == nil {
		return
	}
	old, closer := d.voice, d.closer
	old.Stop(fade)
	go func() {
		<-old.Done()
		closer()
	}()
	d.voice, d.closer, d.id = nil, nil, 0
}

func (d *deck) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopLocked(20 * time.Millisecond)
}

// setPaused pauses or resumes.
func (d *deck) setPaused(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.voice == nil {
		return
	}
	if on {
		d.voice.Pause()
	} else {
		d.voice.Resume()
	}
}

// seek moves to at.
func (d *deck) seek(at time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.voice != nil {
		_ = d.voice.Seek(at)
	}
}

// position returns where the track playing is, as heard, its length,
// and which it is; zero for none.
func (d *deck) position() (at, length time.Duration, id int) {
	d.mu.Lock()
	v, id := d.voice, d.id
	d.mu.Unlock()
	if v == nil {
		return 0, 0, 0
	}
	return v.Position(), v.Len(), id
}

// done returns a channel closed as the track playing ends, or nil.
func (d *deck) done() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.voice == nil {
		return nil
	}
	return d.voice.Done()
}

// setLevel sets the listening volume, and the gain that matches the
// track to the target, in decibels.
func (d *deck) setLevel(volume, matchDB float32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.volume, d.match = volume, float32(math.Pow(10, float64(matchDB)/20))
	if d.voice != nil {
		d.voice.SetVolume(max(d.volume*d.match, 1e-6), anim.Spring{Response: 0.15, Damping: 1})
	}
}

// heard appends the frames heard since from to dst, as the analyzer
// gives them; the window's meters read every frame once.
func (d *deck) heard(dst []float32, from int64) (frames []float32, now int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.an.Heard(dst, from)
}

// spectrum fills out with the level at each of freqs, as heard.
func (d *deck) spectrum(freqs, out []float32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.an.Spectrum(freqs, out, nil)
}
