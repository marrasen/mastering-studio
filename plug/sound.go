package plug

// A Listener reads a [Sound] a frame of the window at a time, each
// frame of the sound once: what a meter or a spectrum takes. It is for
// one view, on the window's goroutine.
type Listener struct {
	Sound Sound
	buf   []float32
	at    int64
	rate  int
}

// Listen returns the frames heard since it was last called, at most
// most, stereo frames interleaved, with their rate: none while the
// sound stands still, and the last most at the first call and after a
// jump back. The frames are the Listener's, until the next call.
func (l *Listener) Listen(most int) (frames []float32, rate int) {
	if l.Sound == nil || most <= 0 {
		return nil, 0
	}
	if len(l.buf) < 2*most {
		l.buf = make([]float32, 2*most)
	}
	_, at, rate := l.Sound.Heard(l.buf[:0])
	if at == 0 || at == l.at {
		return nil, rate
	}
	want := int64(most)
	if at > l.at && l.at != 0 {
		want = min(want, at-l.at)
	}
	n, at, rate := l.Sound.Heard(l.buf[:2*want])
	l.at, l.rate = at, rate
	return l.buf[:2*n], rate
}
