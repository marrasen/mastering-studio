package main

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/speaker"
)

// A deck plays one track at a time, as rendered for export, at its
// file's rate, resampled on its way to the speakers only where they
// play at another. Switching
// tracks keeps the time, so the ear compares the same moment of each.
// Its methods are safe from any goroutine.
type deck struct {
	mix *audio.Mixer
	an  *audio.Analyzer

	mu sync.Mutex
	// spk is the speakers, or nil where they failed to open, for spkErr.
	spk    *speaker.Speaker
	spkErr error
	voice  *audio.Voice
	// sw is the track playing, as edited, and rack its chain; next is
	// the track queued after it.
	sw   *switcher
	rack *rack
	next *queued
	// turns carries the tracks the voice has turned to, from the queue.
	turns chan int
	// stage is the chain of the track playing, at rate, and tap holds
	// what it is fed, for the input's meters.
	stage *chainStage
	rate  int
	tap   *ioTap
	// monitor is how the sound is listened to: in stereo, mono, or its
	// side alone; the meters read it as it is. bypass plays the tracks
	// without their chains and gains.
	monitor *monitor
	bypass  atomic.Bool
	// id is the track playing, and volume the listening level, as a
	// ratio; matches are the gains, as ratios, by track, that bring each
	// to the target loudness while levels are matched, which each
	// track's stage plays it at.
	id      int
	volume  float32
	matches map[int]float32
}

func newDeck(mix *audio.Mixer) *deck {
	an := audio.NewAnalyzer(mix, 32)
	return &deck{mix: mix, an: an, volume: 0.8, matches: map[int]float32{}, turns: make(chan int, 8), tap: newIOTap(tapKeep),
		monitor: &monitor{}}
}

