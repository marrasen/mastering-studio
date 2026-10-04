package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marrasen/gunim/audio"
)

// An export's report: a text file beside the files, of what was
// exported and how, for the record and for whoever mixes the next
// version: the release and the export's settings, and each track's
// source, edit, chain, notes and loudness as written.

// reportHead is what a report says of the export as a whole.
type reportHead struct {
	at                 time.Time
	release            Release
	album              string
	wav, mp3           bool
	bits               int
	dither             bool
	kbps               int
	lame               string
	target             float32
	gap                time.Duration
	tracks, exportedOf int
}

// reportTrack is what a report says of a track, from the album as it
// was exported.
type reportTrack struct {
	number      int
	title, note string
	marks       []Mark
	source, rel string
	format      audio.Format
	edit        Edit
	gap         time.Duration
	ownGap      bool
	chain       []Slot
	base        string
	wav, mp3    bool
	out         Measure
	err         error
}

// reportName is the report's file's name, by when the export was.
func reportName(at time.Time) string {
	return "Export report " + at.Format("2006-01-02 1504") + ".txt"
}

// writeReport writes the report to dir.
func writeReport(dir string, h reportHead, ts []reportTrack) (string, error) {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	rule := strings.Repeat("=", 72)
	line("%s", rule)
	title := h.release.Title
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(h.album), filepath.Ext(h.album))
	}
	line("EXPORT REPORT  %s", title)
	line("%s", rule)
	line("Exported:      %s", h.at.Format("2006-01-02 15:04:05 MST"))
	line("Artist:        %s", orNone(h.release.Artist))
	line("Release:       %s", orNone(h.release.Title))
	line("Year:          %s", orNone(h.release.Year))
	line("Genre:         %s", orNone(h.release.Genre))
	line("Album file:    %s", orNone(h.album))
	line("Tracks:        %d exported of %d", h.exportedOf, h.tracks)
	var formats []string
	if h.wav {
		w := fmt.Sprintf("WAV %d-bit", h.bits)
		switch {
		case h.bits == 32:
			w = "WAV 32-bit float"
		case h.dither:
			w += ", TPDF dither"
		default:
			w += ", no dither"
		}
		formats = append(formats, w)
	}
	if h.mp3 {
		formats = append(formats, fmt.Sprintf("MP3 %d kbps CBR, LAME at %s", h.kbps, h.lame))
	}
	line("Formats:       %s", strings.Join(formats, "; "))
	line("Target:        %.1f LUFS", h.target)
	line("Silence:       %s before each track, but where a track has its own", secs(h.gap))
	line("Loudness:      ITU-R BS.1770 / EBU R128 as written; range by EBU Tech 3342")
	for _, t := range ts {
		line("")
		line("%s", strings.Repeat("-", 72))
		line("%02d  %s", t.number, t.title)
		line("%s", strings.Repeat("-", 72))
		if t.err != nil {
			line("NOT EXPORTED:  %v", t.err)
		}
		if t.note != "" {
			line("Note:          %s", t.note)
		}
		for _, m := range t.marks {
			line("Note at %s: %s", clock(m.At), m.Text)
		}
		line("Source:        %s", t.rel)
		if fi, err := os.Stat(t.source); err == nil {
			line("               %s, modified %s", size(fi.Size()), fi.ModTime().Format("2006-01-02 15:04:05"))
		} else {
			line("               not found: %v", err)
		}
		f := t.format
		res := fmt.Sprintf("%s, %d Hz", f.Name, f.SampleRate)
		if f.Bits > 0 {
			res += fmt.Sprintf(", %d-bit", f.Bits)
		}
		if f.Channels > 0 {
			res += fmt.Sprintf(", %d channels", f.Channels)
		}
		line("Format:        %s", res)
		gap := secs(t.gap)
		if t.ownGap {
			gap += " (its own)"
		}
		end := "the file's end"
		if t.edit.End > 0 {
			end = clock(t.edit.End)
		}
		line("Cut:           %s to %s, %s of silence before", clock(t.edit.Start), end, gap)
		line("Fades:         in %s %s, out %s %s", secs(t.edit.FadeIn.Length), curveNames[t.edit.FadeIn.Curve],
			secs(t.edit.FadeOut.Length), curveNames[t.edit.FadeOut.Curve])
		line("Gain:          in %+.1f dB, out %+.1f dB", t.edit.Gain, t.edit.Out)
		if len(t.chain) == 0 {
			line("Plugins:       none")
		}
		for i, s := range t.chain {
			label := "Plugins:      "
			if i > 0 {
				label = "              "
			}
			state := ""
			if s.Bypass {
				state = " (bypassed)"
			}
			line("%s %d. %s, %s%s", label, i+1, s.Name, orNone(s.Vendor), state)
		}
		if t.err != nil {
			continue
		}
		m := t.out
		line("Length:        %s, silence before included", clock(m.Length))
		lra := "none"
		if m.Ranged {
			lra = fmt.Sprintf("%.1f LU (%.1f to %.1f LUFS)", m.LRA, m.Low, m.High)
		}
		lufs := "silent"
		if m.Loud {
			lufs = fmt.Sprintf("%.1f LUFS, %+.1f LU from the target", m.LUFS, m.LUFS-h.target)
		}
		line("Loudness:      %s", lufs)
		line("Range:         %s", lra)
		line("Peak:          %.1f dBTP true peak, %.1f dBFS sample peak", dB(float64(m.TruePeak)), dB(float64(m.Peak)))
		for _, ext := range []string{".wav", ".mp3"} {
			if (ext == ".wav" && !t.wav) || (ext == ".mp3" && !t.mp3) {
				continue
			}
			p := t.base + ext
			if fi, err := os.Stat(p); err == nil {
				line("Written:       %s, %s", filepath.Base(p), size(fi.Size()))
			}
		}
	}
	path := filepath.Join(dir, reportName(h.at))
	return path, os.WriteFile(path, []byte(b.String()), 0o644)
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// secs writes d in seconds.
func secs(d time.Duration) string { return fmt.Sprintf("%.2f s", d.Seconds()) }

// size writes a file's size.
func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
