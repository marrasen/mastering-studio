package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// MP3s are encoded by LAME, where it is installed: no MP3 encoder in
// Go matches it, nor shares gunim's license. The app hands it the
// sound as 16-bit samples, dithered, and the tags, and LAME writes
// the file.

// findLAME returns LAME's path: set, where it is there, or found on the
// PATH or where its installers put it; or "" where it is nowhere.
func findLAME(set string) string {
	if set != "" {
		if fi, err := os.Stat(set); err == nil && !fi.IsDir() {
			return set
		}
	}
	if p, err := exec.LookPath("lame"); err == nil {
		return p
	}
	if runtime.GOOS == "windows" {
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			d := os.Getenv(env)
			if d == "" {
				continue
			}
			for _, sub := range []string{"LAME", "Lame For Audacity", filepath.Join("Programs", "LAME")} {
				p := filepath.Join(d, sub, "lame.exe")
				if _, err := os.Stat(p); err == nil {
					return p
				}
			}
		}
	}
	return ""
}

// lamePath is the LAME MP3s are encoded by, or "".
var lamePath string

// useLAME has MP3s encoded by the LAME at path, or none where path is
// "".
func useLAME(path string) {
	lamePath = path
	if path == "" {
		encodeMP3 = nil
		return
	}
	encodeMP3 = func(ctx context.Context, w io.WriteSeeker, rate, kbps int, tags trackTags) (soundWriter, error) {
		return newLAMEWriter(ctx, path, w, rate, kbps, tags)
	}
}

// lameArgs are LAME's arguments: raw stereo 16-bit samples at rate in
// from its input, an MP3 of kbps out to its output, tagged.
func lameArgs(rate, kbps int, t trackTags) []string {
	args := []string{"--quiet", "-r", "-s", strconv.FormatFloat(float64(rate)/1000, 'f', -1, 64), "--bitwidth", "16",
		"--signed", "--little-endian", "-m", "j", "--cbr", "-b", strconv.Itoa(kbps), "-q", "2", "--add-id3v2"}
	tag := func(flag, v string) {
		if v != "" {
			args = append(args, flag, v)
		}
	}
	tag("--tt", t.Title)
	tag("--ta", t.Artist)
	tag("--tl", t.Album)
	tag("--ty", t.Year)
	tag("--tg", t.Genre)
	if t.Track > 0 {
		tag("--tn", fmt.Sprintf("%d/%d", t.Track, t.Total))
	}
	return append(args, "-", "-")
}

// lameWriter runs LAME, the sound in through a pipe, the MP3 out to a
// file.
type lameWriter struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	stderr bytes.Buffer
	buf    []byte
	rng    *rand.Rand
}

// newLAMEWriter starts LAME, until it finishes or ctx ends.
func newLAMEWriter(ctx context.Context, path string, out io.Writer, rate, kbps int, tags trackTags) (*lameWriter, error) {
	w := &lameWriter{rng: rand.New(rand.NewPCG(0x6c61, 0x6d65))}
	w.cmd = exec.CommandContext(ctx, path, lameArgs(rate, kbps, tags)...)
	w.cmd.Stdout = out
	w.cmd.Stderr = &w.stderr
	quiet(w.cmd)
	in, err := w.cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	w.in = in
	if err := w.cmd.Start(); err != nil {
		return nil, fmt.Errorf("LAME: %w", err)
	}
	return w, nil
}

// Write implements [soundWriter]: frames as 16-bit samples, dithered.
func (w *lameWriter) Write(frames []float32) error {
	w.buf = w.buf[:0]
	for _, s := range frames {
		v := math.Round(float64(s)*32768 + w.rng.Float64() - w.rng.Float64())
		w.buf = binary.LittleEndian.AppendUint16(w.buf, uint16(int16(max(-32768, min(v, 32767)))))
	}
	if _, err := w.in.Write(w.buf); err != nil {
		return w.failed(err)
	}
	return nil
}

// Close implements [soundWriter]: LAME finishes the file.
func (w *lameWriter) Close() error {
	err := w.in.Close()
	if werr := w.cmd.Wait(); werr != nil {
		return w.failed(werr)
	}
	return err
}

// failed is err, with what LAME said.
func (w *lameWriter) failed(err error) error {
	if said := strings.TrimSpace(w.stderr.String()); said != "" {
		return errors.New("LAME: " + said)
	}
	return fmt.Errorf("LAME: %w", err)
}
