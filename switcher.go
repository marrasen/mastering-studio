package main

import (
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/audio"
)

// switcher plays a track, rendered, and plays another render of it in
// its place as the track is edited while it plays, crossfading from
// the old. It loops, where a loop is set: at the loop's out the sound
// jumps back to its in, the sound past the out fading out.
//
// Every render reads one stream of the track's file, decoded ahead,
// each through a cursor of its own: one plays, one stands at the
// loop's in and one at its out, so the jump back reads at once.
//
// The sound reads and seeks it, one goroutine at a time; the
// application changes it through mailboxes the sound takes from as it
// reads, so neither waits for the other. A render the sound lets go
// comes back to the application to close, through gone.
type switcher struct {
	st   *audio.Stream
	path string
	rate int

	// The sound's: cur plays; old fades out over fade more frames, of
	// fadeLen, where it is the loop's spare where oldSpare says so.
	cur, old      *render
	oldSpare      bool
	fade, fadeLen int
	buf           []float32
	// loop, where on, plays from in to out over and over; count is the
	// frames given since the last restart.
	loop  loopSet
	count int64

	// The mailboxes: a swap and a loop waiting, and restarts asked.
	swapTo   atomic.Pointer[swapAsk]
	loopTo   atomic.Pointer[loopSet]
	restarts atomic.Int64
	restart0 int64
	// gone holds renders let go, for the application to close; goneAt
	// is where the sound puts the next.
	gone   [16]atomic.Pointer[render]
	goneAt int
	// length is the render's length, for any goroutine.
	length atomic.Int64
	// jumps are the jumps back since the last restart, each with the
	// count it came at and the frames gone back by then, of generation
	// jumpGen, for where and mapped to read from any goroutine.
	jumps   [64]loopJump
	jumpN   atomic.Int64
	jumpGen atomic.Int64
}

// loopFrames is a loop, in frames of the render.
type loopFrames struct {
	on      bool
	in, out int64
}

// loopSet is a loop and the renders standing at its ends: spare at the
// out, to fade out of, and anchor at the in, so the stream keeps what
// the jump back reads.
type loopSet struct {
	loopFrames
	spare, anchor *render
}

// swapAsk is a render to play in the current one's place, crossfading
// over fade frames.
type swapAsk struct {
	r    *render
	fade int
}

// loopJump is a jump back: at the count at, with back frames gone back
// by then, all told; gen says which restart it is of, -1 while it is
// written.
type loopJump struct{ gen, at, back atomic.Int64 }

// openSwitcher opens the track at path to play, rendered with gap and
// e.
func openSwitcher(path string, gap time.Duration, e Edit) (*switcher, error) {
	src, format, closer, err := openTrack(path)
	if err != nil {
		return nil, err
	}
	st, err := audio.NewStream(src, audio.StreamOptions{Rate: format.SampleRate, Close: closer})
	if err != nil {
		closer()
		return nil, err
	}
	s := &switcher{st: st, path: path, rate: format.SampleRate, buf: make([]float32, 2*stageChunk)}
	s.cur = s.render(gap, e)
	s.length.Store(s.cur.Len())
	return s, nil
}

// render returns a render of the track, with gap and e, on a cursor of
// its own.
func (s *switcher) render(gap time.Duration, e Edit) *render {
	c := s.st.Cursor()
	r := newRender(c, s.rate, gap, e)
	r.cursor = c
	return r
}

// prime seeks to frame f and waits a moment for the sound there to be
// decoded, so it starts whole. It is for before the sound reads.
func (s *switcher) prime(f int64) {
	_ = s.cur.SeekFrame(f)
	s.cur.cursor.WaitReady(int64(s.rate/10), time.Second)
}

// swap has the sound play a render with gap and e in place of the
// current one, from where it is, crossfading over fade frames.
func (s *switcher) swap(gap time.Duration, e Edit, fade int) {
	if was := s.swapTo.Swap(&swapAsk{r: s.render(gap, e), fade: fade}); was != nil {
		// Never taken: the sound never read it.
		was.r.release()
	}
	s.st.Wake()
}

// setLoop loops from in to out while l.on, with renders of gap and e
// at its ends.
func (s *switcher) setLoop(l loopFrames, gap time.Duration, e Edit) {
	set := &loopSet{loopFrames: l, spare: s.render(gap, e), anchor: s.render(gap, e)}
	_ = set.spare.SeekFrame(l.out)
	_ = set.anchor.SeekFrame(l.in)
	if was := s.loopTo.Swap(set); was != nil {
		was.spare.release()
		was.anchor.release()
	}
	s.st.Wake()
}

// restart forgets the jumps, as a seek starts the count again.
func (s *switcher) restart() { s.restarts.Add(1) }

