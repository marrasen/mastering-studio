package main

import (
	"fmt"
	"log"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/asio"
	"github.com/marrasen/gunim/install"
	"github.com/marrasen/gunim/widget"
)

// The settings' dialog: how the studio plays its sound, how it looks,
// and how it takes newer releases. Each change applies as it is made.

type (
	// SettingsDraft is what the settings' dialog shows.
	SettingsDraft struct {
		// Output is how the studio plays, Drivers the ASIO drivers it
		// can play through, and Now how it plays at this moment.
		Output  Output
		Drivers []string
		Now     OutputState
		// Theme is the theme's name.
		Theme string
		// Installed says the studio is installed, Updates how it takes
		// newer releases, and Beta that it takes pre-releases too.
		Installed bool
		Updates   string
		Beta      bool
	}

	// OpenSettings opens the settings' dialog.
	OpenSettings struct{}
	// SettingsClosed says the settings' dialog closed.
	SettingsClosed struct{}
	// ChooseTheme paints the studio in the theme named.
	ChooseTheme struct{ Name string }
	// SetUpdates sets how the installed studio takes newer releases, as
	// install.UpdateMode names it.
	SetUpdates struct{ Mode string }
)

// settingsTopic is what the settings' dialog watches.
const settingsTopic = "settings"

// updateModes are the ways to take newer releases the settings offer,
// as they name them.
var updateModes = []struct {
	mode install.UpdateMode
	name string
}{
	{install.UpdatesInstall, "Fetch and install them"},
	{install.UpdatesNotify, "Ask first"},
	{install.UpdatesOff, "Look for none"},
}

// settingsDialog is the settings' dialog, a tab for each part.
type settingsDialog struct {
	*widget.Dialog
	draft SettingsDraft
	// The sound's.
	driver, rate, bits, buffer *widget.Dropdown
	dither, auto               *widget.Checkbox
	panel                      *widget.Button
	now, path, problem         *widget.Label
	// The look's, and the updates'.
	look, updates *widget.Dropdown
	beta          *widget.Checkbox
	installNote   *widget.Label
}

