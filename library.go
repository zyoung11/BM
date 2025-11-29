package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/gopxl/beep/v2/speaker"
	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

// LibraryEntry holds enriched information about a file or directory in the library.
type LibraryEntry struct {
	entry os.DirEntry
	info  os.FileInfo
	isDir bool // True if it's a directory or a symlink to a directory.
}

// Library browses the music directory and adds songs to the playlist.
type Library struct {
	app *App

	entries     []LibraryEntry // All entries in the current directory
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
func (p *Library) scanDirectory(path string) {
	// Save current cursor position for current path before changing
	if p.currentPath != "" {
		p.pathHistory[p.currentPath] = p.cursor
	}

	p.entries = make([]LibraryEntry, 0)
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
		info, err := file.Info()
		if err != nil {
			continue // Skip files we can't get info for
		}

		isDir := info.IsDir()
		isLink := info.Mode()&os.ModeSymlink != 0
		isValidFlac := strings.HasSuffix(strings.ToLower(info.Name()), ".flac")

		// If it's a symlink, we need to check what it points to
		if isLink {
			targetPath := filepath.Join(path, file.Name())
			targetInfo, err := os.Stat(targetPath)
			if err != nil {
				continue // Ignore broken links
			}
			isDir = targetInfo.IsDir() // Update isDir based on the link's target
			isValidFlac = strings.HasSuffix(strings.ToLower(targetInfo.Name()), ".flac")
		}

		// Add to entries if it's a directory or a valid flac file
		if isDir || isValidFlac {
			p.entries = append(p.entries, LibraryEntry{
				entry: file,
				info:  info,
				isDir: isDir,
			})
		}
	}

	// Sort so directories come first, then files alphabetically
	sort.SliceStable(p.entries, func(i, j int) bool {
		if p.entries[i].isDir != p.entries[j].isDir {
			return p.entries[i].isDir // Directories first
		}
		return strings.ToLower(p.entries[i].entry.Name()) < strings.ToLower(p.entries[j].entry.Name())
	})
	p.offset = 0
}


// ensureGlobalCache builds a cache of all .flac files and directories if it doesn't exist.
func (p *Library) ensureGlobalCache() {
	if p.globalFileCache != nil {
		return
	}

	allFlacFiles := make(map[string]bool)
	dirWithFlac := make(map[string]bool)
	visited := make(map[string]bool)

	var walk func(string)
	walk = func(dirPath string) {
		realPath, err := filepath.EvalSymlinks(dirPath)
		if err != nil {
			realPath = dirPath // Use path for broken links
		}
		if visited[realPath] {
			return // Avoid cycles
		}
		visited[realPath] = true

		files, err := os.ReadDir(dirPath)
		if err != nil {
			return
		}

		for _, file := range files {
			entryPath := filepath.Join(dirPath, file.Name())
			info, err := file.Info()
			if err != nil {
				continue
			}

			isDir := info.IsDir()
			if info.Mode()&os.ModeSymlink != 0 {
				statInfo, statErr := os.Stat(entryPath)
				if statErr == nil {
					isDir = statInfo.IsDir()
				} else {
					continue // Broken link
				}
			}

			if isDir {
				walk(entryPath)
			} else if strings.HasSuffix(strings.ToLower(file.Name()), ".flac") {
				allFlacFiles[entryPath] = true
				// Mark all parent directories as containing flac
				tempPath := entryPath
				for {
					tempPath = filepath.Dir(tempPath)
					absTemp, errT := filepath.Abs(tempPath)
					absInitial, errI := filepath.Abs(p.initialPath)
					if errT != nil || errI != nil || absTemp < absInitial {
						break
					}
					dirWithFlac[tempPath] = true
					if absTemp == absInitial {
						break
					}
				}
			}
		}
	}

	walk(p.initialPath)

	// Combine into a single cache
	cache := make([]string, 0, len(allFlacFiles)+len(dirWithFlac))
	for path := range allFlacFiles {
		cache = append(cache, path)
	}
	for path := range dirWithFlac {
		cache = append(cache, path)
	}
	p.globalFileCache = cache
}

