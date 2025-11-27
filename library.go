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

	entries     []os.DirEntry // All entries in the current directory
	currentPath string
	initialPath string // The starting path provided to the application
	cursor      int
	selected    map[string]bool // Use file path as key for persistent selection
	offset      int             // For scrolling the view
	pathHistory map[string]int  // Store cursor position for each path
	lastEntered string          // Store the name of the last entered directory

	isSearching       bool
	searchQuery       string
	globalFileCache   []string // Cache of all .flac file paths
	filteredSongPaths []string // Results of the current search
}

// NewLibrary creates a new instance of Library.
func NewLibrary(app *App) *Library {
	return &Library{
		app:         app,
		currentPath: ".",
		initialPath: ".",
		selected:    make(map[string]bool),
		pathHistory: make(map[string]int),
	}
}

// NewLibraryWithPath creates a new instance of Library with a specific starting path.
func NewLibraryWithPath(app *App, startPath string) *Library {
	return &Library{
		app:         app,
		currentPath: startPath,
		initialPath: startPath,
		selected:    make(map[string]bool),
		pathHistory: make(map[string]int),
	}
}

// scanDirectory reads the contents of a directory and populates the entries list.
// This is used for the standard directory browsing view.
func (p *Library) scanDirectory(path string) {
	// Save current cursor position for current path before changing
	if p.currentPath != "" {
		p.pathHistory[p.currentPath] = p.cursor
	}

	p.entries = make([]os.DirEntry, 0)
	p.currentPath = path

	// Restore cursor position if available, otherwise set to 0
	if savedCursor, exists := p.pathHistory[path]; exists {
		p.cursor = savedCursor
	} else {
		p.cursor = 0
	}

	files, err := os.ReadDir(path)
	if err != nil {
		return
	}

	for _, file := range files {
		if file.IsDir() || strings.HasSuffix(strings.ToLower(file.Name()), ".flac") {
			p.entries = append(p.entries, file)
		}
	}
	// Sort so directories come first, then files alphabetically
	sort.SliceStable(p.entries, func(i, j int) bool {
		if p.entries[i].IsDir() != p.entries[j].IsDir() {
			return p.entries[i].IsDir()
		}
		return strings.ToLower(p.entries[i].Name()) < strings.ToLower(p.entries[j].Name())
	})
	p.offset = 0
}

// ensureGlobalCache builds a cache of all .flac files if it doesn't exist.
func (p *Library) ensureGlobalCache() {
	if p.globalFileCache != nil {
		return
	}
	p.globalFileCache = make([]string, 0)
	filepath.WalkDir(p.initialPath, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".flac") {
			p.globalFileCache = append(p.globalFileCache, path)
		}
		return nil
	})
}

// filterSongs updates filteredSongPaths based on the searchQuery.
func (p *Library) filterSongs() {
	if p.searchQuery == "" {
		p.filteredSongPaths = nil
		p.scanDirectory(p.currentPath) // Refresh directory view when search is cleared
		return
	}

	p.ensureGlobalCache()
	type scoredSong struct {
		path  string
		score int
	}
	var scoredSongs []scoredSong

	for _, path := range p.globalFileCache {
		// We match against the full path to allow searching for artist/album folders
		score := fuzzyMatch(p.searchQuery, path)
		if score > 0 {
			scoredSongs = append(scoredSongs, scoredSong{
				path:  path,
				score: score,
			})
		}
	}

	// Sort by score (descending)
	sort.Slice(scoredSongs, func(i, j int) bool {
		return scoredSongs[i].score > scoredSongs[j].score
	})

	p.filteredSongPaths = make([]string, 0, len(scoredSongs))
	for _, scored := range scoredSongs {
		p.filteredSongPaths = append(p.filteredSongPaths, scored.path)
	}
	p.cursor = 0
	p.offset = 0
}

// Init initializes the library by scanning the starting directory.
func (p *Library) Init() {
	p.scanDirectory(p.currentPath)
}

