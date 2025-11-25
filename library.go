package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/term"
)

// Library browses the music directory and adds songs to the playlist.
type Library struct {
	app *App

	files    []string
	cursor   int
	selected map[int]bool // Using a map for quick lookups
	offset   int          // For scrolling the view
}

// NewLibrary creates a new instance of Library.
func NewLibrary(app *App) *Library {
	return &Library{
		app:      app,
		selected: make(map[int]bool),
	}
}

// Init scans the music directory for .flac files.
func (p *Library) Init() {
	p.files = make([]string, 0)
	// For now, scan the current directory.
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".flac") {
			p.files = append(p.files, path)
		}
		return nil
	})
	if err != nil {
		// For now, just log it. A real app might show this in the UI.
		// log.Printf("Error scanning directory: %v", err)
	}
}

// HandleKey handles user input for the library page.
func (p *Library) HandleKey(key rune) (Page, error) {
	switch key {
	case '\x1b': // ESC
		return nil, fmt.Errorf("user quit")
	case KeyArrowUp:
		if p.cursor > 0 {
			p.cursor--
		}
	case KeyArrowDown:
		if p.cursor < len(p.files)-1 {
			p.cursor++
		}
	case ' ': // Toggle selection and add/remove from playlist
		if p.cursor >= len(p.files) {
			break // No file at cursor position
		}
		
		filePath := p.files[p.cursor]
		// wasSelected := p.selected[p.cursor] // Store previous state, not strictly needed with new logic

		p.toggleSelection(p.cursor)

		if p.selected[p.cursor] { // If it's now selected
			// Add to playlist if not already present
			found := false
			for _, s := range p.app.Playlist {
				if s == filePath {
					found = true
					break
				}
			}
			if !found {
				p.app.Playlist = append(p.app.Playlist, filePath)
			}
		} else { // If it's now deselected
			p.removeSongFromPlaylist(filePath)
		}

		if p.cursor < len(p.files)-1 {
			p.cursor++
		}

	}
	p.View() // Redraw on any key press
	return nil, nil
}

// toggleSelection adds or removes a file index from the selection.
func (p *Library) toggleSelection(index int) {
	if p.selected[index] {
		delete(p.selected, index)
	} else {
		p.selected[index] = true
	}
}

// removeSongFromPlaylist removes the first occurrence of a song path from the app's playlist.
func (p *Library) removeSongFromPlaylist(songPath string) {
	for i, s := range p.app.Playlist {
		if s == songPath {
			p.app.Playlist = append(p.app.Playlist[:i], p.app.Playlist[i+1:]...)
			return // Remove only the first occurrence
		}
	}
}



// HandleSignal handles window resize events.
func (p *Library) HandleSignal(sig os.Signal) error {
	if sig == syscall.SIGWINCH {
		p.View()
	}
	return nil
}

// View renders the library file list.
func (p *Library) View() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H") // Clear screen

	// Title
	title := "Library - Press <space> to select, <enter> to add"
	fmt.Printf("\x1b[1;1H\x1b[K\x1b[1m%s\x1b[0m", title)

	// Make sure offset is valid
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+h-2 {
		p.offset = p.cursor - h + 3
	}

	// Draw files
	for i := 0; i < h-2; i++ {
		fileIndex := p.offset + i
		if fileIndex >= len(p.files) {
			break
		}

		line := p.files[fileIndex]
		// Truncate line if it's too long
		if len(line) > w {
			line = line[:w]
		}

		// Styling
		style := "\x1b[0m" // Reset
		if fileIndex == p.cursor {
			style += "\x1b[7m" // Reverse video for cursor
		}
		if p.selected[fileIndex] {
			style += "\x1b[32m" // Green text for selected
			line = "▸ " + line
		} else {
			line = "  " + line
		}

		fmt.Printf("\x1b[%d;1H\x1b[K%s%s\x1b[0m", i+2, style, line)
	}
}

// Tick for Library does nothing.
func (p *Library) Tick() {}