// filterSongs updates filteredSongPaths based on the searchQuery.
func (p *Library) filterSongs() {
	if p.searchQuery == "" {
		p.filteredSongPaths = nil
		p.scanDirectory(p.currentPath) // Refresh directory view when search is cleared
		return
	}

	p.ensureGlobalCache()
	type scoredItem struct {
		path     string
		score    int
		isDir    bool
		itemName string // For sorting by name within same score
	}
	var scoredItems []scoredItem

	for _, path := range p.globalFileCache {
		// We match against the full path to allow searching for artist/album folders
		score := fuzzyMatch(p.searchQuery, path)
		if score > 0 {
			// Check if it's a directory
			info, err := os.Stat(path)
			isDir := err == nil && info.IsDir()

			scoredItems = append(scoredItems, scoredItem{
				path:     path,
				score:    score,
				isDir:    isDir,
				itemName: filepath.Base(path),
			})
		}
	}

	// Sort: directories first, then by score (descending), then by name
	sort.Slice(scoredItems, func(i, j int) bool {
		// Directories come first
		if scoredItems[i].isDir != scoredItems[j].isDir {
			return scoredItems[i].isDir
		}
		// Then by score (higher scores first)
		if scoredItems[i].score != scoredItems[j].score {
			return scoredItems[i].score > scoredItems[j].score
		}
		// Finally by name (alphabetical)
		return strings.ToLower(scoredItems[i].itemName) < strings.ToLower(scoredItems[j].itemName)
	})

	p.filteredSongPaths = make([]string, 0, len(scoredItems))
	for _, scored := range scoredItems {
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
	if IsKey(key, GlobalConfig.Keymap.Library.SearchMode.ConfirmSearch) {
		p.isSearching = false // Exit input mode, keeping the search results
	} else if IsKey(key, GlobalConfig.Keymap.Library.SearchMode.EscapeSearch) {
		p.isSearching = false // Exit input mode
		p.searchQuery = ""    // Also clear the search query
		p.filterSongs()
	} else if IsKey(key, GlobalConfig.Keymap.Library.SearchMode.SearchBackspace) {
		if len(p.searchQuery) > 0 {
			runes := []rune(p.searchQuery)
			p.searchQuery = string(runes[:len(runes)-1])
			p.filterSongs()
		}
	} else if key == KeyArrowUp || key == KeyArrowDown || key == KeyArrowLeft || key == KeyArrowRight {
		// Arrow keys confirm search and exit input mode
		p.isSearching = false
	} else {
		if key >= 32 { // Allow any printable character
			p.searchQuery += string(key)
			p.filterSongs()
		}
	}
}

// handleDirViewInput handles keystrokes for the directory browsing view.
func (p *Library) handleDirViewInput(key rune) (Page, bool, error) {
	if IsKey(key, GlobalConfig.Keymap.Library.Search) {
		p.isSearching = true
	} else if IsKey(key, GlobalConfig.Keymap.Library.NavUp) {
		if len(p.entries) > 0 {
			p.cursor = (p.cursor - 1 + len(p.entries)) % len(p.entries)
		}
	} else if IsKey(key, GlobalConfig.Keymap.Library.NavDown) {
		if len(p.entries) > 0 {
			p.cursor = (p.cursor + 1) % len(p.entries)
		}
	} else if IsKey(key, GlobalConfig.Keymap.Library.NavEnterDir) {
		if p.cursor < len(p.entries) && p.entries[p.cursor].isDir {
			p.lastEntered = p.entries[p.cursor].entry.Name()
			newPath := filepath.Join(p.currentPath, p.entries[p.cursor].entry.Name())
			p.scanDirectory(newPath)
		}
	} else if IsKey(key, GlobalConfig.Keymap.Library.NavExitDir) {
		currentAbs, _ := filepath.Abs(p.currentPath)
		initialAbs, _ := filepath.Abs(p.initialPath)
		if currentAbs != initialAbs {
			newPath := filepath.Dir(p.currentPath)
			p.scanDirectory(newPath)
			if p.lastEntered != "" {
				for i, libEntry := range p.entries {
					if libEntry.entry.Name() == p.lastEntered && libEntry.isDir {
						p.cursor = i
						break
					}
				}
				p.lastEntered = ""
			}
		}
	} else if IsKey(key, GlobalConfig.Keymap.Library.ToggleSelect) {
		if p.cursor < len(p.entries) {
			p.toggleSelectionForEntry(p.entries[p.cursor])
			if p.cursor < len(p.entries)-1 {
				p.cursor++
			}
		}
	} else if IsKey(key, GlobalConfig.Keymap.Library.ToggleSelectAll) {
		p.toggleSelectAll(false) // Toggle all in current directory view
	}
	return nil, false, nil
}

// handleSearchViewInput handles keystrokes for the search results view.
func (p *Library) handleSearchViewInput(key rune) (Page, bool, error) {
	if IsKey(key, GlobalConfig.Keymap.Library.SearchMode.EscapeSearch) {
		p.searchQuery = "" // Clear search
		p.filterSongs()
	} else if IsKey(key, GlobalConfig.Keymap.Library.Search) {
		p.isSearching = true // Re-enter input mode
	} else if IsKey(key, GlobalConfig.Keymap.Library.NavUp) {
		if len(p.filteredSongPaths) > 0 {
			p.cursor = (p.cursor - 1 + len(p.filteredSongPaths)) % len(p.filteredSongPaths)
		}
	} else if IsKey(key, GlobalConfig.Keymap.Library.NavDown) {
		if len(p.filteredSongPaths) > 0 {
			p.cursor = (p.cursor + 1) % len(p.filteredSongPaths)
		}
	} else if IsKey(key, GlobalConfig.Keymap.Library.ToggleSelect) {
		if p.cursor < len(p.filteredSongPaths) {
			path := p.filteredSongPaths[p.cursor]
			info, err := os.Stat(path)
			if err == nil && info.IsDir() {
				// Directory selection logic...
				var songsInDir []string
				var collectSongs func(string)
				collectSongs = func(dirPath string) {
					files, err := os.ReadDir(dirPath)
					if err != nil {
						return
					}
					for _, file := range files {
						entryPath := filepath.Join(dirPath, file.Name())
						info, err := file.Info()
						if err != nil {
							continue
						}
						isDir := info.IsDir()
						if info.Mode()&os.ModeSymlink != 0 {
							statInfo, statErr := os.Stat(entryPath)
							if statErr == nil {
								isDir = statInfo.IsDir()
							} else {
								continue // Broken link
							}
						}
						if isDir {
							collectSongs(entryPath)
						} else if strings.HasSuffix(strings.ToLower(file.Name()), ".flac") {
							songsInDir = append(songsInDir, entryPath)
						}
					}
				}
				collectSongs(path)
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
						if p.selected[songPath] {
							p.toggleSelection(songPath)
						}
					} else {
						if !p.selected[songPath] {
							p.toggleSelection(songPath)
						}
					}
				}
			} else {
				p.toggleSelection(path)
			}
			if p.cursor < len(p.filteredSongPaths)-1 {
				p.cursor++
			}
		}
	} else if IsKey(key, GlobalConfig.Keymap.Library.ToggleSelectAll) {
		p.toggleSelectAll(true) // Toggle all in search results
	}
	return nil, false, nil
}

