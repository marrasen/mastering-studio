// Command mastering masters an album: its tracks, each cut, faded, and
// set apart from the one before by the same silence; played one at a
// time, a key or a click switching to another at the same moment, to
// compare; measured as they will be exported, loudness and true peak,
// as each edit settles; and exported one at a time, each at its own
// length, to WAV files of 16 or 24 bits, dithered, or 32-bit float.
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
	flag.Parse()
	var w, h float32
	if _, err := fmt.Sscanf(*size, "%gx%g", &w, &h); err != nil || w <= 0 || h <= 0 {
		log.Fatalf("mastering: -size %q: want a width and a height, as 1440x900", *size)
	}
	if err := run(*state, flag.Args(), *play, *shot, *after, geom.Sz(w, h)); err != nil {
		log.Fatal(err)
	}
}

func run(file string, paths []string, play bool, shot string, after time.Duration, size geom.Size) error {
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
		w, err := a.NewWindow(gunim.WindowOptions{Title: "Mastering", Size: size})
		if err != nil {
			return fmt.Errorf("mastering: %w", err)
		}
		registerViews(w, d)
		c := w.Client()
		if shot != "" {
			go func() {
				select {
				case <-time.After(after):
				case <-ctx.Done():
					return
				}
				if err := writeShot(ctx, c, shot); err != nil {
					log.Print(err)
				}
				c.Close()
			}()
		}
		return serve(ctx, c, d, file, paths, play)
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