// take takes what the mailboxes hold. It runs on the sound's side.
func (s *switcher) take() {
	if n := s.restarts.Load(); n != s.restart0 {
		s.restart0 = n
		s.jumpGen.Add(1)
		s.jumpN.Store(0)
	}
	if a := s.swapTo.Swap(nil); a != nil {
		_ = a.r.SeekFrame(s.cur.at)
		s.dropOld()
		s.old, s.oldSpare, s.cur = s.cur, false, a.r
		s.fade, s.fadeLen = a.fade, a.fade
		s.length.Store(s.cur.Len())
	}
	if l := s.loopTo.Swap(nil); l != nil {
		if s.oldSpare {
			// The spare fading out goes with its loop.
			s.old, s.oldSpare = nil, false
		}
		s.letGo(s.loop.spare)
		s.letGo(s.loop.anchor)
		s.loop = *l
	}
}

// letGo hands r to the application to close; a render the application
// has yet to take room for stays open until the stream closes.
func (s *switcher) letGo(r *render) {
	if r == nil {
		return
	}
	if s.gone[s.goneAt].CompareAndSwap(nil, r) {
		s.goneAt = (s.goneAt + 1) % len(s.gone)
	}
}

// collect closes the renders the sound let go. It runs on the
// application's side.
func (s *switcher) collect() {
	for i := range s.gone {
		if r := s.gone[i].Swap(nil); r != nil {
			r.release()
		}
	}
}

// where returns where in the track count frames given is: the count
// less the jumps back made by then.
func (s *switcher) where(count int64) int64 {
	g, n := s.jumpGen.Load(), s.jumpN.Load()
	for i := n - 1; i >= max(0, n-int64(len(s.jumps))); i-- {
		j := &s.jumps[i%int64(len(s.jumps))]
		if j.gen.Load() != g {
			continue
		}
		at, back := j.at.Load(), j.back.Load()
		if j.gen.Load() != g {
			continue
		}
		if at <= count {
			return count - back
		}
	}
	return count
}

// jumped records a jump back of back frames, at count.
func (s *switcher) jumped(back int64) {
	n := s.jumpN.Load()
	total := back
	if n > 0 {
		total += s.jumps[(n-1)%int64(len(s.jumps))].back.Load()
	}
	j := &s.jumps[n%int64(len(s.jumps))]
	j.gen.Store(-1)
	j.at.Store(s.count)
	j.back.Store(total)
	j.gen.Store(s.jumpGen.Load())
	s.jumpN.Store(n + 1)
}

// mapped is where in the track the frames given by at are.
func (s *switcher) mapped(at time.Duration) time.Duration {
	if s.jumpN.Load() == 0 {
		return at
	}
	return duration(s.where(frames(at, s.rate)), s.rate)
}

// Len implements [audio.Seeker].
func (s *switcher) Len() int64 { return s.length.Load() }

// SeekFrame implements [audio.Seeker].
func (s *switcher) SeekFrame(f int64) error {
	s.take()
	s.dropOld()
	// The count goes on from f: where that is takes the jumps back.
	s.count = f
	return s.cur.SeekFrame(s.where(f))
}

// dropOld stops the fade out: the old render goes, or, the loop's
// spare, stands at the out again for the next jump.
func (s *switcher) dropOld() {
	if s.old == nil {
		return
	}
	if s.oldSpare {
		_ = s.old.SeekFrame(s.loop.out)
	} else {
		s.letGo(s.old)
	}
	s.old, s.oldSpare = nil, false
}

// Read implements [audio.Source].
func (s *switcher) Read(dst []float32) (int, error) {
	s.take()
	dst = dst[:2*min(len(dst)/2, len(s.buf)/2)]
	// Looping, the sound goes as far as the loop's out.
	l := s.loop
	looping := l.on && l.spare != nil && l.out > l.in && s.cur.at < l.out
	if looping {
		dst = dst[:2*min(int64(len(dst)/2), l.out-s.cur.at)]
	}
	n, err := s.mix(dst)
	s.count += int64(n)
	if looping && s.cur.at >= l.out {
		// At the out, back to the in, the sound past the out, from the
		// spare standing there, fading out over 10 ms as the sound
		// from the in fades in.
		s.dropOld()
		_ = s.cur.SeekFrame(l.in)
		s.old, s.oldSpare = l.spare, true
		s.fade = max(1, s.rate/100)
		s.fadeLen = s.fade
		s.jumped(l.out - l.in)
		err = nil
	}
	return n, err
}

// mix reads the render into dst, the render before fading out of it.
func (s *switcher) mix(dst []float32) (int, error) {
	n, err := s.cur.Read(dst)
	if s.old == nil {
		return n, err
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

// close closes the track's stream and file. Nothing may read the
// switcher as it closes.
func (s *switcher) close() {
	s.collect()
	s.st.Close()
}
