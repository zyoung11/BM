package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"bm/search"

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

// searchReturnState stores the search view state to restore when the user exits
// a directory that was entered from the search results.
//
// searchReturnState 保存从搜索结果进入目录后退出该目录时需要恢复的搜索视图状态。
type searchReturnState struct {
	browsePath  string
	enteredPath string
	query       string
	cursor      int
	offset      int
}

// Library browses the music directory and adds songs to the playlist.
//
// Library 浏览音乐目录并将歌曲添加到播放列表。
type Library struct {
	app *App

	entries             []LibraryEntry // All entries in the current directory. / 当前目录中的所有条目。
	currentPath         string
	initialPath         string // The starting path provided to the application. / 提供给应用程序的起始路径。
	cursor              int
	selected            map[string]bool // Use file path as key for persistent selection. / 使用文件路径作为持久选择的键。
	offset              int             // For scrolling the view. / 用于滚动视图。
	pathHistory         map[string]int  // Store cursor position for each path. / 存储每个路径的光标位置。
	columns             []libraryColumn
	previewEntriesCache map[string][]LibraryEntry
	isSearching         bool
	searchQuery         string
	// searchCursor is the UI cursor on the search results.
	//
	// searchCursor 是搜索结果上的UI光标。
	searchCursor int
	// searchOffset is the scroll offset of the search results.
	//
	// searchOffset 是搜索结果的滚动偏移。
	searchOffset int
	// searchReturns stacks the saved search states of directories entered from
	// the search results.
	//
	// searchReturns 堆叠保存从搜索结果进入目录时暂存的搜索状态。
	searchReturns     []searchReturnState
	globalFileCache   []string // Cache of all audio file paths. / 所有音频文件路径的缓存。
	filteredSongPaths []string // Results of the current search. / 当前搜索的结果。
	searchEngine      *search.Engine
	searchDirCount    int             // Number of dirs in filtered results, for separator. / 筛选结果中目录数量，用于分割线。
	dirSelectionCache map[string]bool // Cache for directory partial selection state. / 目录部分选择状态的缓存。
	lastRemoveTime    time.Time       // Debounce mechanism for removing currently playing song. / 移除当前播放歌曲的防抖机制。
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
		lastRemoveTime:    time.Time{},
		searchEngine:      search.New(),
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
		currentPath:       filepath.Clean(startPath),
		initialPath:       filepath.Clean(startPath),
		selected:          selectedSongs,
		pathHistory:       make(map[string]int),
		dirSelectionCache: make(map[string]bool),
		lastRemoveTime:    time.Time{},
		searchEngine:      search.New(),
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

	p.entries = readLibraryEntries(path)
	p.cursor = min(p.cursor, max(len(p.entries)-1, 0))
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
	p.searchEngine.BuildFromPaths(cache)
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
		score    float64
		isDir    bool
		itemName string
	}
	var scoredItems []scoredItem

	for _, path := range p.globalFileCache {
		score := p.searchEngine.Match(p.searchQuery, path)
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
		if math.Abs(scoredItems[i].score-scoredItems[j].score) > 0.0001 {
			return scoredItems[i].score > scoredItems[j].score
		}
		return strings.ToLower(scoredItems[i].itemName) < strings.ToLower(scoredItems[j].itemName)
	})

	maxDirs := GlobalConfig.App.MaxSearchDirs
	if maxDirs <= 0 {
		maxDirs = 15
	}

	p.filteredSongPaths = make([]string, 0, len(scoredItems))
	p.searchDirCount = 0
	for _, scored := range scoredItems {
		if scored.isDir && p.searchDirCount >= maxDirs {
			continue
		}
		p.filteredSongPaths = append(p.filteredSongPaths, scored.path)
		if scored.isDir {
			p.searchDirCount++
		}
	}
	p.clampSearchCursor()
}