// handleSearchInput handles keystrokes when in search input mode.
func (p *Library) handleSearchInput(key rune) {
	switch key {
	case '\x1b', KeyEnter: // ESC or Enter
		p.isSearching = false // Exit input mode
	case KeyBackspace:
		if len(p.searchQuery) > 0 {
			runes := []rune(p.searchQuery)
			p.searchQuery = string(runes[:len(runes)-1])
			p.filterSongs()
		}
	default:
		if key >= 32 { // Allow any printable character
			p.searchQuery += string(key)
			p.filterSongs()
		}
	}
}

// handleDirViewInput handles keystrokes for the directory browsing view.
func (p *Library) handleDirViewInput(key rune) (Page, error) {
	switch key {
	case '\x1b': // ESC
		return nil, fmt.Errorf("user quit")
	case 'f':
		p.isSearching = true
	case 'k', 'w', KeyArrowUp:
		if len(p.entries) > 0 {
			p.cursor = (p.cursor - 1 + len(p.entries)) % len(p.entries)
		}
	case 'j', 's', KeyArrowDown:
		if len(p.entries) > 0 {
			p.cursor = (p.cursor + 1) % len(p.entries)
		}
	case 'l', 'd', KeyArrowRight:
		if p.cursor < len(p.entries) && p.entries[p.cursor].IsDir() {
			p.lastEntered = p.entries[p.cursor].Name()
			newPath := filepath.Join(p.currentPath, p.entries[p.cursor].Name())
			p.scanDirectory(newPath)
		}
	case 'h', 'a', KeyArrowLeft:
		currentAbs, _ := filepath.Abs(p.currentPath)
		initialAbs, _ := filepath.Abs(p.initialPath)
		if currentAbs != initialAbs {
			newPath := filepath.Dir(p.currentPath)
			p.scanDirectory(newPath)
			if p.lastEntered != "" {
				for i, entry := range p.entries {
					if entry.Name() == p.lastEntered && entry.IsDir() {
						p.cursor = i
						break
					}
				}
				p.lastEntered = ""
			}
		}
	case ' ':
		if p.cursor >= len(p.entries) {
			break
		}
		p.toggleSelectionForEntry(p.entries[p.cursor])
		if p.cursor < len(p.entries)-1 {
			p.cursor++
		}
	case 'e':
		p.toggleSelectAll(false) // Toggle all in current directory view
	}
	return nil, nil
}

// handleSearchViewInput handles keystrokes for the search results view.
func (p *Library) handleSearchViewInput(key rune) (Page, error) {
	switch key {
	case '\x1b': // ESC
		p.searchQuery = "" // Clear search
		p.filterSongs()
	case 'f', KeyEnter:
		p.isSearching = true // Re-enter input mode
	case 'k', 'w', KeyArrowUp:
		if len(p.filteredSongPaths) > 0 {
			p.cursor = (p.cursor - 1 + len(p.filteredSongPaths)) % len(p.filteredSongPaths)
		}
	case 'j', 's', KeyArrowDown:
		if len(p.filteredSongPaths) > 0 {
			p.cursor = (p.cursor + 1) % len(p.filteredSongPaths)
		}
	case ' ':
		if p.cursor < len(p.filteredSongPaths) {
			p.toggleSelection(p.filteredSongPaths[p.cursor])
			if p.cursor < len(p.filteredSongPaths)-1 {
				p.cursor++
			}
		}
	case 'e':
		p.toggleSelectAll(true) // Toggle all in search results
	}
	return nil, nil
}

// HandleKey routes user input based on the current mode.
func (p *Library) HandleKey(key rune) (Page, error) {
	var err error
	var page Page

	if p.isSearching {
		p.handleSearchInput(key)
	} else if p.searchQuery != "" {
		page, err = p.handleSearchViewInput(key)
	} else {
		page, err = p.handleDirViewInput(key)
	}

	p.View() // Redraw on any key press
	return page, err
}

// toggleSelectionForEntry handles selection logic for a DirEntry (file or dir).
func (p *Library) toggleSelectionForEntry(entry os.DirEntry) {
	fullPath := filepath.Join(p.currentPath, entry.Name())
	if !entry.IsDir() {
		p.toggleSelection(fullPath)
	} else {
		var songsInDir []string
		filepath.WalkDir(fullPath, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".flac") {
				songsInDir = append(songsInDir, path)
			}
			return nil
		})

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
				p.toggleSelection(songPath) // Deselect
			} else {
				if !p.selected[songPath] {
					p.toggleSelection(songPath) // Select
				}
			}
		}
	}
}

