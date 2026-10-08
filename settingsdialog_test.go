package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/speaker"
)

func TestTheSoundAndThemeAreKeptInTheSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if got := readSettings(path).Output; got != firstOutput || !got.Dither {
		t.Fatalf("with no settings yet the sound is %+v, want %+v", got, firstOutput)
	}
	was := openSpeaker
	openSpeaker = func(*audio.Mixer, speaker.Options) (*speaker.Speaker, error) { return nil, errors.New("shut") }
	defer func() { openSpeaker = was }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	a.settingsFile = path
	want := Output{Rate: 44100, Bits: 16, Dither: false, Fixed: true, Buffer: 4096}
	a.handle(SetOutput{Output: want})
	a.handle(ChooseTheme{Name: "dim"})
	st := readSettings(path)
	if st.Output != want || st.Theme != "dim" {
		t.Fatalf("kept %+v and theme %q, want %+v and dim", st.Output, st.Theme, want)
	}
	a.handle(ChooseTheme{Name: "a theme of a later version"})
	if got := readSettings(path).Theme; got != themeName {
		t.Errorf("a theme unknown is kept as %q, want %q", got, themeName)
	}
}

func TestTheSoundsOptionsFollowTheSettings(t *testing.T) {
	o := Output{Rate: 44100, Bits: 24, Dither: true}.options()
	if o.Rate != 44100 || o.Bits != 24 || !o.Dither || o.Fixed || o.Latency != autoLatency {
		t.Errorf("auto: %+v", o)
	}
	o = Output{Rate: 48000, Fixed: true, Buffer: 4800}.options()
	if !o.Fixed || o.Latency != 100*time.Millisecond {
		t.Errorf("4800 samples at 48 kHz: %+v", o)
	}
}

func TestTheSettingsDialogSendsWhatItShows(t *testing.T) {
	d := SettingsDraft{Output: Output{Driver: "Interface ASIO", Rate: 44100, Bits: 16, Dither: true, Fixed: true, Buffer: 2048},
		Drivers: []string{"Other ASIO", "Interface ASIO"}, Theme: "contrast"}
	s := newSettingsDialog(d)
	if got := s.output(); got != d.Output {
		t.Errorf("the dialog sends %+v, want %+v", got, d.Output)
	}
	if s.buffer.Disabled || s.dither.Disabled || s.look.Selected() != 2 {
		t.Errorf("buffer disabled %v, dither disabled %v, theme %d", s.buffer.Disabled, s.dither.Disabled, s.look.Selected())
	}
	// An auto buffer shows the size it has grown to, to hold once
	// Auto is unticked; floats take no dither.
	d.Output = Output{Rate: 48000}
	d.Now = OutputState{Rate: 48000, Frames: 8000, Latency: 166 * time.Millisecond}
	s = newSettingsDialog(d)
	if !s.buffer.Disabled || !s.dither.Disabled || bufferSizes[s.buffer.Selected()] != 8192 {
		t.Errorf("auto: buffer disabled %v at %d, dither disabled %v", s.buffer.Disabled, bufferSizes[s.buffer.Selected()], s.dither.Disabled)
	}
	if got := s.output(); got.Bits != 0 || got.Fixed || got.Driver != "" {
		t.Errorf("auto, floats, the system's sound: the dialog sends %+v", got)
	}
}

func TestTheSettingsSayWhereTheSystemResamples(t *testing.T) {
	d := SettingsDraft{Now: OutputState{Rate: 44100, DeviceRate: 48000}}
	if got := soundPath(d); !strings.Contains(got, "resamples it to 48 kHz") || !strings.Contains(got, "44.1 kHz") {
		t.Errorf("resampled: %q", got)
	}
	d.Drivers = []string{"ASIO4ALL"}
	if got := soundPath(d); !strings.Contains(got, "ASIO driver") {
		t.Errorf("with a driver at hand: %q", got)
	}
	d.Now = OutputState{Rate: 44100, Driver: "ASIO4ALL", DriverBits: 24, DriverBuffer: 256}
	if got := soundPath(d); got != "ASIO4ALL plays it at 44.1 kHz, in 24-bit integer samples, 256 at a time." {
		t.Errorf("through a driver: %q", got)
	}
	if got := soundNow(OutputState{Frames: 7200, Latency: 150 * time.Millisecond, Dropouts: 3}); got != "The buffer is 7200 samples now, 150 ms. The sound broke up 3 times." {
		t.Errorf("now: %q", got)
	}
}