// clampSearchCursor keeps the search cursor within the bounds of the filtered results.
//
// clampSearchCursor 将搜索光标保持在过滤结果的范围内。
func (p *Library) clampSearchCursor() {
	if p.searchCursor >= len(p.filteredSongPaths) {
		p.searchCursor = max(len(p.filteredSongPaths)-1, 0)
	}
	if p.searchCursor < 0 {
		p.searchCursor = 0
	}
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
			p.searchCursor = 0
			p.searchOffset = 0
			p.filterSongs()
		}
	} else if key == KeyArrowUp || key == KeyArrowDown || key == KeyArrowLeft || key == KeyArrowRight {
		p.isSearching = false
	} else {
		if key >= 32 {
			p.searchQuery += string(key)
			p.searchCursor = 0
			p.searchOffset = 0
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
			oldCursor := p.cursor
			p.cursor = (p.cursor - 1 + len(p.entries)) % len(p.entries)
			return nil, p.tryFastCursorMove(oldCursor), nil
		}
	} else if IsKey(key, GlobalConfig.Keymap.Library.NavDown) {
		if len(p.entries) > 0 {
			oldCursor := p.cursor
			p.cursor = (p.cursor + 1) % len(p.entries)
			return nil, p.tryFastCursorMove(oldCursor), nil
		}
	} else if IsKey(key, GlobalConfig.Keymap.Library.NavEnterDir) {
		if p.cursor < len(p.entries) && p.entries[p.cursor].isDir {
			p.pushColumn()
			newPath := filepath.Join(p.currentPath, p.entries[p.cursor].entry.Name())
			p.scanDirectory(newPath)
		}
	} else if IsKey(key, GlobalConfig.Keymap.Library.NavExitDir) {
		p.exitDir()
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
			oldCursor := p.searchCursor
			p.searchCursor = (p.searchCursor - 1 + len(p.filteredSongPaths)) % len(p.filteredSongPaths)
			return nil, p.tryFastCursorMove(oldCursor), nil
		}
	} else if IsKey(key, GlobalConfig.Keymap.Library.NavDown) {
		if len(p.filteredSongPaths) > 0 {
			oldCursor := p.searchCursor
			p.searchCursor = (p.searchCursor + 1) % len(p.filteredSongPaths)
			return nil, p.tryFastCursorMove(oldCursor), nil
		}
	} else if IsKey(key, GlobalConfig.Keymap.Library.NavEnterDir) {
		p.enterDirFromSearchResults()
	} else if IsKey(key, GlobalConfig.Keymap.Library.ToggleSelect) {
		if p.searchCursor < len(p.filteredSongPaths) {
			path := p.filteredSongPaths[p.searchCursor]
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
				if allSelected {
					p.removeSongsFromPlaylistBatch(songsInDir)
				} else {
					p.addSongsToPlaylistBatch(songsInDir)
				}
			} else {
				p.toggleSelection(path)
			}
			if p.searchCursor < len(p.filteredSongPaths)-1 {
				p.searchCursor++
			}
		}
	} else if IsKey(key, GlobalConfig.Keymap.Library.ToggleSelectAll) {
		p.toggleSelectAll(true)
	}
	return nil, false, nil
}

// enterDirFromSearchResults enters the directory under the cursor in the search
// results and saves the search view state to restore when the user exits it.
//
// enterDirFromSearchResults 进入搜索结果中光标所在的目录，并保存退出该目录时
// 需要恢复的搜索视图状态。
func (p *Library) enterDirFromSearchResults() {
	if p.searchCursor < 0 || p.searchCursor >= len(p.filteredSongPaths) {
		return
	}
	path := p.filteredSongPaths[p.searchCursor]
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return
	}
	p.searchReturns = append(p.searchReturns, searchReturnState{
		browsePath:  p.currentPath,
		enteredPath: path,
		query:       p.searchQuery,
		cursor:      p.searchCursor,
		offset:      p.searchOffset,
	})
	p.pushColumn()
	p.searchQuery = ""
	p.searchCursor = 0
	p.searchOffset = 0
	p.scanDirectory(path)
}