// toggleSelectAll toggles selection for all items in the current view (dir or search).
func (p *Library) toggleSelectAll(isSearchView bool) {
	var allSongs []string
	if isSearchView {
		allSongs = p.filteredSongPaths
	} else {
		for _, entry := range p.entries {
			fullPath := filepath.Join(p.currentPath, entry.Name())
			if !entry.IsDir() {
				allSongs = append(allSongs, fullPath)
			} else {
				filepath.WalkDir(fullPath, func(path string, d os.DirEntry, err error) error {
					if err == nil && !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".flac") {
						allSongs = append(allSongs, path)
					}
					return nil
				})
			}
		}
	}

	if len(allSongs) == 0 {
		return
	}

	allCurrentlySelected := true
	for _, songPath := range allSongs {
		if !p.selected[songPath] {
			allCurrentlySelected = false
			break
		}
	}

	for _, songPath := range allSongs {
		if allCurrentlySelected {
			if p.selected[songPath] {
				p.toggleSelection(songPath)
			} // Deselect
		} else {
			if !p.selected[songPath] {
				p.toggleSelection(songPath)
			} // Select
		}
	}
}

// toggleSelection adds or removes a file path from the selection and playlist.
func (p *Library) toggleSelection(path string) {
	if p.selected[path] {
		delete(p.selected, path)
		p.removeSongFromPlaylist(path)
	} else {
		p.selected[path] = true
		// Add to playlist, avoiding duplicates
		found := false
		for _, s := range p.app.Playlist {
			if s == path {
				found = true
				break
			}
		}
		if !found {
			p.app.Playlist = append(p.app.Playlist, path)
			if len(p.app.Playlist) == 1 {
				p.app.PlaySongWithSwitch(path, false)
			}
		}
	}
}