// play plays track id, rendered from the file at path with the album's
// gap and the edit e, through its chain, r, from at, paused or not; the
// track before fades out over fade, and this one in.
func (d *deck) play(id int, path string, gap time.Duration, e Edit, r *rack, at time.Duration, paused bool, fade time.Duration) error {
	sw, err := openSwitcher(path, gap, e)
	if err != nil {
		return err
	}
	// Decoded where it starts before it plays, so it starts whole.
	sw.prime(frames(at, sw.rate))
	st := newStage(sw, r, e.Out)
	st.tap, st.bypass = d.tap, &d.bypass
	st.setIn(e.Gain)
	out := audio.Resample(st, sw.rate, d.mix.Rate())
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopLocked(fade)
	r.setActive(true)
	st.setLevel(d.matchLocked(id))
	st.level = d.matchLocked(id)
	d.voice = d.mix.Play(out, audio.Options{Volume: max(d.volume, 1e-6), FadeIn: fade, Paused: paused,
		Insert: d.monitor})
	// Seeking through the voice, so it says where it is from the start.
	_ = d.voice.Seek(at)
	d.sw, d.rack, d.id, d.stage, d.rate = sw, r, id, st, sw.rate
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
			// The renders the sound let go, closed.
			d.sw.collect()
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
	d.sw, d.rack, d.id, d.next, d.stage, d.rate = n.sw, n.rack, n.id, nil, n.stage, n.rate
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
	same := d.voice != nil && d.id == id && sw.path == path
	d.mu.Unlock()
	if !same {
		return false
	}
	sw.swap(gap, e, sw.rate*15/1000)
	d.mu.Lock()
	if d.stage != nil {
		d.stage.setOut(e.Out)
		d.stage.setIn(e.Gain)
	}
	d.mu.Unlock()
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

// pauseFade is how long a pause fades the sound out, and playing on
// fades it back in.
const pauseFade = 150 * time.Millisecond

// setPaused pauses or resumes, fading over pauseFade.
func (d *deck) setPaused(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.voice == nil {
		return
	}
	if on {
		d.voice.Pause(pauseFade)
	} else {
		d.voice.Resume(pauseFade)
	}
}

// seek moves to at.
func (d *deck) seek(at time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.voice != nil {
		// A seek starts the count again: the jumps back before it are
		// behind it.
		d.sw.restart()
		_ = d.voice.Seek(at)
	}
}

// position returns where the track playing is, as heard, its length,
// and which it is; zero for none.
func (d *deck) position() (at, length time.Duration, id int) {
	d.mu.Lock()
	v, id, sw := d.voice, d.id, d.sw
	d.mu.Unlock()
	if v == nil {
		return 0, 0, 0
	}
	// Looping, the voice counts on through the jumps back: where in the
	// track that is takes them away.
	return sw.mapped(v.Position()), v.Len(), id
}

// heardFed returns the frame fed into the chain of the track playing
// that is heard now, at the chain's rate, and whether one plays: where
// the views of a plugin read the sound it plays.
func (d *deck) heardFed() (at int64, rate int, ok bool) {
	d.mu.Lock()
	v, rate, r := d.voice, d.rate, d.rack
	d.mu.Unlock()
	if v == nil || r == nil || rate == 0 {
		return 0, 0, false
	}
	// The chain is fed its latency ahead of what comes out.
	return frames(v.Position(), rate) + int64(r.latency()), rate, true
}

// heardFrames is the frames the voice has given since it was last
// sought, as heard: through jumps back, as the chain is fed them.
func (d *deck) heardFrames() (at time.Duration, ok bool) {
	d.mu.Lock()
	v := d.voice
	d.mu.Unlock()
	if v == nil {
		return 0, false
	}
	return v.Position(), true
}

// setLoop loops track id from in to out, render's time, while on; the
// sound past out fades out of a render of its own, of path with gap and
// e.
func (d *deck) setLoop(id int, path string, gap time.Duration, e Edit, in, out time.Duration, on bool) {
	d.mu.Lock()
	sw, rate, playing := d.sw, d.rate, d.voice != nil && d.id == id
	d.mu.Unlock()
	if !playing || sw == nil {
		return
	}
	if sw.path != path {
		return
	}
	sw.setLoop(loopFrames{on: on, in: frames(in, rate), out: frames(out, rate)}, gap, e)
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

// setLevel sets the listening volume, and the gains, in decibels, by
// track, that match each to the target: the track playing, and the one
// queued, take theirs up at once.
func (d *deck) setLevel(volume float32, matchDB map[int]float32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.volume = volume
	clear(d.matches)
	for id, db := range matchDB {
		d.matches[id] = float32(math.Pow(10, float64(db)/20))
	}
	if d.voice != nil {
		d.voice.SetVolume(max(d.volume, 1e-6), anim.Spring{Response: 0.15, Damping: 1})
	}
	if d.stage != nil {
		d.stage.setLevel(d.matchLocked(d.id))
	}
	if d.next != nil {
		d.next.stage.setLevel(d.matchLocked(d.next.id))
	}
}

// matchLocked is the gain, as a ratio, that matches track id's level.
// It runs with mu held.
func (d *deck) matchLocked(id int) float32 {
	if g, ok := d.matches[id]; ok {
		return g
	}
	return 1
}

// matchOf is the gain, as a ratio, that matches track id's level.
func (d *deck) matchOf(id int) float32 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.matchLocked(id)
}

// heard appends the frames heard since from to dst, as the analyzer
// gives them; the window's meters read every frame once.
func (d *deck) heard(dst []float32, from int64) (frames []float32, now int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.an.HeardBefore(dst, from)
}

// spectrum fills out with the level at each of freqs, as heard.
func (d *deck) spectrum(freqs, out []float32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.an.Spectrum(freqs, nil, out)
}

// queued is a track waiting to play once the one playing ends.
type queued struct {
	id    int
	sw    *switcher
	rack  *rack
	stage *chainStage
	rate  int
}

// queue has track id, rendered and run through its chain r, play once
// the track playing ends, without a gap, in place of any queued before.
func (d *deck) queue(id int, path string, gap time.Duration, e Edit, r *rack) error {
	sw, err := openSwitcher(path, gap, e)
	if err != nil {
		return err
	}
	sw.prime(0)
	st := newStage(sw, r, e.Out)
	st.tap, st.bypass = d.tap, &d.bypass
	st.setIn(e.Gain)
	out := audio.Resample(st, sw.rate, d.mix.Rate())
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.voice == nil {
		sw.close()
		return nil
	}
	d.unqueueLocked()
	// Its chain runs from the moment the mixer reaches it, at its own
	// level.
	r.setActive(true)
	st.setLevel(d.matchLocked(id))
	st.level = d.matchLocked(id)
	d.voice.Then(out)
	d.next = &queued{id: id, sw: sw, rack: r, stage: st, rate: sw.rate}
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

// input appends to dst the frames fed into the chain of the track
// playing from frame from up to to, at its rate, which it returns.
func (d *deck) input(dst []float32, from, to int64) (frames []float32, rate int) {
	d.mu.Lock()
	rate = d.rate
	d.mu.Unlock()
	return d.tap.read(dst, from, to), rate
}

// Listen is how the sound is listened to.
type Listen int

const (
	// ListenStereo plays it as it is.
	ListenStereo Listen = iota
	// ListenMono plays its channels together, in both speakers, to hear
	// it as a mono player would.
	ListenMono
	// ListenSide plays the difference of its channels, in both
	// speakers: what differs between them, as reverb and width.
	ListenSide
)

// monitor is the insert that plays the sound as listened to. Only the
// speakers hear it: what is measured and exported is the sound as it
// is.
type monitor struct{ mode atomic.Int32 }

// Process implements [audio.Insert].
func (m *monitor) Process(frames []float32) {
	switch Listen(m.mode.Load()) {
	case ListenMono:
		for i := 0; i+1 < len(frames); i += 2 {
			v := (frames[i] + frames[i+1]) / 2
			frames[i], frames[i+1] = v, v
		}
	case ListenSide:
		for i := 0; i+1 < len(frames); i += 2 {
			v := (frames[i] - frames[i+1]) / 2
			frames[i], frames[i+1] = v, v
		}
	case ListenStereo:
	}
}

// listen sets how the sound is listened to.
func (d *deck) listen(l Listen) { d.monitor.mode.Store(int32(l)) }

// setBypass plays the tracks without their chains and gains, or with.
func (d *deck) setBypass(on bool) { d.bypass.Store(on) }