// exitDir steps out of the current directory, or leaves a directory that was
// entered from the search results and returns to the results. It reports whether
// the view moved.
//
// exitDir 退出当前目录一层，或退出从搜索结果进入的目录并回到搜索结果。
// 返回值表示视图是否发生了移动。
func (p *Library) exitDir() bool {
	if p.exitToSearchResults() {
		return true
	}
	currentAbs, _ := filepath.Abs(p.currentPath)
	initialAbs, _ := filepath.Abs(p.initialPath)
	if currentAbs == initialAbs {
		return false
	}
	if p.popColumn() {
		return true
	}
	p.scanDirectory(filepath.Dir(p.currentPath))
	return true
}

// escapeBack performs one outward step: leave the search input, clear the
// search results or step out of a directory. It reports whether anything was
// still left to back out of.
//
// escapeBack 执行一步向外操作：退出搜索输入、清除搜索结果或退出一层目录。
// 返回值表示是否还有可后退的空间。
func (p *Library) escapeBack() bool {
	if p.isSearching {
		p.isSearching = false
		p.searchQuery = ""
		p.filterSongs()
		return true
	}
	if p.searchQuery != "" {
		p.searchQuery = ""
		p.filterSongs()
		return true
	}
	return p.exitDir()
}

// exitToSearchResults leaves a directory that was entered from the search results
// and restores the search view state saved on entry. It reports whether the
// switch happened.
//
// exitToSearchResults 退出从搜索结果进入的目录，并恢复进入时保存的搜索视图状态。
// 返回值表示是否完成了该切换。
func (p *Library) exitToSearchResults() bool {
	if len(p.searchReturns) == 0 {
		return false
	}
	top := p.searchReturns[len(p.searchReturns)-1]
	if filepath.Clean(top.enteredPath) != filepath.Clean(p.currentPath) {
		return false
	}
	p.searchReturns = p.searchReturns[:len(p.searchReturns)-1]
	p.searchQuery = top.query
	p.searchCursor = top.cursor
	p.searchOffset = top.offset
	p.popColumn()
	p.filterSongs()
	return true
}

// HandleKey routes user input based on the current mode (directory view, search results, or search input).
//
// HandleKey 根据当前模式（目录视图、搜索结果或搜索输入）路由用户输入。
func (p *Library) HandleKey(key rune) (Page, bool, error) {
	var err error
	var page Page
	redrawn := false

	if p.isSearching {
		p.handleSearchInput(key)
	} else if p.searchQuery != "" {
		page, redrawn, err = p.handleSearchViewInput(key)
	} else {
		page, redrawn, err = p.handleDirViewInput(key)
	}

	if !redrawn {
		p.View()
	}
	return page, false, err
}

// tryFastCursorMove redraws only the old and new cursor rows after a cursor move
// that keeps the view window in place. It reports whether the redraw already
// happened and only runs in differential rendering mode.
//
// tryFastCursorMove 在光标移动不改变视图窗口时只重绘新旧光标行。
// 返回值表示是否已完成重绘，仅在差分渲染模式下生效。
func (p *Library) tryFastCursorMove(oldCursor int) bool {
	if !p.app.diffRender {
		return false
	}
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}
	listHeight := h - 4

	if p.searchQuery != "" {
		return p.tryFastSearchMove(w, listHeight, oldCursor)
	}

	newOffset := min(p.cursor, p.offset)
	if p.cursor >= newOffset+listHeight {
		newOffset = p.cursor - listHeight + 1
	}
	if newOffset != p.offset {
		return false
	}

	geoms, _ := p.planColumns(w)
	if len(geoms) == 0 {
		return false
	}
	var buf strings.Builder
	buf.WriteString(p.redrawColumnRow(geoms[len(geoms)-1], oldCursor))
	buf.WriteString(p.redrawColumnRow(geoms[len(geoms)-1], p.cursor))
	buf.WriteString(p.redrawPreviewArea(w, listHeight))
	fmt.Print(buf.String())
	return true
}