// removeSongFromPlaylist removes a song path from the app's playlist.
func (p *Library) removeSongFromPlaylist(songPath string) {
	for i, s := range p.app.Playlist {
		if s == songPath {
			p.app.Playlist = append(p.app.Playlist[:i], p.app.Playlist[i+1:]...)
			if p.app.mprisServer != nil {
				p.app.mprisServer.UpdateProperties()
			}
			return
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

// View renders the library page based on the current mode.
func (p *Library) View() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H") // Clear screen

	title := "Library"
	titleX := (w - len(title)) / 2
	fmt.Printf("\x1b[1;%dH\x1b[1m%s\x1b[0m", titleX, title)

	listHeight := h - 4

	var currentListLength int // For scrollbar and list rendering
	var currentCursor int     // For scrollbar
	var currentOffset int     // For scrollbar

	// Determine list length, cursor, and offset based on current view mode
	if p.searchQuery != "" {
		currentListLength = len(p.filteredSongPaths)
	} else {
		currentListLength = len(p.entries)
	}
	currentCursor = p.cursor
	currentOffset = p.offset

	// Adjust offset for scrolling
	if currentCursor < currentOffset {
		currentOffset = currentCursor
	}
	if currentCursor >= currentOffset+listHeight {
		currentOffset = currentCursor - listHeight + 1
	}

	// Render Footer (including search prompt and cursor if searching)
	if p.isSearching || p.searchQuery != "" {
		p.drawSearchFooter(w, h, fmt.Sprintf("Search: %s", p.searchQuery))
	} else {
		p.drawPathFooter(w, h, fmt.Sprintf("Path: %s", p.currentPath))
	}

	// Render the list content
	if p.searchQuery != "" { // Render filtered results
		p.renderFilteredListContent(w, h, listHeight, currentOffset)
	} else { // Render directory content
		p.renderDirectoryListContent(w, h, listHeight, currentOffset)
	}

	// Draw Scrollbar
	p.drawScrollbar(h, listHeight, currentListLength, currentOffset)
}

// Helper for drawing search footer with cursor positioning
func (p *Library) drawSearchFooter(w, h int, footerText string) {
	// Truncate footer if it's too long
	if len(footerText) > w {
		footerText = "..." + footerText[len(footerText)-w+3:]
	}
	footerX := (w - len(footerText)) / 2
	if footerX < 1 {
		footerX = 1
	}
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", h, footerX, footerText)
	if p.isSearching {
		cursorX := footerX + len("Search: ") + len(p.searchQuery)
		if cursorX <= w {
			fmt.Printf("\x1b[%d;%dH", h, cursorX)
		}
	}
}

// Helper for drawing path footer
func (p *Library) drawPathFooter(w, h int, footerText string) {
	// Truncate footer if it's too long
	if len(footerText) > w {
		footerText = "..." + footerText[len(footerText)-w+3:]
	}
	footerX := (w - len(footerText)) / 2
	if footerX < 1 {
		footerX = 1
	}
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", h, footerX, footerText)
}

// Helper for rendering filtered list content
func (p *Library) renderFilteredListContent(w, h, listHeight, currentOffset int) {
	for i := 0; i < listHeight; i++ {
		entryIndex := currentOffset + i
		if entryIndex >= len(p.filteredSongPaths) {
			break
		}

		path := p.filteredSongPaths[entryIndex]
		line := ""
		style := "\x1b[0m"
		if p.selected[path] {
			line = "✓ " + path
			style += "\x1b[32m"
		} else {
			line = "  " + path
		}
		if entryIndex == p.cursor {
			style += "\x1b[7m"
		}
		if len(line) > w-1 {
			line = line[:w-1]
		}
		fmt.Printf("\x1b[%d;1H\x1b[K%s%s\x1b[0m", i+3, style, line)
	}
}

// Helper for rendering directory list content
func (p *Library) renderDirectoryListContent(w, h, listHeight, currentOffset int) {
	for i := 0; i < listHeight; i++ {
		entryIndex := currentOffset + i
		if entryIndex >= len(p.entries) {
			break
		}

		entry := p.entries[entryIndex]
		fullPath := filepath.Join(p.currentPath, entry.Name())
		line, style := p.getDirEntryLine(entry, fullPath, entryIndex == p.cursor)
		if len(line) > w-1 {
			line = line[:w-1]
		}
		fmt.Printf("\x1b[%d;1H\x1b[K%s%s\x1b[0m", i+3, style, line)
	}
}

// getDirEntryLine generates the display line and style for a directory entry.
func (p *Library) getDirEntryLine(entry os.DirEntry, fullPath string, isCursor bool) (string, string) {
	line := ""
	style := "\x1b[0m" // Reset

	if entry.IsDir() {
		// Directory-specific styling
		isDirPartiallySelected := false
		filepath.WalkDir(fullPath, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && p.selected[path] {
				isDirPartiallySelected = true
				return filepath.SkipDir // Optimization
			}
			return nil
		})
		if isDirPartiallySelected {
			line = "✓ " + entry.Name() + "/"
			style += "\x1b[32m"
		} else {
			line = "▸ " + entry.Name() + "/"
		}
	} else {
		// File-specific styling
		if p.selected[fullPath] {
			line = "✓ " + entry.Name()
			style += "\x1b[32m"
		} else {
			line = "  " + entry.Name()
		}
	}
	if isCursor {
		style += "\x1b[7m"
	}
	return line, style
}

// drawScrollbar draws a scrollbar on the right side of the screen.
func (p *Library) drawScrollbar(h, listHeight, totalItems, currentOffset int) {
	if totalItems <= listHeight {
		return
	}

	w, _, _ := term.GetSize(int(os.Stdout.Fd()))
	thumbSize := listHeight * listHeight / totalItems
	if thumbSize < 1 {
		thumbSize = 1
	}

	scrollRange := totalItems - listHeight
	thumbRange := listHeight - thumbSize

	thumbStart := 0
	if scrollRange > 0 {
		thumbStart = currentOffset * thumbRange / scrollRange
	}

	for i := 0; i < listHeight; i++ {
		if i >= thumbStart && i < thumbStart+thumbSize {
			fmt.Printf("\x1b[%d;%dH┃", i+3, w)
		} else {
			fmt.Printf("\x1b[%d;%dH│", i+3, w)
		}
	}
}

// Tick for Library does nothing.
func (p *Library) Tick() {}
