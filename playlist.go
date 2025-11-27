package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gopxl/beep/v2/speaker"
	"golang.org/x/term"
)

// PlayList displays the list of songs to be played.
type PlayList struct {
	app    *App
	cursor int // The UI cursor on the viewPlaylist
	offset int

	isSearching     bool
	searchQuery     string
	viewPlaylist    []string // The filtered playlist to be displayed
	originalIndices []int    // Map from viewPlaylist index to app.Playlist index
}

// NewPlayList creates a new instance of PlayList.
func NewPlayList(app *App) *PlayList {
	return &PlayList{
		app:             app,
		isSearching:     false,
		searchQuery:     "",
		viewPlaylist:    make([]string, 0),
		originalIndices: make([]int, 0),
	}
}

// Init for PlayList prepares the initial view.
func (p *PlayList) Init() {
	p.filterPlaylist()
}

// filterPlaylist updates the viewPlaylist based on the searchQuery.
func (p *PlayList) filterPlaylist() {
	p.viewPlaylist = make([]string, 0)
	p.originalIndices = make([]int, 0)

	if p.searchQuery == "" {
		p.viewPlaylist = p.app.Playlist
		for i := range p.app.Playlist {
			p.originalIndices = append(p.originalIndices, i)
		}
	} else {
		for i, songPath := range p.app.Playlist {
			songName := filepath.Base(songPath)
			if strings.Contains(strings.ToLower(songName), strings.ToLower(p.searchQuery)) {
				p.viewPlaylist = append(p.viewPlaylist, songPath)
				p.originalIndices = append(p.originalIndices, i)
			}
		}
	}

	// Reset cursor and offset when filter changes
	p.cursor = 0
	p.offset = 0
}

// HandleKey handles user input for the playlist.
func (p *PlayList) HandleKey(key rune) (Page, error) {
	if p.isSearching {
		switch key {
		case '\x1b': // ESC
			p.isSearching = false
			p.searchQuery = ""
			p.filterPlaylist()
		case KeyEnter:
			p.isSearching = false // Exit search, keep filter
		case KeyBackspace:
			if len(p.searchQuery) > 0 {
				runes := []rune(p.searchQuery)
				p.searchQuery = string(runes[:len(runes)-1])
				p.filterPlaylist()
			}
		default:
			if key >= 32 && key <= 126 { // Printable ASCII
				p.searchQuery += string(key)
				p.filterPlaylist()
			}
		}
		p.View()
		return nil, nil
	}

	// Not searching
	needRedraw := true
	switch key {
	case '\x1b': // ESC
		return nil, fmt.Errorf("user quit")
	case 'f':
		p.isSearching = true
		p.searchQuery = ""
		p.filterPlaylist()
	case KeyEnter: // Play current song
		if len(p.viewPlaylist) > 0 && p.cursor >= 0 && p.cursor < len(p.viewPlaylist) {
			songPath := p.viewPlaylist[p.cursor]
			if err := p.app.PlaySong(songPath); err != nil {
				// Handle error
			}
		}
		needRedraw = false // PlaySong handles redraw
	case 'k', 'w', KeyArrowUp:
		if len(p.viewPlaylist) > 0 {
			p.cursor = (p.cursor - 1 + len(p.viewPlaylist)) % len(p.viewPlaylist)
		}
	case 'j', 's', KeyArrowDown:
		if len(p.viewPlaylist) > 0 {
			p.cursor = (p.cursor + 1) % len(p.viewPlaylist)
		}
	case ' ': // Remove current song from playlist
		p.removeCurrentSong()
	}

	if needRedraw {
		p.View()
	}
	return nil, nil
}

