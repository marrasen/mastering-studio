package main

import (
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/speaker"
)

// Output is how the studio plays its sound: through which driver, at
// what rate, in what samples, and how far ahead of the speakers. It is
// kept for the computer, across albums.
type Output struct {
	// Driver is the ASIO driver played through, by name, or empty for
	// the system's own sound.
	Driver string `json:",omitempty"`
	// Rate is the rate played at, in hertz; zero is 48 kHz. A track at
	// the same rate plays sample for sample, and one at another is
	// resampled to it.
	Rate int `json:",omitempty"`
	// Bits rounds the sound to integer samples of 16 or 24 bits as it
	// leaves, as a WAV file of such samples holds it, dithered where
	// Dither says; zero leaves 32-bit float.
	Bits   int `json:",omitempty"`
	Dither bool
	// Fixed holds the buffer at Buffer samples. Otherwise it starts at
	// autoLatency and grows each time the sound breaks up.
	Fixed  bool `json:",omitempty"`
	Buffer int  `json:",omitempty"`
}

// firstOutput is how the studio plays until the user says otherwise.
var firstOutput = Output{Dither: true}

// autoLatency is how far ahead the mixer starts out working, where the
// buffer grows by itself. Mastering wants no quick answer from the
// sound: a buffer that rides out a busy moment, the meters following
// it as heard.
const autoLatency = 150 * time.Millisecond

// outputRates are the rates the settings offer.
var outputRates = []int{44100, 48000, 88200, 96000, 176400, 192000}

// bufferSizes are the buffers the settings offer, in samples.
var bufferSizes = []int{512, 1024, 2048, 4096, 8192, 16384, 32768}

// rate returns the rate o plays at.
func (o Output) rate() int {
	if o.Rate > 0 {
		return o.Rate
	}
	return audio.SampleRate
}

// options returns the speaker's options for o.
func (o Output) options() speaker.Options {
	so := speaker.Options{Name: appName, Driver: o.Driver, Rate: o.rate(), Bits: o.Bits, Dither: o.Dither,
		Latency: autoLatency}
	if o.Fixed && o.Buffer > 0 {
		so.Fixed = true
		so.Latency = audio.DurationAt(int64(o.Buffer), so.Rate)
	}
	return so
}

// SetOutput sets how the studio plays.
type SetOutput struct{ Output Output }

// OpenControlPanel opens the ASIO driver's own settings.
type OpenControlPanel struct{}

// OutputState is how the studio plays now, as the speakers report it.
type OutputState struct {
	// Silent says no sound plays at all, for Problem.
	Silent bool
	// Driver is the ASIO driver playing, or empty for the system's own
	// sound.
	Driver string
	// Rate is the rate played at, and DeviceRate the rate the system
	// converts it to on its way to the device, where it does.
	Rate, DeviceRate int
	// Latency is how far ahead the mixer works, and Frames the same in
	// samples.
	Latency time.Duration
	Frames  int64
	// Dropouts counts the times the sound broke up.
	Dropouts int64
	// Problem says why the output asked for plays otherwise.
	Problem string
	// DriverBits is how many bits a sample the ASIO driver takes, and
	// DriverFloat says they are floating point; DriverBuffer is its
	// buffer, in samples.
	DriverBits   int
	DriverFloat  bool
	DriverBuffer int
}

// outputState returns how the deck's speakers play now.
func (d *deck) outputState() OutputState {
	d.mu.Lock()
	spk, problem := d.spk, d.spkErr
	d.mu.Unlock()
	if spk == nil {
		s := OutputState{Silent: true}
		if problem != nil {
			s.Problem = problem.Error()
		}
		return s
	}
	st := spk.State()
	s := OutputState{Driver: st.Driver, Rate: st.Rate, DeviceRate: st.DeviceRate, Latency: st.Latency,
		Frames: st.Frames, Dropouts: st.Dropouts}
	if st.Problem != nil {
		s.Problem = st.Problem.Error()
	}
	if st.ASIO != nil {
		s.DriverBits, s.DriverFloat, s.DriverBuffer = st.ASIO.Bits, st.ASIO.Float, st.ASIO.Buffer
	}
	return s
}

// openSpeaker opens the speakers; a test puts a function of its own
// here, to keep them shut.
var openSpeaker = speaker.Open

// openOutput opens the speakers as o says, or, open already, has them
// play so.
func (d *deck) openOutput(o Output) error {
	d.mu.Lock()
	spk := d.spk
	d.mu.Unlock()
	if spk != nil {
		return spk.Set(o.options())
	}
	spk, err := openSpeaker(d.mix, o.options())
	d.mu.Lock()
	defer d.mu.Unlock()
	d.spk, d.spkErr = spk, err
	return err
}

// outputChanged returns a channel that receives as the speakers open
// again, at a rate of their own, or nil while there are none.
func (d *deck) outputChanged() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.spk == nil {
		return nil
	}
	return d.spk.Changed()
}

// controlPanel opens the ASIO driver's own settings.
func (d *deck) controlPanel() error {
	d.mu.Lock()
	spk := d.spk
	d.mu.Unlock()
	if spk == nil {
		return nil
	}
	return spk.ControlPanel()
}

// playAgain plays the track playing again from where it is, as the
// speakers open again at another rate: its sound, made for the rate
// before, is made anew.
func (a *app) playAgain() {
	t := a.track(a.Current)
	if t == nil || a.d.done() == nil {
		return
	}
	at, _, id := a.d.position()
	if id != t.ID {
		return
	}
	a.queued = queuedKey{}
	if err := a.d.play(t.ID, t.File, a.gapOf(t), t.Edit, a.rackOf(t), at, !a.Playing, 15*time.Millisecond); err != nil {
		a.Note = err.Error()
	}
	a.queueNext()
}