// HandleKey routes user input based on the current mode.
func (p *Library) HandleKey(key rune) (Page, bool, error) {
	var err error
	var page Page

	if p.isSearching {
		p.handleSearchInput(key)
	} else if p.searchQuery != "" {
		page, _, err = p.handleSearchViewInput(key)
	} else {
		page, _, err = p.handleDirViewInput(key)
	}

	p.View() // Redraw on any key press
	return page, true, err
}

// toggleSelectionForEntry handles selection logic for a LibraryEntry (file or dir).
func (p *Library) toggleSelectionForEntry(libEntry LibraryEntry) {
	fullPath := filepath.Join(p.currentPath, libEntry.entry.Name())
	if !libEntry.isDir {
		p.toggleSelection(fullPath)
	} else {
		var songsInDir []string
		var collectSongs func(string)
		collectSongs = func(dirPath string) {
			files, err := os.ReadDir(dirPath)
			if err != nil {
				return
			}
			for _, file := range files {
				entryPath := filepath.Join(dirPath, file.Name())
				info, err := file.Info()
				if err != nil {
					continue
				}
				isDir := info.IsDir()
				if info.Mode()&os.ModeSymlink != 0 {
					statInfo, statErr := os.Stat(entryPath)
					if statErr == nil {
						isDir = statInfo.IsDir()
					} else {
						continue // Broken link
					}
				}
				if isDir {
					collectSongs(entryPath)
				} else if strings.HasSuffix(strings.ToLower(file.Name()), ".flac") {
					songsInDir = append(songsInDir, entryPath)
				}
			}
		}
		collectSongs(fullPath)

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
		for _, libEntry := range p.entries {
			fullPath := filepath.Join(p.currentPath, libEntry.entry.Name())
			if !libEntry.isDir {
				allSongs = append(allSongs, fullPath)
			} else {
				var collectSongs func(string)
				collectSongs = func(dirPath string) {
					files, err := os.ReadDir(dirPath)
					if err != nil {
						return
					}
					for _, file := range files {
						entryPath := filepath.Join(dirPath, file.Name())
						info, err := file.Info()
						if err != nil {
							continue
						}
						isDir := info.IsDir()
						if info.Mode()&os.ModeSymlink != 0 {
							statInfo, statErr := os.Stat(entryPath)
							if statErr == nil {
								isDir = statInfo.IsDir()
							} else {
								continue // Broken link
							}
						}
						if isDir {
							collectSongs(entryPath)
						} else if strings.HasSuffix(strings.ToLower(file.Name()), ".flac") {
							allSongs = append(allSongs, entryPath)
						}
					}
				}
				collectSongs(fullPath)
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
	// Save the updated playlist
	if err := SavePlaylist(p.app.Playlist, p.initialPath); err != nil {
		log.Printf("Warning: failed to save playlist: %v", err)
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

			// If the playlist is now empty, stop everything.
			if len(p.app.Playlist) == 0 {
				if p.app.player != nil {
					speaker.Lock()
					if p.app.player.ctrl != nil {
						p.app.player.ctrl.Paused = true
					}
					speaker.Unlock()
				}
				p.app.player = nil
				p.app.currentSongPath = ""
				if p.app.mprisServer != nil {
					p.app.mprisServer.StopService()
					p.app.mprisServer = nil
				}
				// Update player page to show empty state
				if playerPage, ok := p.app.pages[0].(*PlayerPage); ok {
					playerPage.UpdateSong("")
				}
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
			fmt.Printf("\x1b[%d;%dH█", h, cursorX)
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

		// Check if it's a directory
		info, err := os.Stat(path)
		isDir := err == nil && info.IsDir()

		// For directories, check if any songs inside are selected
		isSelected := p.selected[path]
		if isDir && !isSelected {
			// Check if directory contains any selected songs
			var checkSelected func(string) bool
			checkSelected = func(dirPath string) bool {
				files, err := os.ReadDir(dirPath)
				if err != nil {
					return false
				}
				for _, file := range files {
					entryPath := filepath.Join(dirPath, file.Name())
					info, err := file.Info()
					if err != nil {
						continue
					}

					isDir := info.IsDir()
					if info.Mode()&os.ModeSymlink != 0 {
						statInfo, statErr := os.Stat(entryPath)
						if statErr == nil {
							isDir = statInfo.IsDir()
						} else {
							continue // Broken link
						}
					}

					if isDir {
						if checkSelected(entryPath) {
							return true
						}
					} else if p.selected[entryPath] {
						return true
					}
				}
				return false
			}
			isSelected = checkSelected(path)
		}

		if isSelected {
			line = "✓ " + path
			style += "\x1b[32m"
		} else {
			line = "  " + path
		}

		// Add directory indicator for directories
		if isDir {
			line += "/"
		}

		if entryIndex == p.cursor {
			style += "\x1b[7m"
		}
		// Use runewidth for accurate string width calculation and truncation
		if runewidth.StringWidth(line) > w-1 {
			// Truncate the line to fit the terminal width
			for runewidth.StringWidth(line) > w-1 && len(line) > 0 {
				line = line[:len(line)-1]
			}
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

		libEntry := p.entries[entryIndex]
		fullPath := filepath.Join(p.currentPath, libEntry.entry.Name())
		line, style := p.getDirEntryLine(libEntry, fullPath, entryIndex == p.cursor)
		// Use runewidth for accurate string width calculation and truncation
		if runewidth.StringWidth(line) > w-1 {
			// Truncate the line to fit the terminal width
			for runewidth.StringWidth(line) > w-1 && len(line) > 0 {
				line = line[:len(line)-1]
			}
		}
		fmt.Printf("\x1b[%d;1H\x1b[K%s%s\x1b[0m", i+3, style, line)
	}
}

// getDirEntryLine generates the display line and style for a directory entry.
func (p *Library) getDirEntryLine(libEntry LibraryEntry, fullPath string, isCursor bool) (string, string) {
	line := ""
	style := "\x1b[0m" // Reset
	isLink := libEntry.info.Mode()&os.ModeSymlink != 0
	name := libEntry.entry.Name()

	if isLink {
		name += "@"
	}

	if libEntry.isDir {
		// Directory-specific styling
		isDirPartiallySelected := false
		var checkSelected func(string) bool
		checkSelected = func(dirPath string) bool {
			files, err := os.ReadDir(dirPath)
			if err != nil {
				return false
			}
			for _, file := range files {
				entryPath := filepath.Join(dirPath, file.Name())
				info, err := file.Info()
				if err != nil {
					continue
				}

				isDir := info.IsDir()
				if info.Mode()&os.ModeSymlink != 0 {
					statInfo, statErr := os.Stat(entryPath)
					if statErr == nil {
						isDir = statInfo.IsDir()
					} else {
						continue // Broken link
					}
				}

				if isDir {
					if checkSelected(entryPath) {
						return true
					}
				} else if p.selected[entryPath] {
					return true
				}
			}
			return false
		}
		isDirPartiallySelected = checkSelected(fullPath)
		if isDirPartiallySelected {
			line = "✓ " + name + "/"
			style += "\x1b[32m"
		} else {
			line = "▸ " + name + "/"
		}
	} else {
		// File-specific styling
		if p.selected[fullPath] {
			line = "✓ " + name
			style += "\x1b[32m"
		} else {
			line = "  " + name
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
