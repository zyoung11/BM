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
//
// LibraryEntry 保存媒体库中文件或目录的丰富信息。
type LibraryEntry struct {
	entry os.DirEntry
	info  os.FileInfo
	isDir bool // True if it's a directory or a symlink to a directory. / 如果是目录或指向目录的符号链接，则为true。
}

// Library browses the music directory and adds songs to the playlist.
//
// Library 浏览音乐目录并将歌曲添加到播放列表。
type Library struct {
	app *App

	entries           []LibraryEntry // All entries in the current directory. / 当前目录中的所有条目。
	currentPath       string
	initialPath       string // The starting path provided to the application. / 提供给应用程序的起始路径。
	cursor            int
	selected          map[string]bool // Use file path as key for persistent selection. / 使用文件路径作为持久选择的键。
	offset            int             // For scrolling the view. / 用于滚动视图。
	pathHistory       map[string]int  // Store cursor position for each path. / 存储每个路径的光标位置。
	lastEntered       string          // Store the name of the last entered directory. / 存储最后进入的目录的名称。
	isSearching       bool
	searchQuery       string
	globalFileCache   []string        // Cache of all audio file paths. / 所有音频文件路径的缓存。
	filteredSongPaths []string        // Results of the current search. / 当前搜索的结果。
	resamplingSong    string          // Path of the song currently being resampled. / 当前正在重采样的歌曲路径。
	dirSelectionCache map[string]bool // Cache for directory partial selection state. / 目录部分选择状态的缓存。
}

// NewLibrary creates a new instance of Library.
//
// NewLibrary 创建一个新的 Library 实例。
func NewLibrary(app *App) *Library {
	return &Library{
		app:               app,
		currentPath:       ".",
		initialPath:       ".",
		selected:          make(map[string]bool),
		pathHistory:       make(map[string]int),
		dirSelectionCache: make(map[string]bool),
	}
}

// NewLibraryWithPath creates a new instance of Library with a specific starting path.
//
// NewLibraryWithPath 使用特定的起始路径创建一个新的 Library 实例。
func NewLibraryWithPath(app *App, startPath string) *Library {
	selectedSongs := make(map[string]bool)
	for _, songPath := range app.Playlist {
		selectedSongs[songPath] = true
	}

	return &Library{
		app:               app,
		currentPath:       startPath,
		initialPath:       startPath,
		selected:          selectedSongs,
		pathHistory:       make(map[string]int),
		dirSelectionCache: make(map[string]bool),
	}
}

// scanDirectory reads the contents of a directory, filters for audio files and directories,
// sorts them, and populates the entries list. It also handles symlinks.
//
// scanDirectory 读取目录内容，筛选音频文件和目录，对它们进行排序，并填充到条目列表中。它还能处理符号链接。
func (p *Library) scanDirectory(path string) {
	if p.currentPath != "" {
		p.pathHistory[p.currentPath] = p.cursor
	}

	p.entries = make([]LibraryEntry, 0)
	p.currentPath = path

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
			continue
		}

		isDir := info.IsDir()
		isLink := info.Mode()&os.ModeSymlink != 0
		isValidAudio := isAudioFile(info.Name())

		if isLink {
			targetPath := filepath.Join(path, file.Name())
			targetInfo, err := os.Stat(targetPath)
			if err != nil {
				continue
			}
			isDir = targetInfo.IsDir()
			isValidAudio = isAudioFile(targetInfo.Name())
		}

		if isDir || isValidAudio {
			p.entries = append(p.entries, LibraryEntry{
				entry: file,
				info:  info,
				isDir: isDir,
			})
		}
	}

	sort.SliceStable(p.entries, func(i, j int) bool {
		if p.entries[i].isDir != p.entries[j].isDir {
			return p.entries[i].isDir
		}
		return strings.ToLower(p.entries[i].entry.Name()) < strings.ToLower(p.entries[j].entry.Name())
	})
	p.offset = 0
}