func TestADriverThatFailedSaysWhatPlaysInItsPlace(t *testing.T) {
	d := SettingsDraft{Output: Output{Driver: "Realtek ASIO"}, Drivers: []string{"Realtek ASIO"},
		Now: OutputState{Rate: 48000, Problem: "asio: starting Realtek ASIO: the driver refused to start"}}
	if got := soundProblem(d); !strings.HasSuffix(got, systemSound()+" plays in its place.") {
		t.Errorf("the problem says %q", got)
	}
	// Its settings open all the same, as a change there may let it start.
	if s := newSettingsDialog(d); s.panel.Disabled {
		t.Error("the settings of a driver that failed are out of reach")
	}
	d.Output.Driver = ""
	if got := soundProblem(d); strings.Contains(got, "in its place") {
		t.Errorf("with the system's sound chosen, the problem says %q", got)
	}
}

func TestALookForUpdatesSaysHowItWent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	a.checking = true
	a.tookCheck(checked{err: errors.New("no network")})
	if a.checking || a.checked != "Couldn't check: no network" {
		t.Fatalf("after a failed look: checking %v, %q", a.checking, a.checked)
	}
	a.tookCheck(checked{})
	if !strings.HasSuffix(a.checked, "is up to date.") {
		t.Fatalf("with nothing newer: %q", a.checked)
	}
	d := a.settingsDraft()
	if d.Checked != a.checked {
		t.Fatalf("the dialog is told %q", d.Checked)
	}
	// The button waits for the studio to be installed, and for a look
	// under way.
	if s := newSettingsDialog(SettingsDraft{}); !s.check.Disabled {
		t.Error("a studio not installed offers to check")
	}
	if s := newSettingsDialog(SettingsDraft{Installed: true}); s.check.Disabled {
		t.Error("an installed studio offers no check")
	}
	if s := newSettingsDialog(SettingsDraft{Installed: true, Checking: true}); !s.check.Disabled || s.installNote.Text != "Checking for updates…" {
		t.Errorf("while checking: disabled %v, %q", s.check.Disabled, s.installNote.Text)
	}
}

func TestAFailedDriverAsksUntilAnswered(t *testing.T) {
	to := fallbackToast("Realtek ASIO", "starting Realtek ASIO: the driver refused to start")
	if to.Title != "Realtek ASIO didn't start" || !strings.Contains(to.Body, systemSound()+" plays in its place") ||
		len(to.Buttons) != 2 || to.Buttons[0].OnClick(false, nil) != (UseSystemSound{}) ||
		to.Buttons[1].OnClick(false, nil) != (OpenControlPanel{}) {
		t.Fatalf("the toast says %q, %q, with %d buttons", to.Title, to.Body, len(to.Buttons))
	}
	a := album()
	a.Fallback, a.FallbackWhy, a.Fallbacks = "Realtek ASIO", "the driver refused to start", 1
	w, r, run := stage(t, a)
	run(10)
	if r.toasts.Len() != 1 {
		t.Fatalf("with the driver failed, %d toasts show", r.toasts.Len())
	}
	// The driver plays, or another output is chosen: the toast goes.
	a.Fallback, a.FallbackWhy = "", ""
	if err := w.Client().Publish(albumTopic, a); err != nil {
		t.Fatal(err)
	}
	run(120)
	if r.toasts.Len() != 0 {
		t.Fatalf("with the driver playing, %d toasts show", r.toasts.Len())
	}
}

func TestUsingTheSystemsSoundLetsTheDriverGo(t *testing.T) {
	was := openSpeaker
	openSpeaker = func(*audio.Mixer, speaker.Options) (*speaker.Speaker, error) { return nil, errors.New("shut") }
	defer func() { openSpeaker = was }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	a.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	a.output = Output{Driver: "Realtek ASIO", Rate: 44100}
	a.Fallback, a.Fallbacks = "Realtek ASIO", 1
	a.handle(UseSystemSound{})
	if a.output.Driver != "" || a.output.Rate != 44100 || readSettings(a.settingsFile).Output.Driver != "" {
		t.Fatalf("the sound plays %+v", a.output)
	}
	if a.Fallback != "" {
		t.Errorf("the toast of %q still asks", a.Fallback)
	}
}
