package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/gopxl/beep/v2/speaker"
	"github.com/mattn/go-runewidth"
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
		type scoredSong struct {
			path  string
			score int
			index int
		}
		var scoredSongs []scoredSong

		for i, songPath := range p.app.Playlist {
			songName := filepath.Base(songPath)
			score := fuzzyMatch(p.searchQuery, songName)
			if score > 0 {
				scoredSongs = append(scoredSongs, scoredSong{
					path:  songPath,
					score: score,
					index: i,
				})
			}
		}

		// Sort by score (descending)
		sort.Slice(scoredSongs, func(i, j int) bool {
			return scoredSongs[i].score > scoredSongs[j].score
		})

		for _, scored := range scoredSongs {
			p.viewPlaylist = append(p.viewPlaylist, scored.path)
			p.originalIndices = append(p.originalIndices, scored.index)
		}
	}

	// Reset cursor and offset when filter changes
	p.cursor = 0
	p.offset = 0
}

// HandleKey handles user input for the playlist.
func (p *PlayList) HandleKey(key rune) (Page, error) {
	if p.isSearching {
		if IsKey(key, AppConfig.Keymap.Playlist.SearchMode.ConfirmSearch) {
			p.isSearching = false // Exit input mode, keeping the search results
		} else if IsKey(key, AppConfig.Keymap.Playlist.SearchMode.EscapeSearch) {
			p.isSearching = false
			p.searchQuery = ""
			p.filterPlaylist()
		} else if IsKey(key, AppConfig.Keymap.Playlist.SearchMode.SearchBackspace) {
			if len(p.searchQuery) > 0 {
				runes := []rune(p.searchQuery)
				p.searchQuery = string(runes[:len(runes)-1])
				p.filterPlaylist()
			}
		} else if key == KeyArrowUp || key == KeyArrowDown || key == KeyArrowLeft || key == KeyArrowRight {
			// Arrow keys confirm search and exit input mode
			p.isSearching = false
		} else {
			if key >= 32 { // Allow any printable character
				p.searchQuery += string(key)
				p.filterPlaylist()
			}
		}
		p.View()
		return nil, nil
	}

	// Not in search input mode
	needRedraw := true
	if IsKey(key, AppConfig.Keymap.Playlist.SearchMode.EscapeSearch) {
		if p.searchQuery != "" { // If there's an active search, clear it
			p.searchQuery = ""
			p.filterPlaylist()
		}
	} else if IsKey(key, AppConfig.Keymap.Playlist.Search) {
		p.isSearching = true
	} else if IsKey(key, AppConfig.Keymap.Playlist.PlaySong) {
		if len(p.viewPlaylist) > 0 && p.cursor >= 0 && p.cursor < len(p.viewPlaylist) {
			songPath := p.viewPlaylist[p.cursor]
			if err := p.app.PlaySong(songPath); err != nil {
				// Handle error
			}
		}
		needRedraw = false // PlaySong handles redraw
	} else if IsKey(key, AppConfig.Keymap.Playlist.NavUp) {
		if len(p.viewPlaylist) > 0 {
			p.cursor = (p.cursor - 1 + len(p.viewPlaylist)) % len(p.viewPlaylist)
		}
	} else if IsKey(key, AppConfig.Keymap.Playlist.NavDown) {
		if len(p.viewPlaylist) > 0 {
			p.cursor = (p.cursor + 1) % len(p.viewPlaylist)
		}
	} else if IsKey(key, AppConfig.Keymap.Playlist.RemoveSong) {
		oldCursor := p.cursor
		p.removeCurrentSong()
		if oldCursor < len(p.viewPlaylist) {
			p.cursor = oldCursor
		} else if len(p.viewPlaylist) > 0 {
			p.cursor = len(p.viewPlaylist) - 1
		}
	} else {
		needRedraw = false
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
			// Trigger a re-filter in Library page if it's currently showing search results
			if libPage.searchQuery != "" {
				libPage.filterSongs()
			}
			break
		}
	}

	// After modification, we must refresh the filtered view
	p.filterPlaylist()

	// Adjust cursor: move to next item if available, otherwise stay at current position
	// But since filterPlaylist() resets cursor to 0, we need to preserve the intended behavior
	// Instead, we'll handle cursor movement in the HandleKey method like Library page does

	if len(p.app.Playlist) == 0 {
		p.stopPlaybackAndShowEmptyState()
	} else if wasPlayingSong {
		nextIndex := originalIndex
		if nextIndex >= len(p.app.Playlist) {
			nextIndex = len(p.app.Playlist) - 1
		}
		p.app.PlaySongWithSwitch(p.app.Playlist[nextIndex], false)
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

	listHeight := h - 4

	// Render Footer
	var footer string
	if p.isSearching || p.searchQuery != "" {
		footer = fmt.Sprintf("Search: %s", p.searchQuery)
	} else {
		// No custom footer for Playlist, leave empty
		footer = ""
	}
	// Truncate footer if it's too long
	if len(footer) > w {
		footer = "..." + footer[len(footer)-w+3:]
	}
	footerX := (w - len(footer)) / 2
	if footerX < 1 {
		footerX = 1
	}
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", h, footerX, footer)
	// If searching, position cursor at the end of the query
	if p.isSearching {
		cursorX := footerX + len("Search: ") + len(p.searchQuery)
		if cursorX <= w {
			fmt.Printf("\x1b[%d;%dH█", h, cursorX)
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

	// Adjust offset for scrolling
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
		// Use runewidth for accurate string width calculation and truncation
		if runewidth.StringWidth(line) > w-1 {
			// Truncate the line to fit the terminal width
			for runewidth.StringWidth(line) > w-1 && len(line) > 0 {
				line = line[:len(line)-1]
			}
		}
		fmt.Printf("\x1b[%d;1H\x1b[K%s%s\x1b[0m", i+3, style, line)
	}

	totalItems := len(p.viewPlaylist)
	if totalItems > listHeight {
		thumbSize := listHeight * listHeight / totalItems
		if thumbSize < 1 {
			thumbSize = 1
		}
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