func newSettingsDialog(d SettingsDraft) *settingsDialog {
	dlg := widget.NewDialog("Settings")
	dlg.Width = 600
	s := &settingsDialog{Dialog: dlg, draft: d}
	set := func() gunim.Intent { return SetOutput{Output: s.output()} }

	s.driver = widget.NewDropdown(widget.Labels(append([]string{systemSound()}, d.Drivers...)...))
	s.driver.Label = "Output"
	s.driver.SetSelected(slices.Index(d.Drivers, d.Output.Driver)+1, nil)
	s.driver.OnChange = func(_ int, _ *gunim.UI) gunim.Intent { return set() }
	s.panel = widget.NewButton("Driver settings…")
	s.panel.OnClick = widget.Sends(OpenControlPanel{})

	rates := make([]string, len(outputRates))
	for i, r := range outputRates {
		rates[i] = hz(r)
	}
	s.rate = widget.NewDropdown(widget.Labels(rates...))
	s.rate.Label = "Sample rate"
	s.rate.SetSelected(max(0, slices.Index(outputRates, d.Output.rate())), nil)
	s.rate.OnChange = func(_ int, _ *gunim.UI) gunim.Intent { return set() }

	bits := make([]string, len(bitsChoices))
	for i, c := range bitsChoices {
		bits[i] = c.name
	}
	s.bits = widget.NewDropdown(widget.Labels(bits...))
	s.bits.Label = "Bit depth"
	s.bits.SetSelected(max(0, slices.IndexFunc(bitsChoices, func(c bitsChoice) bool { return c.bits == d.Output.bits() })), nil)
	s.bits.OnChange = func(_ int, _ *gunim.UI) gunim.Intent { return set() }
	s.dither = widget.NewCheckbox("Dither")
	s.dither.SetChecked(d.Output.Dither, nil)
	s.dither.OnChange = func(_ bool, _ *gunim.UI) gunim.Intent { return set() }

	s.auto = widget.NewCheckbox("Auto: grow it each time the sound breaks up")
	s.auto.SetChecked(!d.Output.Fixed, nil)
	s.auto.OnChange = func(_ bool, _ *gunim.UI) gunim.Intent { return set() }
	s.buffer = widget.NewDropdown(widget.Labels(bufferNames(d.Output.rate())...))
	s.buffer.Label = "Buffer size"
	s.buffer.OnChange = func(_ int, _ *gunim.UI) gunim.Intent { return set() }
	s.buffer.SetSelected(nearestBuffer(d.Output.Buffer), nil)

	s.now, s.path, s.problem = widget.NewLabel(""), widget.NewLabel(""), widget.NewLabel("")
	s.problem.Color = widget.DialogProblem
	// The panel's button shows where there is a driver to choose.
	output := widget.Row(s.driver)
	if len(d.Drivers) > 0 {
		output = widget.Row(s.driver, s.panel)
	}
	sound := widget.NewForm()
	sound.Add("Output", output).
		Add("Sample rate", widget.Row(s.rate)).
		Add("Bit depth", widget.Row(s.bits, s.dither)).
		Add("Buffer", s.auto).
		Add("", widget.Row(s.buffer))
	soundPage := widget.Column(sound, s.now, s.path, s.problem)

	labels := make([]string, len(studioThemes))
	for i, t := range studioThemes {
		labels[i] = t.Label
	}
	s.look = widget.NewDropdown(widget.Labels(labels...))
	s.look.Label = "Theme"
	s.look.SetSelected(max(0, slices.IndexFunc(studioThemes, func(t studioTheme) bool { return t.Name == d.Theme })), nil)
	s.look.OnChange = func(i int, _ *gunim.UI) gunim.Intent { return ChooseTheme{Name: studioThemes[i].Name} }
	lookPage := widget.NewForm().Add("Theme", widget.Row(s.look))

	modes := make([]string, len(updateModes))
	for i, m := range updateModes {
		modes[i] = m.name
	}
	s.updates = widget.NewDropdown(widget.Labels(modes...))
	s.updates.Label = "Newer releases"
	s.updates.OnChange = func(i int, _ *gunim.UI) gunim.Intent { return SetUpdates{Mode: string(updateModes[i].mode)} }
	s.beta = widget.NewCheckbox("Beta: take pre-releases too")
	s.beta.SetChecked(d.Beta, nil)
	s.beta.OnChange = func(on bool, _ *gunim.UI) gunim.Intent { return SetBeta{On: on} }
	s.installNote = widget.NewLabel("")
	updates := widget.NewForm().Add("Newer releases", widget.Row(s.updates)).Add("", s.beta)
	updatesPage := widget.Column(updates, s.installNote)

	dlg.Body = widget.NewTabs([]string{"Sound", "Look", "Updates"}, soundPage, lookPage, updatesPage)
	dlg.SetButtons("Done", "")
	dlg.OnAccept = widget.Sends(SettingsClosed{})
	dlg.OnDismiss = widget.Sends(SettingsClosed{})
	s.fill(d, nil)
	return s
}

// show takes the draft anew, as the sound plays on and settings change.
func (s *settingsDialog) show(d SettingsDraft, u *gunim.UI) {
	if d.Output.rate() != s.draft.Output.rate() {
		s.buffer.SetItems(widget.Labels(bufferNames(d.Output.rate())...))
	}
	s.draft = d
	s.fill(d, u)
	u.Invalidate()
}

