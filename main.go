// Command mastering masters an album: its tracks, each cut, faded, and
// set apart from the one before by the same silence; played one at a
// time, a key or a click switching to another at the same moment, to
// compare; measured as they will be exported, loudness and true peak,
// as each edit settles; and exported one at a time, each at its own
// length, to WAV files of 16 or 24 bits, dithered, or 32-bit float.
//
// Each track runs through a chain of VST3 plugins of its own: only the
// track heard has its plugins running, and a track is measured and
// exported through a copy of its chain, run offline.
//
//	go run ./example/mastering
//	go run ./example/mastering mix1.wav mix2.wav
//
// Files dropped on the list join the album, which is kept between runs.
// Keys: Space plays and pauses, 1 to 9 pick a track, Up and Down step
// through them, Left and Right seek, Home starts over, M matches levels.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/speaker"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

func main() {
	state := flag.String("project", projectFile(), "the file the album is kept in; empty keeps nothing")
	shot := flag.String("shot", "", "write the window to this PNG file after -after, and quit")
	after := flag.Duration("after", 2*time.Second, "how long -shot waits")
	play := flag.Bool("play", false, "start playing the track picked")
	size := flag.String("size", "1440x900", "the window's size")
	plugins := flag.String("plugins", "", "more folders of VST3 plugins, beside the system's, as a list like PATH")
	flag.Parse()
	var w, h float32
	if _, err := fmt.Sscanf(*size, "%gx%g", &w, &h); err != nil || w <= 0 || h <= 0 {
		log.Fatalf("mastering: -size %q: want a width and a height, as 1440x900", *size)
	}
	o := options{file: *state, paths: flag.Args(), play: *play, shot: *shot, after: *after, size: geom.Sz(w, h)}
	if *plugins != "" {
		o.plugins = filepath.SplitList(*plugins)
	}
	if err := run(o); err != nil {
		log.Fatal(err)
	}
}

// options are how the program runs.
type options struct {
	// file is where the album is kept, paths the files to add, and play
	// says to start playing.
	file  string
	paths []string
	play  bool
	// shot is a PNG file to write the window to, after after.
	shot  string
	after time.Duration
	size  geom.Size
	// plugins are more folders of plugins.
	plugins []string
}

func run(o options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	mix := audio.NewMixer()
	d := newDeck(mix)
	// Mastering wants no quick answer from the sound: a buffer that rides
	// out a busy moment, the meters following it as heard.
	if spk, err := speaker.Open(mix, speaker.Options{Name: "gunim mastering", Latency: 150 * time.Millisecond}); err != nil {
		log.Printf("mastering: no sound: %v", err)
	} else {
		d.spk = spk
	}
	err := gunim.Main(ctx, func(a *gunim.App) error {
		w, err := a.NewWindow(gunim.WindowOptions{Title: "Mastering", Size: o.size})
		if err != nil {
			return fmt.Errorf("mastering: %w", err)
		}
		registerViews(w, d)
		c := w.Client()
		if o.shot != "" {
			go func() {
				select {
				case <-time.After(o.after):
				case <-ctx.Done():
					return
				}
				if err := writeShot(ctx, c, o.shot); err != nil {
					log.Print(err)
				}
				c.Close()
			}()
		}
		return serve(ctx, c, d, o)
	})
	if errors.Is(err, driver.ErrNoDriver) {
		log.Print("gunim has no driver for this operating system yet")
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// writeShot writes what the window shows to a PNG file.
func writeShot(ctx context.Context, c gunim.Client, path string) error {
	img, err := c.Shot(ctx)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return errors.Join(png.Encode(f, img), f.Close())
}