// removeCurrentSong removes the song at the current cursor position.
func (p *PlayList) removeCurrentSong() {
	if p.cursor < 0 || p.cursor >= len(p.viewPlaylist) {
		return
	}

	// Get the original index before removal
	originalIndex := p.originalIndices[p.cursor]
	songPath := p.app.Playlist[originalIndex]
	wasPlayingSong := (p.app.currentSongPath == songPath)

	// Remove from app.Playlist
	p.app.Playlist = append(p.app.Playlist[:originalIndex], p.app.Playlist[originalIndex+1:]...)

	// Find Library page and update its selection state
	for _, page := range p.app.pages {
		if libPage, ok := page.(*Library); ok {
			delete(libPage.selected, songPath)
			break
		}
	}

	// After modification, we must refresh the filtered view
	p.filterPlaylist()

	// Adjust cursor if it's now out of bounds
	if p.cursor >= len(p.viewPlaylist) && len(p.viewPlaylist) > 0 {
		p.cursor = len(p.viewPlaylist) - 1
	} else if len(p.viewPlaylist) == 0 {
		p.cursor = 0
	}

	if wasPlayingSong {
		if len(p.app.Playlist) > 0 {
			nextIndex := originalIndex
			if nextIndex >= len(p.app.Playlist) {
				nextIndex = len(p.app.Playlist) - 1
			}
			p.app.PlaySongWithSwitch(p.app.Playlist[nextIndex], false)
		} else {
			p.stopPlaybackAndShowEmptyState()
		}
	}
}


// stopPlaybackAndShowEmptyState stops playback and shows an empty state.
func (p *PlayList) stopPlaybackAndShowEmptyState() {
	if p.app.player != nil {
		speaker.Lock()
		p.app.player.ctrl.Paused = true
		speaker.Unlock()
	}
	p.app.player = nil
	p.app.currentSongPath = ""
	if p.app.mprisServer != nil {
		p.app.mprisServer.StopService()
		p.app.mprisServer = nil
	}
	if playerPage, ok := p.app.pages[0].(*PlayerPage); ok {
		playerPage.UpdateSong("")
	}
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
	title := "PlayList"
	titleX := (w - len(title)) / 2
	fmt.Printf("\x1b[1;%dH\x1b[1m%s\x1b[0m", titleX, title)

	// Footer
	var footer string
	if p.isSearching {
		footer = fmt.Sprintf("Search: %s", p.searchQuery)
	}
	footerX := (w - len(footer)) / 2
	if footerX < 1 { footerX = 1 }
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", h, footerX, footer)
	if p.isSearching {
		cursorX := footerX + len("Search: ") + len(p.searchQuery)
		if cursorX <= w {
			fmt.Printf("\x1b[%d;%dH", h, cursorX)
		}
	}

	if len(p.viewPlaylist) == 0 {
		msg := "PlayList is empty"
		if p.searchQuery != "" {
			msg = "No songs match your search"
		}
		msg2 := "Add songs from the Library tab"
		msgX := (w - len(msg)) / 2
		msg2X := (w - len(msg2)) / 2
		centerRow := h / 2

		fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", centerRow-1, msgX, msg)
		if p.searchQuery == "" {
			fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", centerRow+1, msg2X, msg2)
		}
		return
	}

	listHeight := h - 4
	if listHeight < 0 { listHeight = 0 }

	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+listHeight {
		p.offset = p.cursor - listHeight + 1
	}

	for i := 0; i < listHeight; i++ {
		trackIndex := p.offset + i
		if trackIndex >= len(p.viewPlaylist) {
			break
		}

		trackPath := p.viewPlaylist[trackIndex]
		trackName := filepath.Base(trackPath)

		style := "\x1b[32m" // Default green
		if trackPath == p.app.currentSongPath {
			style = "\x1b[31m" // Red for playing
		}
		if trackIndex == p.cursor {
			style += "\x1b[7m" // Reverse for cursor
		}

		line := fmt.Sprintf("✓ %s", trackName)
		if len(line) > w-1 {
			line = line[:w-1]
		}
		fmt.Printf("\x1b[%d;1H\x1b[K%s%s\x1b[0m", i+3, style, line)
	}

	totalItems := len(p.viewPlaylist)
	if totalItems > listHeight {
		thumbSize := listHeight * listHeight / totalItems
		if thumbSize < 1 { thumbSize = 1 }
		scrollRange := totalItems - listHeight
		thumbRange := listHeight - thumbSize
		thumbStart := 0
		if scrollRange > 0 {
			thumbStart = p.offset * thumbRange / scrollRange
		}
		for i := 0; i < listHeight; i++ {
			if i >= thumbStart && i < thumbStart+thumbSize {
				fmt.Printf("\x1b[%d;%dH┃", i+3, w) // Thumb
			} else {
				fmt.Printf("\x1b[%d;%dH│", i+3, w) // Track
			}
		}
	}
}

// Tick for PlayList does nothing.
func (p *PlayList) Tick() {}