// tryFastSearchMove redraws only the old and new search cursor rows after a move
// that keeps the results window in place. It reports whether the redraw already
// happened.
//
// tryFastSearchMove 在光标移动不改变结果窗口时只重绘新旧光标行。
// 返回值表示是否已完成重绘。
func (p *Library) tryFastSearchMove(w, listHeight, oldCursor int) bool {
	dirCount := p.searchDirCount
	hasSep := dirCount > 0 && dirCount < len(p.filteredSongPaths)
	effectiveHeight := listHeight
	if hasSep {
		effectiveHeight--
	}

	newOffset := min(p.searchCursor, p.searchOffset)
	if p.searchCursor >= newOffset+effectiveHeight {
		newOffset = p.searchCursor - effectiveHeight + 1
	}
	if newOffset != p.searchOffset {
		return false
	}

	geoms, _ := p.planColumns(w)
	if len(geoms) == 0 {
		return false
	}
	var buf strings.Builder
	buf.WriteString(p.redrawSearchRow(geoms[len(geoms)-1], oldCursor))
	buf.WriteString(p.redrawSearchRow(geoms[len(geoms)-1], p.searchCursor))
	buf.WriteString(p.redrawPreviewArea(w, listHeight))
	fmt.Print(buf.String())
	return true
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

		if allSelected {
			p.removeSongsFromPlaylistBatch(songsInDir)
		} else {
			p.addSongsToPlaylistBatch(songsInDir)
		}
	}
	// Clear cache on selection change
	p.dirSelectionCache = make(map[string]bool)
	p.previewEntriesCache = nil
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

	if allCurrentlySelected {
		p.removeSongsFromPlaylistBatch(allSongs)
	} else {
		p.addSongsToPlaylistBatch(allSongs)
	}
	// Clear cache on selection change
	p.dirSelectionCache = make(map[string]bool)
	p.previewEntriesCache = nil
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
		found := slices.Contains(p.app.Playlist, path)
		if !found {
			p.app.setPlaylist(append(p.app.Playlist, path))
			if len(p.app.Playlist) == 1 {
				p.app.PlaySongWithSwitchAndRender(path, false, false)
			}
		}
	}
	// Clear cache on selection change
	p.dirSelectionCache = make(map[string]bool)
	p.previewEntriesCache = nil
	if err := SavePlaylist(p.app.Playlist, p.initialPath); err != nil {
		l.Warnf("failed to save playlist: %v\n\n警告: 保存播放列表失败: %v", err, err)
	}
}

// removeSongFromPlaylist removes a song path from the app's playlist.
//
// removeSongFromPlaylist 从应用的播放列表中删除一个歌曲路径。
func (p *Library) removeSongFromPlaylist(songPath string) {
	for i, s := range p.app.Playlist {
		if s == songPath {
			wasPlayingSong := (p.app.currentSongPath == songPath)

			// 防抖机制：防止快速连续移除当前播放的歌曲
			if wasPlayingSong {
				currentTime := time.Now()
				debounceMs := GlobalConfig.App.SwitchDebounceMs
				if debounceMs == 0 {
					debounceMs = 200
				}
				if currentTime.Sub(p.lastRemoveTime) < time.Duration(debounceMs)*time.Millisecond {
					return
				}
				p.lastRemoveTime = currentTime
			}

			p.app.setPlaylist(append(p.app.Playlist[:i], p.app.Playlist[i+1:]...))

			p.app.removeFromPlayHistory(songPath)

			if len(p.app.Playlist) == 0 {
				if p.app.player != nil {
					speaker.Lock()
					if p.app.player.ctrl != nil {
						p.app.player.ctrl.Paused = true
					}
					speaker.Unlock()
				}
				p.app.player = nil
				p.app.setCurrentSong("")
				if p.app.mprisServer != nil {
					p.app.mprisServer.StopService()
					p.app.mprisServer = nil
				}
				if playerPage, ok := p.app.pages[0].(*PlayerPage); ok {
					playerPage.UpdateSong("")
				}
			} else if wasPlayingSong {
				// 如果移除的是正在播放的歌曲，播放下一首
				nextIndex := i
				if nextIndex >= len(p.app.Playlist) {
					nextIndex = len(p.app.Playlist) - 1
				}
				p.app.PlaySongWithSwitchAndRender(p.app.Playlist[nextIndex], false, false)
			}

			return
		}
	}
}

