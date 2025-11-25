package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/term"
)

// Library browses the music directory and adds songs to the playlist.
type Library struct {
	app *App

	entries     []os.DirEntry
	currentPath string
	cursor      int
	selected    map[string]bool // Use file path as key for persistent selection
	offset      int             // For scrolling the view
}

// NewLibrary creates a new instance of Library.
func NewLibrary(app *App) *Library {
	return &Library{
		app:         app,
		currentPath: ".",
		selected:    make(map[string]bool),
	}
}

// scanDirectory reads the contents of a directory and populates the entries list.
func (p *Library) scanDirectory(path string) {
	p.entries = make([]os.DirEntry, 0)
	p.currentPath = path
	p.cursor = 0 // Reset cursor on directory change

	files, err := os.ReadDir(path)
	if err != nil {
		// In a real app, you might want to display this error in the UI
		return
	}

	for _, file := range files {
		// We are interested in directories and .flac files
		if file.IsDir() || strings.HasSuffix(strings.ToLower(file.Name()), ".flac") {
			p.entries = append(p.entries, file)
		}
	}
	// Sort so directories come first, then files
	sort.Slice(p.entries, func(i, j int) bool {
		if p.entries[i].IsDir() != p.entries[j].IsDir() {
			return p.entries[i].IsDir()
		}
		return p.entries[i].Name() < p.entries[j].Name()
	})
}

// Init initializes the library by scanning the starting directory.
func (p *Library) Init() {
	p.scanDirectory(p.currentPath)
}

// HandleKey handles user input for the library page.
func (p *Library) HandleKey(key rune) (Page, error) {
	switch key {
	case '\x1b': // ESC
		return nil, fmt.Errorf("user quit")
	case KeyArrowUp:
		if len(p.entries) > 0 {
			p.cursor = (p.cursor - 1 + len(p.entries)) % len(p.entries)
		}
	case KeyArrowDown:
		if len(p.entries) > 0 {
			p.cursor = (p.cursor + 1) % len(p.entries)
		}
	case KeyArrowRight:
		if p.cursor < len(p.entries) && p.entries[p.cursor].IsDir() {
			newPath := filepath.Join(p.currentPath, p.entries[p.cursor].Name())
			p.scanDirectory(newPath)
		}
	case KeyArrowLeft:
		if p.currentPath != "." {
			newPath := filepath.Dir(p.currentPath)
			p.scanDirectory(newPath)
		}
	case ' ':
		if p.cursor >= len(p.entries) {
			break
		}
		entry := p.entries[p.cursor]
		fullPath := filepath.Join(p.currentPath, entry.Name())

		if !entry.IsDir() {
			// It's a file, toggle its selection
			p.toggleSelection(fullPath)
			if p.selected[fullPath] {
				// Now selected, ensure it's in the playlist (without duplicates)
				found := false
				for _, s := range p.app.Playlist {
					if s == fullPath {
						found = true
						break
					}
				}
				if !found {
					p.app.Playlist = append(p.app.Playlist, fullPath)
				}
			} else {
				// Now deselected, remove it from the playlist
				p.removeSongFromPlaylist(fullPath)
			}
		} else {
			// It's a directory, toggle all files within it
			var songsInDir []string
			filepath.WalkDir(fullPath, func(path string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".flac") {
					songsInDir = append(songsInDir, path)
				}
				return nil
			})

			// If any song in the directory is not selected, select them all.
			// Otherwise, deselect them all.
			allSelected := true
			if len(songsInDir) > 0 {
				for _, songPath := range songsInDir {
					if !p.selected[songPath] {
						allSelected = false
						break
					}
				}
			} else {
				allSelected = false
			}


			for _, songPath := range songsInDir {
				if allSelected {
					// Deselect and remove from playlist
					delete(p.selected, songPath)
					p.removeSongFromPlaylist(songPath)
				} else {
					// Select and add to playlist
					if !p.selected[songPath] {
						p.selected[songPath] = true
						p.app.Playlist = append(p.app.Playlist, songPath)
					}
				}
			}
		}

		// Auto-advance cursor
		if p.cursor < len(p.entries)-1 {
			p.cursor++
		}
	}
	p.View() // Redraw on any key press
	return nil, nil
}

// toggleSelection adds or removes a file path from the selection.
func (p *Library) toggleSelection(path string) {
	if p.selected[path] {
		delete(p.selected, path)
	} else {
		p.selected[path] = true
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
	title := fmt.Sprintf("Library: %s - Use arrows to navigate, space to select", p.currentPath)
	fmt.Printf("\x1b[1;1H\x1b[K\x1b[1m%s\x1b[0m", title)

	// Make sure offset is valid
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+h-2 {
		p.offset = p.cursor - h + 3
	}

	// Draw entries
	for i := 0; i < h-2; i++ {
		entryIndex := p.offset + i
		if entryIndex >= len(p.entries) {
			break
		}

		entry := p.entries[entryIndex]
		entryName := entry.Name()
		fullPath := filepath.Join(p.currentPath, entryName)

		line := ""
		// Styling
		style := "\x1b[0m" // Reset

		if entry.IsDir() {
			// Determine if the directory is "fully selected"
			dirFullPath := filepath.Join(p.currentPath, entry.Name())
			var songsInDir []string
			filepath.WalkDir(dirFullPath, func(path string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".flac") {
					songsInDir = append(songsInDir, path)
				}
				return nil
			})

			isDirFullySelected := false
			if len(songsInDir) > 0 { // Only consider fully selected if there are songs in it
				allSongsSelected := true
				for _, songPath := range songsInDir {
					if !p.selected[songPath] {
						allSongsSelected = false
						break
					}
				}
				if allSongsSelected {
					isDirFullySelected = true
				}
			}

			if isDirFullySelected {
				line = "✓ " + entryName + "/" // Mark directory as selected
				style += "\x1b[32m" // Green text for selected directory
			} else {
				line = "▸ " + entryName + "/" // Default directory indicator
			}
		} else {
			// Existing file logic
			if p.selected[fullPath] {
				line = "✓ " + entryName
				style += "\x1b[32m" // Green text for selected file
			} else {
				line = "  " + entryName
			}
		}

		// Apply reverse video style for cursor, always on top of selection style
		if entryIndex == p.cursor {
			style += "\x1b[7m" // Reverse video for cursor
		}

		if len(line) > w {
			line = line[:w]
		}

		fmt.Printf("\x1b[%d;1H\x1b[K%s%s\x1b[0m", i+2, style, line)
	}
}

// Tick for Library does nothing.
func (p *Library) Tick() {}
