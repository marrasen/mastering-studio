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

	mu    sync.Mutex
	voice *audio.Voice
	// sw is the track playing, as edited, and rack its chain; next is
	// the track queued after it.
	sw   *switcher
	rack *rack
	next *queued
	// turns carries the tracks the voice has turned to, from the queue.
	turns chan int
	// id is the track playing, and volume the listening level, as a
	// ratio, with match, the gain that brings the track to the target
	// loudness while levels are matched.
	id     int
	volume float32
	match  float32
}

func newDeck(mix *audio.Mixer) *deck {
	an := audio.NewAnalyzer(mix, 32)
	return &deck{mix: mix, an: an, volume: 0.8, match: 1, turns: make(chan int, 8)}
}

// play plays track id, rendered from the file at path with the album's
// gap and the edit e, through its chain, r, from at, paused or not; the
// track before fades out over fade, and this one in.
func (d *deck) play(id int, path string, gap time.Duration, e Edit, r *rack, at time.Duration, paused bool, fade time.Duration) error {
	src, format, closer, err := openTrack(path)
	if err != nil {
		return err
	}
	sw := &switcher{cur: newRender(src, format.SampleRate, gap, e), closer: closer}
	out := audio.Resample(newStage(sw, r), format.SampleRate)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopLocked(fade)
	r.setActive(true)
	d.voice = d.mix.Play(out, audio.Options{Volume: max(d.volume*d.match, 1e-6), FadeIn: fade, Paused: paused})
	// Seeking through the voice, so it says where it is from the start.
	_ = d.voice.Seek(at)
	d.sw, d.rack, d.id = sw, r, id
	go d.watch(d.voice)
	return nil
}

// watch follows voice v's turns to the tracks queued, until it ends.
// It looks with mu held, as unqueue does, so a turn is taken once.
func (d *deck) watch(v *audio.Voice) {
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-v.Done():
			return
		case <-t.C:
		}
		d.mu.Lock()
		if d.voice == v {
			d.takeTurnLocked()
		}
		d.mu.Unlock()
	}
}

// takeTurnLocked makes the track queued the one playing, where the
// voice has turned to it, and tells of it. It runs with mu held.
func (d *deck) takeTurnLocked() bool {
	if d.next == nil {
		return false
	}
	select {
	case <-d.voice.Turned():
	default:
		return false
	}
	n := d.next
	d.sw.close()
	if d.rack != n.rack {
		d.rack.setActive(false)
	}
	d.sw, d.rack, d.id, d.next = n.sw, n.rack, n.id, nil
	select {
	case d.turns <- n.id:
	default:
	}
	return true
}

// edit plays track id as edited anew, from where it is, with a crossfade
// too short to hear as one; it returns false where the track is not the
// one playing.
func (d *deck) edit(id int, path string, gap time.Duration, e Edit) bool {
	d.mu.Lock()
	sw := d.sw
	same := d.voice != nil && d.id == id
	d.mu.Unlock()
	if !same {
		return false
	}
	src, format, closer, err := openTrack(path)
	if err != nil {
		return false
	}
	sw.swap(newRender(src, format.SampleRate, gap, e), closer, format.SampleRate*15/1000)
	return true
}

// stopLocked fades the voice playing out over fade, and closes its file
// once it is done, and stops its chain, unless it plays on. It runs with
// mu held.
func (d *deck) stopLocked(fade time.Duration) {
	if d.voice == nil {
		return
	}
	d.unqueueLocked()
	old, sw, r := d.voice, d.sw, d.rack
	old.Stop(fade)
	go func() {
		<-old.Done()
		sw.close()
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.rack != r {
			r.setActive(false)
		}
	}()
	d.voice, d.sw, d.rack, d.id = nil, nil, nil, 0
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

// switcher plays a track's render, and takes a new one for an edit,
// crossfading from the old.
type switcher struct {
	mu     sync.Mutex
	cur    *render
	closer func()
	// old fades out over fade more frames, of fadeLen.
	old           *render
	oldCloser     func()
	fade, fadeLen int
	buf           []float32
}

// swap plays r, closed by closer, from where the render before is,
// crossfading over fade frames.
func (s *switcher) swap(r *render, closer func(), fade int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.old != nil {
		s.oldCloser()
	}
	_ = r.SeekFrame(s.cur.at)
	s.old, s.oldCloser, s.cur, s.closer = s.cur, s.closer, r, closer
	s.fade, s.fadeLen = fade, fade
}

// Len implements [audio.Seeker].
func (s *switcher) Len() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur.Len()
}

// SeekFrame implements [audio.Seeker].
func (s *switcher) SeekFrame(f int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropOld()
	return s.cur.SeekFrame(f)
}

func (s *switcher) dropOld() {
	if s.old != nil {
		s.oldCloser()
		s.old, s.oldCloser = nil, nil
	}
}

// Read implements [audio.Source].
func (s *switcher) Read(dst []float32) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.cur.Read(dst)
	if s.old == nil {
		return n, err
	}
	if cap(s.buf) < 2*n {
		s.buf = make([]float32, 2*n)
	}
	b := s.buf[:2*n]
	clear(b)
	_, _ = s.old.Read(b)
	for i := range n {
		t := float32(1)
		if left := s.fade - i; left > 0 {
			t = 1 - float32(left)/float32(s.fadeLen)
		}
		dst[2*i] = dst[2*i]*t + b[2*i]*(1-t)
		dst[2*i+1] = dst[2*i+1]*t + b[2*i+1]*(1-t)
	}
	if s.fade -= n; s.fade <= 0 {
		s.dropOld()
	}
	return n, err
}

// close closes the renders' files.
func (s *switcher) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropOld()
	s.closer()
}

// queued is a track waiting to play once the one playing ends.
type queued struct {
	id   int
	sw   *switcher
	rack *rack
}

// queue has track id, rendered and run through its chain r, play once
// the track playing ends, without a gap, in place of any queued before.
func (d *deck) queue(id int, path string, gap time.Duration, e Edit, r *rack) error {
	src, format, closer, err := openTrack(path)
	if err != nil {
		return err
	}
	sw := &switcher{cur: newRender(src, format.SampleRate, gap, e), closer: closer}
	out := audio.Resample(newStage(sw, r), format.SampleRate)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.voice == nil {
		sw.close()
		return nil
	}
	d.unqueueLocked()
	// Its chain runs from the moment the mixer reaches it.
	r.setActive(true)
	d.voice.Then(out)
	d.next = &queued{id: id, sw: sw, rack: r}
	return nil
}

// unqueue takes the track queued away.
func (d *deck) unqueue() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.unqueueLocked()
}

func (d *deck) unqueueLocked() {
	if d.next == nil {
		return
	}
	if d.voice != nil {
		d.voice.Then(nil)
		// Turned to it already, it plays: it stays.
		if d.takeTurnLocked() {
			return
		}
	}
	d.next.sw.close()
	if d.next.rack != d.rack {
		d.next.rack.setActive(false)
	}
	d.next = nil
}

// hasNext says a track is queued.
func (d *deck) hasNext() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.next != nil
}