// fill sets what follows from the draft: which choices apply, the
// buffer an auto buffer has grown to, and what the sound does now.
func (s *settingsDialog) fill(d SettingsDraft, u *gunim.UI) {
	// A driver that failed to start opens its settings too: a change
	// there may let it.
	s.panel.Disabled = d.Output.Driver == ""
	s.dither.Disabled = d.Output.bits() == 32
	s.buffer.Disabled = !d.Output.Fixed
	if !d.Output.Fixed && d.Now.Frames > 0 {
		s.buffer.SetSelected(nearestBuffer(int(d.Now.Frames)), u)
	}
	i := slices.IndexFunc(updateModes, func(m struct {
		mode install.UpdateMode
		name string
	}) bool {
		return string(m.mode) == d.Updates
	})
	s.updates.SetSelected(max(0, i), u)
	s.updates.Disabled = !d.Installed
	s.installNote.Text = ""
	if !d.Installed {
		s.installNote.Text = "The studio takes newer releases once it is installed."
	}
	s.now.Text, s.path.Text, s.problem.Text = soundNow(d.Now), soundPath(d), soundProblem(d)
}

// output returns the sound's settings as the dialog shows them.
func (s *settingsDialog) output() Output {
	o := Output{Rate: outputRates[max(0, min(s.rate.Selected(), len(outputRates)-1))], Dither: s.dither.Checked(),
		Fixed: !s.auto.Checked(), Buffer: bufferSizes[max(0, min(s.buffer.Selected(), len(bufferSizes)-1))]}
	if i := s.driver.Selected() - 1; i >= 0 && i < len(s.draft.Drivers) {
		o.Driver = s.draft.Drivers[i]
	}
	if b := bitsChoices[max(0, min(s.bits.Selected(), len(bitsChoices)-1))].bits; b != 32 {
		o.Bits = b
	}
	return o
}

// bits returns the bits o plays in, 32 for floats.
func (o Output) bits() int {
	if o.Bits == 16 || o.Bits == 24 {
		return o.Bits
	}
	return 32
}

// systemSound names the system's own sound, as the output choices
// list it.
func systemSound() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows sound"
	case "linux":
		return "PulseAudio or PipeWire"
	}
	return "System sound"
}

// hz writes a rate as kilohertz.
func hz(r int) string {
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(r)/1000), ".0") + " kHz"
}

// bufferNames names the buffer sizes, in samples and in time at rate.
func bufferNames(rate int) []string {
	out := make([]string, len(bufferSizes))
	for i, n := range bufferSizes {
		out[i] = fmt.Sprintf("%d samples · %s", n, ms(time.Duration(int64(n)*int64(time.Second)/int64(rate))))
	}
	return out
}

// ms writes d as whole milliseconds.
func ms(d time.Duration) string {
	return fmt.Sprintf("%d ms", d.Round(time.Millisecond)/time.Millisecond)
}

// nearestBuffer returns the buffer size nearest n samples, by its
// place.
func nearestBuffer(n int) int {
	best := 0
	for i, b := range bufferSizes {
		if abs(b-n) < abs(bufferSizes[best]-n) {
			best = i
		}
	}
	return best
}

func abs(n int) int { return max(n, -n) }

// soundNow says how far ahead the sound plays now, and how often it
// broke up.
func soundNow(n OutputState) string {
	if n.Silent {
		return "No sound plays."
	}
	s := fmt.Sprintf("The buffer is %d samples now, %s.", n.Frames, ms(n.Latency))
	switch n.Dropouts {
	case 0:
	case 1:
		s += " The sound broke up once."
	default:
		s += fmt.Sprintf(" The sound broke up %d times.", n.Dropouts)
	}
	return s
}

// soundPath says what the sound passes through on its way out, and
// where the system resamples it.
func soundPath(d SettingsDraft) string {
	n := d.Now
	if n.Silent {
		return ""
	}
	if n.Driver != "" {
		kind := fmt.Sprintf("%d-bit integer", n.DriverBits)
		if n.DriverFloat {
			kind = fmt.Sprintf("%d-bit float", n.DriverBits)
		}
		return fmt.Sprintf("%s plays it at %s, in %s samples, %d at a time.", n.Driver, hz(n.Rate), kind, n.DriverBuffer)
	}
	if n.DeviceRate != 0 && n.DeviceRate != n.Rate {
		where := "the system's sound settings"
		if runtime.GOOS == "windows" {
			where = "Windows' sound settings, under the device's advanced properties"
		}
		s := fmt.Sprintf("The system resamples it to %s for the device. To hear %s as it is, set the device to %s in %s",
			hz(n.DeviceRate), hz(n.Rate), hz(n.Rate), where)
		if len(d.Drivers) > 0 {
			s += ", or choose an ASIO driver"
		}
		return s + "."
	}
	return fmt.Sprintf("The system plays it at %s.", hz(n.Rate))
}