// ensureGlobalCache builds a cache of all audio files and directories if it doesn't exist.
//
// ensureGlobalCache 如果缓存不存在，则构建一个包含所有音频文件和目录的缓存。
func (p *Library) ensureGlobalCache() {
	if p.globalFileCache != nil {
		return
	}

	allAudioFiles := make(map[string]bool)
	dirWithAudio := make(map[string]bool)
	visited := make(map[string]bool)

	var walk func(string)
	walk = func(dirPath string) {
		realPath, err := filepath.EvalSymlinks(dirPath)
		if err != nil {
			realPath = dirPath
		}
		if visited[realPath] {
			return
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
					continue
				}
			}

			if isDir {
				walk(entryPath)
			} else if isAudioFile(file.Name()) {
				allAudioFiles[entryPath] = true
				tempPath := entryPath
				for {
					tempPath = filepath.Dir(tempPath)
					absTemp, errT := filepath.Abs(tempPath)
					absInitial, errI := filepath.Abs(p.initialPath)
					if errT != nil || errI != nil || absTemp < absInitial {
						break
					}
					dirWithAudio[tempPath] = true
					if absTemp == absInitial {
						break
					}
				}
			}
		}
	}

	walk(p.initialPath)

	cache := make([]string, 0, len(allAudioFiles)+len(dirWithAudio))
	for path := range allAudioFiles {
		cache = append(cache, path)
	}
	for path := range dirWithAudio {
		cache = append(cache, path)
	}
	p.globalFileCache = cache
}

// filterSongs updates filteredSongPaths based on the searchQuery.
//
// filterSongs 根据 searchQuery 更新 filteredSongPaths。
func (p *Library) filterSongs() {
	if p.searchQuery == "" {
		p.filteredSongPaths = nil
		p.scanDirectory(p.currentPath)
		return
	}

	p.ensureGlobalCache()
	type scoredItem struct {
		path     string
		score    int
		isDir    bool
		itemName string
	}
	var scoredItems []scoredItem

	for _, path := range p.globalFileCache {
		score := fuzzyMatch(p.searchQuery, path)
		if score > 0 {
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

	sort.Slice(scoredItems, func(i, j int) bool {
		if scoredItems[i].isDir != scoredItems[j].isDir {
			return scoredItems[i].isDir
		}
		if scoredItems[i].score != scoredItems[j].score {
			return scoredItems[i].score > scoredItems[j].score
		}
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
//
// Init 通过扫描起始目录来初始化媒体库。
func (p *Library) Init() {
	p.scanDirectory(p.currentPath)
}

// handleSearchInput handles keystrokes when in search input mode.
//
// handleSearchInput 处理搜索输入模式下的按键。
func (p *Library) handleSearchInput(key rune) {
	if IsKey(key, GlobalConfig.Keymap.Library.SearchMode.ConfirmSearch) {
		p.isSearching = false
	} else if IsKey(key, GlobalConfig.Keymap.Library.SearchMode.EscapeSearch) {
		p.isSearching = false
		p.searchQuery = ""
		p.filterSongs()
	} else if IsKey(key, GlobalConfig.Keymap.Library.SearchMode.SearchBackspace) {
		if len(p.searchQuery) > 0 {
			runes := []rune(p.searchQuery)
			p.searchQuery = string(runes[:len(runes)-1])
			p.filterSongs()
		}
	} else if key == KeyArrowUp || key == KeyArrowDown || key == KeyArrowLeft || key == KeyArrowRight {
		p.isSearching = false
	} else {
		if key >= 32 {
			p.searchQuery += string(key)
			p.filterSongs()
		}
	}
}

// handleDirViewInput handles keystrokes for the directory browsing view.
//
// handleDirViewInput 处理目录浏览视图中的按键。
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
		p.toggleSelectAll(false)
	}
	return nil, false, nil
}

// handleSearchViewInput handles keystrokes for the search results view.
//
// handleSearchViewInput 处理搜索结果视图中的按键。
func (p *Library) handleSearchViewInput(key rune) (Page, bool, error) {
	if IsKey(key, GlobalConfig.Keymap.Library.SearchMode.EscapeSearch) {
		p.searchQuery = ""
		p.filterSongs()
	} else if IsKey(key, GlobalConfig.Keymap.Library.Search) {
		p.isSearching = true
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
								continue
							}
						}
						if isDir {
							collectSongs(entryPath)
						} else if isAudioFile(file.Name()) {
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
		p.toggleSelectAll(true)
	}
	return nil, false, nil
}

// HandleKey routes user input based on the current mode (directory view, search results, or search input).
//
// HandleKey 根据当前模式（目录视图、搜索结果或搜索输入）路由用户输入。
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

	p.View()
	return page, true, err
}

// toggleSelectionForEntry handles selection logic for a LibraryEntry (which can be a file or directory).
//
// toggleSelectionForEntry 处理 LibraryEntry（可以是文件或目录）的选择逻辑。
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
						continue
					}
				}
				if isDir {
					collectSongs(entryPath)
				} else if isAudioFile(file.Name()) {
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
				p.toggleSelection(songPath)
			} else {
				if !p.selected[songPath] {
					p.toggleSelection(songPath)
				}
			}
		}
	}
	// Clear cache on selection change
	p.dirSelectionCache = make(map[string]bool)
}