// addSongsToPlaylistBatch adds a batch of songs to the playlist in a single
// update and starts playback only when the playlist was empty.
//
// addSongsToPlaylistBatch 一次性向播放列表批量添加歌曲，
// 仅在播放列表为空时启动播放。
func (p *Library) addSongsToPlaylistBatch(songPaths []string) {
	if len(songPaths) == 0 {
		return
	}

	existing := make(map[string]bool, len(p.app.Playlist))
	for _, songPath := range p.app.Playlist {
		existing[songPath] = true
	}

	newPlaylist := append([]string(nil), p.app.Playlist...)
	changed := false
	for _, songPath := range songPaths {
		if p.selected[songPath] {
			continue
		}
		p.selected[songPath] = true
		changed = true
		if !existing[songPath] {
			existing[songPath] = true
			newPlaylist = append(newPlaylist, songPath)
		}
	}
	if !changed {
		return
	}

	wasEmpty := len(p.app.Playlist) == 0
	p.app.setPlaylist(newPlaylist)
	p.dirSelectionCache = make(map[string]bool)
	p.previewEntriesCache = nil
	if err := SavePlaylist(p.app.Playlist, p.initialPath); err != nil {
		l.Warnf("failed to save playlist: %v\n\n警告: 保存播放列表失败: %v", err, err)
	}
	if wasEmpty && len(newPlaylist) > 0 {
		p.app.PlaySongWithSwitchAndRender(newPlaylist[0], false, false)
	}
}

