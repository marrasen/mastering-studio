package main

import (
	"embed"
	"image"

	"github.com/marrasen/gunim/install"
)

// legal are the licence and the notices of the code the studio is made
// of, installed beside the program.
//
//go:embed LICENSE NOTICE
var legal embed.FS

// installer is how the studio installs itself. The program is all
// there is to download: started from anywhere but its own folder, it
// opens gunim's installer, which installs it for the user alone, with
// a Start menu or applications menu entry, an entry to uninstall it
// from, and projects that open in it with a double-click. Installed, it
// keeps itself up to date from the releases on GitHub, when the user
// chose that.
func installer() install.App {
	return install.App{
		Name:        appName,
		ID:          "mastering-studio",
		Version:     version,
		Publisher:   "Marcus Johansson",
		Description: "Masters an album, an EP or a single, through chains of VST3 plugins.",
		URL:         "https://github.com/marrasen/mastering-studio",
		IconFunc:    func() image.Image { return drawIcon(512) },
		Categories:  "AudioVideo;Audio;",
		Files:       legal,
		FileTypes:   []install.FileType{{Name: "Mastering project", Exts: []string{albumExt}, Default: true}},
		Updates:     install.GitHub{Repo: "marrasen/mastering-studio"},
		Data:        []string{configDir(), cacheDir()},
	}
}