// toggleSelectAll toggles the selection for all items in the current view (directory or search results).
//
// toggleSelectAll 切换当前视图（目录或搜索结果）中所有项目的选择状态。
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
								continue
							}
						}
						if isDir {
							collectSongs(entryPath)
						} else if isAudioFile(file.Name()) {
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
			}
		} else {
			if !p.selected[songPath] {
				p.toggleSelection(songPath)
			}
		}
	}
	// Clear cache on selection change
	p.dirSelectionCache = make(map[string]bool)
}

// toggleSelection adds or removes a file path from the selection and playlist.
//
// toggleSelection 从选择和播放列表中添加或删除文件路径。
func (p *Library) toggleSelection(path string) {
	if p.selected[path] {
		delete(p.selected, path)
		p.removeSongFromPlaylist(path)
	} else {
		p.selected[path] = true
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
				// Check if resampling is needed before playing
				needsResample, err := p.NeedsResampling(path)
				if err == nil && needsResample {
					p.resamplingSong = path
					// Removed p.View() call to prevent double rendering
				}
				p.app.PlaySongWithSwitchAndRender(path, false, false)
				p.resamplingSong = "" // Clear resampling flag
			}
		}
	}
	// Clear cache on selection change
	p.dirSelectionCache = make(map[string]bool)
	if err := SavePlaylist(p.app.Playlist, p.initialPath); err != nil {
		log.Printf("Warning: failed to save playlist: %v\n\n警告: 保存播放列表失败: %v", err, err)
	}
}

// removeSongFromPlaylist removes a song path from the app's playlist.
//
// removeSongFromPlaylist 从应用的播放列表中删除一个歌曲路径。
func (p *Library) removeSongFromPlaylist(songPath string) {
	for i, s := range p.app.Playlist {
		if s == songPath {
			p.app.Playlist = append(p.app.Playlist[:i], p.app.Playlist[i+1:]...)
			if p.app.mprisServer != nil {
				p.app.mprisServer.UpdateProperties()
			}

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
				if playerPage, ok := p.app.pages[0].(*PlayerPage); ok {
					playerPage.UpdateSong("")
				}
			}

			return
		}
	}
}

// HandleSignal handles window resize events.
//
// HandleSignal 处理窗口大小调整事件。
func (p *Library) HandleSignal(sig os.Signal) error {
	if sig == syscall.SIGWINCH {
		p.View()
	}
	return nil
}

// View renders the library page based on the current mode.
//
// View 根据当前模式渲染媒体库页面。
func (p *Library) View() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H")

	title := "Library"
	titleX := (w - len(title)) / 2
	fmt.Printf("\x1b[1;%dH\x1b[1m%s\x1b[0m", titleX, title)

	listHeight := h - 4

	var currentListLength int
	var currentCursor int
	var currentOffset int

	if p.searchQuery != "" {
		currentListLength = len(p.filteredSongPaths)
	} else {
		currentListLength = len(p.entries)
	}
	currentCursor = p.cursor
	currentOffset = p.offset

	if currentCursor < currentOffset {
		currentOffset = currentCursor
	}
	if currentCursor >= currentOffset+listHeight {
		currentOffset = currentCursor - listHeight + 1
	}

	// 更新offset字段，确保滚动位置被保存
	p.offset = currentOffset

	if p.resamplingSong != "" {
		p.drawPathFooter(w, h, "↻ Resampling...")
	} else if p.isSearching || p.searchQuery != "" {
		p.drawSearchFooter(w, h, fmt.Sprintf("Search: %s", p.searchQuery))
	} else {
		p.drawPathFooter(w, h, fmt.Sprintf("Path: %s", filepath.Base(p.currentPath)))
	}

	if p.searchQuery != "" {
		p.renderFilteredListContent(w, h, listHeight, currentOffset)
	} else {
		p.renderDirectoryListContent(w, h, listHeight, currentOffset)
	}

	p.drawScrollbar(h, listHeight, currentListLength, currentOffset)
}