// removeSongsFromPlaylistBatch removes a batch of songs from the playlist in a
// single update and switches playback at most once when the current song is
// part of the batch. The next song is chosen from the playlist after the whole
// batch is gone, so removed songs never become candidates.
//
// removeSongsFromPlaylistBatch 一次性从播放列表批量移除歌曲，
// 当前播放歌曲在批次中时最多只切换一次播放，下一首从移除后的播放列表中选取，
// 被移除的歌曲不会再成为候选项。
func (p *Library) removeSongsFromPlaylistBatch(songPaths []string) {
	if len(songPaths) == 0 {
		return
	}

	toRemove := make(map[string]bool, len(songPaths))
	for _, songPath := range songPaths {
		toRemove[songPath] = true
	}

	playingIndex := -1
	for i, songPath := range p.app.Playlist {
		if songPath == p.app.currentSongPath {
			playingIndex = i
			break
		}
	}
	wasPlayingRemoved := playingIndex >= 0 && toRemove[p.app.currentSongPath]

	keptBefore := 0
	newPlaylist := make([]string, 0, len(p.app.Playlist))
	for i, songPath := range p.app.Playlist {
		if toRemove[songPath] {
			continue
		}
		if playingIndex >= 0 && i < playingIndex {
			keptBefore++
		}
		newPlaylist = append(newPlaylist, songPath)
	}

	changed := len(newPlaylist) != len(p.app.Playlist)
	for _, songPath := range songPaths {
		if p.selected[songPath] {
			delete(p.selected, songPath)
			changed = true
		}
	}
	if !changed {
		return
	}

	p.dirSelectionCache = make(map[string]bool)
	p.previewEntriesCache = nil
	p.app.invalidatePendingNext()
	p.app.setPlaylist(newPlaylist)
	for _, songPath := range songPaths {
		p.app.removeFromPlayHistory(songPath)
	}
	if err := SavePlaylist(p.app.Playlist, p.initialPath); err != nil {
		l.Warnf("failed to save playlist: %v\n\n警告: 保存播放列表失败: %v", err, err)
	}

	if !wasPlayingRemoved {
		return
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
		p.app.setCurrentSong("")
		if p.app.mprisServer != nil {
			p.app.mprisServer.StopService()
			p.app.mprisServer = nil
		}
		if playerPage, ok := p.app.pages[0].(*PlayerPage); ok {
			playerPage.UpdateSong("")
		}
		return
	}

	nextIndex := keptBefore
	if nextIndex >= len(p.app.Playlist) {
		nextIndex = len(p.app.Playlist) - 1
	}
	p.lastRemoveTime = time.Now()
	p.app.PlaySongWithSwitchAndRender(p.app.Playlist[nextIndex], false, false)
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
	p.app.beginFrame()
	defer p.app.endFrame()
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	var buf strings.Builder
	if !p.app.diffRender {
		buf.WriteString("\x1b[2J\x1b[3J\x1b[H")
	}

	title := "Library"
	titleX := (w - len(title)) / 2
	fmt.Fprintf(&buf, "\x1b[1;1H\x1b[K\x1b[1;%dH\x1b[1m%s\x1b[0m", titleX, title)
	buf.WriteString("\x1b[2;1H\x1b[K")

	listHeight := h - 4

	if w < 20 || h < 8 {
		for row := 2; row <= h; row++ {
			fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[K", row)
		}
		msg := "Terminal too small to browse"
		if GlobalConfig.App.HelpLanguage == "zh" {
			msg = "终端过小，无法浏览媒体库"
		}
		msg = truncateToWidthFromStart(msg, max(w-1, 1))
		x := max((w-runewidth.StringWidth(msg))/2, 0) + 1
		fmt.Fprintf(&buf, "\x1b[%d;%dH\x1b[90m%s\x1b[0m", max(h/2, 1), x, msg)
		fmt.Print(buf.String())
		return
	}

	isSearchView := p.searchQuery != ""

	var currentListLength int
	if isSearchView {
		currentListLength = len(p.filteredSongPaths)
	} else {
		currentListLength = len(p.entries)
	}

	var currentCursor int
	var currentOffset int
	if isSearchView {
		currentCursor = p.searchCursor
		currentOffset = p.searchOffset
	} else {
		currentCursor = p.cursor
		currentOffset = p.offset
	}

	effectiveHeight := listHeight
	if isSearchView && p.searchDirCount > 0 && p.searchDirCount < len(p.filteredSongPaths) {
		effectiveHeight--
	}
	if currentCursor < currentOffset {
		currentOffset = currentCursor
	}
	if currentCursor >= currentOffset+effectiveHeight {
		currentOffset = currentCursor - effectiveHeight + 1
	}

	if isSearchView {
		p.searchOffset = currentOffset
	} else {
		p.offset = currentOffset
	}

	if p.isSearching || p.searchQuery != "" {
		buf.WriteString(p.drawSearchFooter(w, h, fmt.Sprintf("Search: %s", p.searchQuery)))
	} else {
		buf.WriteString(p.drawPathFooter(w, h, fmt.Sprintf("Path: %s", p.rootDisplayPath())))
	}

	buf.WriteString(p.renderColumns(w, listHeight))

	fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[K", h-1)

	buf.WriteString(p.drawScrollbar(listHeight, currentListLength, currentOffset))
	fmt.Print(buf.String())
}

// rootDisplayPath returns the current path shown relative to the music library
// root, using the user's music folder name as the root node, e.g. "music/JENNIE".
//
// rootDisplayPath 返回以用户音乐文件夹名作为根节点的当前路径显示文本，
// 例如 "music/JENNIE"。
func (p *Library) rootDisplayPath() string {
	root := filepath.Base(filepath.Clean(p.initialPath))
	if root == "." || root == string(filepath.Separator) {
		root = filepath.Clean(p.initialPath)
	}
	rel, err := filepath.Rel(p.initialPath, p.currentPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return root
	}
	return filepath.Join(root, rel)
}

// drawSearchFooter is a helper for drawing the search footer with cursor positioning.
//
// drawSearchFooter 是一个用于绘制带有光标定位的搜索页脚的辅助函数。
func (p *Library) drawSearchFooter(w, h int, footerText string) string {
	footerText = truncateToWidth(footerText, w)
	footerX := max((w-len(footerText))/2, 1)
	out := fmt.Sprintf("\x1b[%d;1H\x1b[K\x1b[%d;%dH\x1b[37m%s\x1b[0m", h, h, footerX, footerText)
	if p.isSearching {
		cursorX := footerX + len("Search: ") + len(p.searchQuery)
		if cursorX <= w {
			out += fmt.Sprintf("\x1b[%d;%dH█", h, cursorX)
		}
	}
	return out
}

// drawPathFooter is a helper for drawing the path footer.
//
// drawPathFooter 是一个用于绘制路径页脚的辅助函数。
func (p *Library) drawPathFooter(w, h int, footerText string) string {
	footerText = truncateToWidth(footerText, w)
	footerX := max((w-len(footerText))/2, 1)
	return fmt.Sprintf("\x1b[%d;1H\x1b[K\x1b[%d;%dH\x1b[37m%s\x1b[0m", h, h, footerX, footerText)
}

// truncateToWidth shortens text with a leading ellipsis so that its display
// width fits within the given width.
//
// truncateToWidth 以省略号开头对文本进行截断，使其显示宽度不超过给定宽度。
func truncateToWidth(text string, w int) string {
	if w <= 3 {
		return ""
	}
	if runewidth.StringWidth(text) <= w {
		return text
	}
	runes := []rune(text)
	for len(runes) > 0 && runewidth.StringWidth("..."+string(runes)) > w {
		runes = runes[1:]
	}
	return "..." + string(runes)
}

// truncateToWidthFromStart shortens text with a trailing ellipsis so that its
// display width fits within the given width.
//
// truncateToWidthFromStart 以结尾省略号对文本进行截断，使其显示宽度不超过给定宽度。
func truncateToWidthFromStart(text string, w int) string {
	if w <= 3 {
		return ""
	}
	if runewidth.StringWidth(text) <= w {
		return text
	}
	runes := []rune(text)
	for len(runes) > 0 && runewidth.StringWidth(string(runes)+"...") > w {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "..."
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
			isDirPartiallySelected = p.dirHasSelection(fullPath)
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
func (p *Library) drawScrollbar(listHeight, totalItems, currentOffset int) string {
	w, _, _ := term.GetSize(int(os.Stdout.Fd()))

	thumbSize := 0
	thumbStart := 0
	if totalItems > listHeight {
		thumbSize = max(listHeight*listHeight/totalItems, 1)
		scrollRange := totalItems - listHeight
		thumbRange := listHeight - thumbSize
		if scrollRange > 0 {
			thumbStart = currentOffset * thumbRange / scrollRange
		}
	}

	var buf strings.Builder
	for i := range listHeight {
		cell := "│"
		if totalItems <= listHeight {
			cell = " "
		} else if i >= thumbStart && i < thumbStart+thumbSize {
			cell = "┃"
		}
		fmt.Fprintf(&buf, "\x1b[%d;%dH%s", i+3, w, cell)
	}
	return buf.String()
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
	return ext == ".flac" || ext == ".mp3" || ext == ".wav" || ext == ".ogg"
}
