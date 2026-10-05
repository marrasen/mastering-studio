package main

import (
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// scanCache keeps what reading each file through told, its waveform and
// spectrogram above all, in a folder, so an album opens with its tracks
// drawn at once rather than read again. A file is known by its path,
// size and time: changed, it is read anew. The folder keeps the files
// read last, keptScans of them.
type scanCache struct{ dir string }

// keptScans is how many files' scans the cache keeps.
const keptScans = 200

// scanCacheDir is where the scans are kept, or "" where there is
// nowhere.
func scanCacheDir() string {
	if d := cacheDir(); d != "" {
		return filepath.Join(d, "scans")
	}
	return ""
}

// cacheDir is where the studio keeps what it can make again, or "".
func cacheDir() string {
	d, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "marras-mastering-studio")
}

// file is where the scan of the file at path, as it is now, is kept,
// or "".
func (c scanCache) file(path string) string {
	if c.dir == "" {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d\x00%d", abs, fi.Size(), fi.ModTime().UnixNano()))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:16])+".scan")
}

// get returns the scan kept of the file at path, as it is now.
func (c scanCache) get(path string) (scan, bool) {
	at := c.file(path)
	if at == "" {
		return scan{}, false
	}
	f, err := os.Open(at)
	if err != nil {
		return scan{}, false
	}
	defer func() { _ = f.Close() }()
	var sc scan
	if gob.NewDecoder(f).Decode(&sc) != nil || sc.Wave == nil {
		return scan{}, false
	}
	// Used now, it is kept the longer.
	now := time.Now()
	_ = os.Chtimes(at, now, now)
	return sc, true
}

// put keeps sc, the scan of the file at path, and lets go of the scans
// used longest ago, past keptScans.
func (c scanCache) put(path string, sc scan) {
	at := c.file(path)
	if at == "" || os.MkdirAll(c.dir, 0o755) != nil {
		return
	}
	tmp, err := os.CreateTemp(c.dir, "*.tmp")
	if err != nil {
		return
	}
	err = gob.NewEncoder(tmp).Encode(sc)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), at)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	c.prune()
}

// prune lets go of the scans used longest ago, past keptScans.
func (c scanCache) prune() {
	ents, err := os.ReadDir(c.dir)
	if err != nil || len(ents) <= keptScans {
		return
	}
	type kept struct {
		name string
		used time.Time
	}
	var all []kept
	for _, e := range ents {
		if fi, err := e.Info(); err == nil && filepath.Ext(e.Name()) == ".scan" {
			all = append(all, kept{e.Name(), fi.ModTime()})
		}
	}
	slices.SortFunc(all, func(a, b kept) int { return b.used.Compare(a.used) })
	for _, k := range all[min(keptScans, len(all)):] {
		_ = os.Remove(filepath.Join(c.dir, k.name))
	}
}