// drawSearchFooter is a helper for drawing the search footer with cursor positioning.
//
// drawSearchFooter 是一个用于绘制带有光标定位的搜索页脚的辅助函数。
func (p *Library) drawSearchFooter(w, h int, footerText string) {
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

// drawPathFooter is a helper for drawing the path footer.
//
// drawPathFooter 是一个用于绘制路径页脚的辅助函数。
func (p *Library) drawPathFooter(w, h int, footerText string) {
	if len(footerText) > w {
		footerText = "..." + footerText[len(footerText)-w+3:]
	}
	footerX := (w - len(footerText)) / 2
	if footerX < 1 {
		footerX = 1
	}
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", h, footerX, footerText)
}

// renderFilteredListContent is a helper for rendering the filtered list content.
//
// renderFilteredListContent 是一个用于渲染筛选后列表内容的辅助函数。
func (p *Library) renderFilteredListContent(w, h, listHeight, currentOffset int) {
	for i := 0; i < listHeight; i++ {
		entryIndex := currentOffset + i
		if entryIndex >= len(p.filteredSongPaths) {
			break
		}

		path := p.filteredSongPaths[entryIndex]
		line := ""
		style := "\x1b[0m"

		info, err := os.Stat(path)
		isDir := err == nil && info.IsDir()

		isSelected := p.selected[path]
		if isDir && !isSelected {
			if cached, ok := p.dirSelectionCache[path]; ok {
				isSelected = cached
			} else {
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
								continue
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
				p.dirSelectionCache[path] = isSelected
			}
		}

		if isSelected {
			line = "✓ " + path
			style += "\x1b[32m"
		} else {
			line = "  " + path
		}

		if isDir {
			line += "/"
		}

		if entryIndex == p.cursor {
			style += "\x1b[7m"
		}
		if runewidth.StringWidth(line) > w-1 {
			for runewidth.StringWidth(line) > w-1 && len(line) > 0 {
				line = line[:len(line)-1]
			}
		}
		fmt.Printf("\x1b[%d;1H\x1b[K%s%s\x1b[0m", i+3, style, line)
	}
}

// renderDirectoryListContent is a helper for rendering the directory list content.
//
// renderDirectoryListContent 是一个用于渲染目录列表内容的辅助函数。
func (p *Library) renderDirectoryListContent(w, h, listHeight, currentOffset int) {
	for i := 0; i < listHeight; i++ {
		entryIndex := currentOffset + i
		if entryIndex >= len(p.entries) {
			break
		}

		libEntry := p.entries[entryIndex]
		fullPath := filepath.Join(p.currentPath, libEntry.entry.Name())
		line, style := p.getDirEntryLine(libEntry, fullPath, entryIndex == p.cursor)
		if runewidth.StringWidth(line) > w-1 {
			for runewidth.StringWidth(line) > w-1 && len(line) > 0 {
				line = line[:len(line)-1]
			}
		}
		fmt.Printf("\x1b[%d;1H\x1b[K%s%s\x1b[0m", i+3, style, line)
	}
}

// getDirEntryLine generates the display line and style for a directory entry.
//
// getDirEntryLine 为目录条目生成显示行和样式。
func (p *Library) getDirEntryLine(libEntry LibraryEntry, fullPath string, isCursor bool) (string, string) {
	line := ""
	style := "\x1b[0m"
	isLink := libEntry.info.Mode()&os.ModeSymlink != 0
	name := libEntry.entry.Name()

	if isLink {
		name += "@"
	}

	if libEntry.isDir {
		isDirPartiallySelected := false
		if cached, ok := p.dirSelectionCache[fullPath]; ok {
			isDirPartiallySelected = cached
		} else {
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
							continue
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
			p.dirSelectionCache[fullPath] = isDirPartiallySelected
		}
		if isDirPartiallySelected {
			line = "✓ " + name + "/"
			style += "\x1b[32m"
		} else {
			line = "▸ " + name + "/"
		}
	} else {
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
//
// drawScrollbar 在屏幕右侧绘制一个滚动条。
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

// NeedsResampling checks if a song needs resampling.
//
// NeedsResampling 检查歌曲是否需要重采样。
func (p *Library) NeedsResampling(songPath string) (bool, error) {
	streamer, format, err := decodeAudioFile(songPath)
	if err != nil {
		return false, err
	}
	streamer.Close()

	return format.SampleRate != p.app.sampleRate, nil
}

// Tick for Library does nothing, as it's event-driven.
//
// Library的Tick方法不执行任何操作，因为它是事件驱动的。
func (p *Library) Tick() {}

// isAudioFile checks if a file has a supported audio extension.
//
// isAudioFile 检查文件是否具有支持的音频扩展名。
func isAudioFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".flac" || ext == ".mp3"
}