// soundProblem says why the output chosen plays otherwise, and, where
// the system's sound plays in place of a driver, says so.
func soundProblem(d SettingsDraft) string {
	p := d.Now.Problem
	if p != "" && d.Output.Driver != "" && d.Now.Driver == "" && !d.Now.Silent {
		p += ". " + systemSound() + " plays in its place."
	}
	return p
}

// The app's half of the dialog.

// settingsDraft is the settings' dialog's state.
func (a *app) settingsDraft() SettingsDraft {
	return SettingsDraft{Output: a.output, Drivers: a.drivers, Now: a.d.outputState(), Theme: a.theme,
		Installed: a.installed != nil, Updates: a.updatesMode(), Beta: a.Beta}
}

// updatesMode is how the installed studio takes newer releases.
func (a *app) updatesMode() string {
	if a.installed == nil {
		return ""
	}
	return string(a.installed.Updates)
}

// openSettings opens the settings' dialog, or closes it.
func (a *app) openSettings(open bool) {
	if a.c == nil || open == a.settingsOpen {
		return
	}
	a.settingsOpen = open
	var err error
	if open {
		a.drivers = nil
		if ds, derr := asio.Drivers(); derr == nil {
			for _, d := range ds {
				a.drivers = append(a.drivers, d.Name)
			}
		}
		a.installed, _ = install.Find(installer())
		a.settingsTick = time.NewTicker(500 * time.Millisecond)
		err = a.c.Mount(gunim.Root, "settings", "settings", a.settingsDraft(), settingsTopic)
	} else {
		a.settingsTick.Stop()
		a.settingsTick = nil
		err = a.c.Unmount("settings")
	}
	if err != nil {
		log.Print(err)
	}
}

// tellFallback tells, as a note, of a driver chosen that failed to
// start, while the system's sound plays in its place.
func (a *app) tellFallback() {
	if a.output.Driver == "" {
		return
	}
	if st := a.d.outputState(); st.Driver == "" && !st.Silent {
		a.Note = fmt.Sprintf("%s didn't start, so %s plays. Settings says why.", a.output.Driver, systemSound())
	}
}

// ticks returns the channel the settings' dialog is told anew on, or
// nil while it is closed.
func (a *app) ticks() <-chan time.Time {
	if a.settingsTick == nil {
		return nil
	}
	return a.settingsTick.C
}

// handleSettings takes what the settings' dialog sends.
func (a *app) handleSettings(in gunim.Intent) {
	switch in := in.(type) {
	case OpenSettings:
		a.openSettings(true)
	case SettingsClosed:
		a.openSettings(false)
	case SetOutput:
		a.output = in.Output
		a.writeSettings()
		if err := a.d.openOutput(a.output); err != nil {
			a.Note = "The sound: " + err.Error()
		}
		a.tellFallback()
	case OpenControlPanel:
		// The driver's settings show until closed: the studio plays on.
		go func() {
			if err := a.d.controlPanel(); err != nil {
				log.Print(err)
			}
		}()
	case ChooseTheme:
		a.theme = themeNamed(in.Name)
		a.writeSettings()
		if a.c != nil {
			_ = a.c.SetTheme(a.theme)
		}
	case SetUpdates:
		if err := install.SetUpdates(installer(), install.UpdateMode(in.Mode)); err != nil {
			a.Note = err.Error()
		}
		a.installed, _ = install.Find(installer())
	}
}
