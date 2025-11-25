package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/term"
)

// PlayList displays the list of songs to be played.
type PlayList struct {
	app *App

	cursor int // The UI cursor
	offset int
}

// NewPlayList creates a new instance of PlayList.
func NewPlayList(app *App) *PlayList {
	return &PlayList{app: app}
}

// Init for PlayList does nothing.
func (p *PlayList) Init() {}

// HandleKey handles user input for the playlist.
func (p *PlayList) HandleKey(key rune) (Page, error) {
	switch key {
	case '\x1b': // ESC
		return nil, fmt.Errorf("user quit")
	case KeyArrowUp:
		if p.cursor > 0 {
			p.cursor--
		}
	case KeyArrowDown:
		if p.cursor < len(p.app.Playlist)-1 {
			p.cursor++
		}
	}
	p.View() // Redraw on any key press
	return nil, nil
}

// HandleSignal redraws the view on resize.
func (p *PlayList) HandleSignal(sig os.Signal) error {
	if sig == syscall.SIGWINCH {
		p.View()
	}
	return nil
}

// View renders the playlist.
func (p *PlayList) View() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H") // Clear screen

	// Title
	title := "Playlist"
	fmt.Printf("\x1b[1;1H\x1b[K\x1b[1m%s\x1b[0m", title)

	if len(p.app.Playlist) == 0 {
		msg := "Playlist is empty. Add songs from the Library tab."
		fmt.Printf("\x1b[3;1H%s", msg)
		return
	}

	// Adjust offset for scrolling
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+h-2 {
		p.offset = p.cursor - h + 3
	}

	// Draw playlist items
	for i := 0; i < h-2; i++ {
		trackIndex := p.offset + i
		if trackIndex >= len(p.app.Playlist) {
			break
		}

		trackPath := p.app.Playlist[trackIndex]
		trackName := filepath.Base(trackPath)

		// Styling
		style := "\x1b[0m" // Reset
		if trackIndex == p.cursor {
			style += "\x1b[7m" // Reverse video for cursor
		}

		line := fmt.Sprintf("  %s", trackName)
		if len(line) > w {
			line = line[:w]
		}

		fmt.Printf("\x1b[%d;1H\x1b[K%s%s\x1b[0m", i+2, style, line)
	}
}

// Tick for PlayList does nothing.
func (p *PlayList) Tick() {}
