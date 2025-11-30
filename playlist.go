package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/gopxl/beep/v2/speaker"
	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

// PlayList displays the list of songs to be played.
//
// PlayList 显示要播放的歌曲列表。
type PlayList struct {
	app    *App
	cursor int // The UI cursor on the viewPlaylist. / UI在viewPlaylist上的光标。
	offset int

	isSearching     bool
	searchQuery     string
	viewPlaylist    []string // The filtered playlist to be displayed. / 要显示的已过滤播放列表。
	originalIndices []int    // Map from viewPlaylist index to app.Playlist index. / 从viewPlaylist索引到app.Playlist索引的映射。

	// Debounce mechanism to prevent accidental rapid removal of the current song.
	// 防抖机制，防止快速连续移除当前播放歌曲。
	lastRemoveTime time.Time
}

// NewPlayList creates a new instance of PlayList.
//
// NewPlayList 创建一个新的 PlayList 实例。
func NewPlayList(app *App) *PlayList {
	return &PlayList{
		app:             app,
		isSearching:     false,
		searchQuery:     "",
		viewPlaylist:    make([]string, 0),
		originalIndices: make([]int, 0),
	}
}

// Init prepares the initial view for the playlist.
//
// Init 准备播放列表的初始视图。
func (p *PlayList) Init() {
	p.filterPlaylist()
}

// filterPlaylist updates the viewPlaylist based on the searchQuery.
//
// filterPlaylist 根据 searchQuery 更新 viewPlaylist。
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

		sort.Slice(scoredSongs, func(i, j int) bool {
			return scoredSongs[i].score > scoredSongs[j].score
		})

		for _, scored := range scoredSongs {
			p.viewPlaylist = append(p.viewPlaylist, scored.path)
			p.originalIndices = append(p.originalIndices, scored.index)
		}
	}

	p.cursor = 0
	p.offset = 0
}

// HandleKey handles user input for the playlist.
//
// HandleKey 处理播放列表的用户输入。
func (p *PlayList) HandleKey(key rune) (Page, bool, error) {
	if p.isSearching {
		if IsKey(key, GlobalConfig.Keymap.Playlist.SearchMode.ConfirmSearch) {
			p.isSearching = false
		} else if IsKey(key, GlobalConfig.Keymap.Playlist.SearchMode.EscapeSearch) {
			p.isSearching = false
			p.searchQuery = ""
			p.filterPlaylist()
		} else if IsKey(key, GlobalConfig.Keymap.Playlist.SearchMode.SearchBackspace) {
			if len(p.searchQuery) > 0 {
				runes := []rune(p.searchQuery)
				p.searchQuery = string(runes[:len(runes)-1])
				p.filterPlaylist()
			}
		} else if key == KeyArrowUp || key == KeyArrowDown || key == KeyArrowLeft || key == KeyArrowRight {
			p.isSearching = false
		} else {
			if key >= 32 {
				p.searchQuery += string(key)
				p.filterPlaylist()
			}
		}
		p.View()
		return nil, false, nil
	}

	needRedraw := true
	if IsKey(key, GlobalConfig.Keymap.Playlist.SearchMode.EscapeSearch) {
		if p.searchQuery != "" {
			p.searchQuery = ""
			p.filterPlaylist()
		}
	} else if IsKey(key, GlobalConfig.Keymap.Playlist.Search) {
		p.isSearching = true
	} else if IsKey(key, GlobalConfig.Keymap.Playlist.PlaySong) {
		if len(p.viewPlaylist) > 0 && p.cursor >= 0 && p.cursor < len(p.viewPlaylist) {
			songPath := p.viewPlaylist[p.cursor]
			if err := p.app.PlaySongWithSwitch(songPath, false); err != nil {
				// Handle error
			}
		}
		needRedraw = false
	} else if IsKey(key, GlobalConfig.Keymap.Playlist.NavUp) {
		if len(p.viewPlaylist) > 0 {
			p.cursor = (p.cursor - 1 + len(p.viewPlaylist)) % len(p.viewPlaylist)
		}
	} else if IsKey(key, GlobalConfig.Keymap.Playlist.NavDown) {
		if len(p.viewPlaylist) > 0 {
			p.cursor = (p.cursor + 1) % len(p.viewPlaylist)
		}
	} else if IsKey(key, GlobalConfig.Keymap.Playlist.RemoveSong) {
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
	return nil, false, nil
}

// removeCurrentSong removes the song at the current cursor position.
//
// removeCurrentSong 删除当前光标位置的歌曲。
func (p *PlayList) removeCurrentSong() {
	if p.cursor < 0 || p.cursor >= len(p.viewPlaylist) {
		return
	}

	originalIndex := p.originalIndices[p.cursor]
	songPath := p.app.Playlist[originalIndex]
	wasPlayingSong := (p.app.currentSongPath == songPath)

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

	p.app.Playlist = append(p.app.Playlist[:originalIndex], p.app.Playlist[originalIndex+1:]...)
	if err := SavePlaylist(p.app.Playlist, p.app.LibraryPath); err != nil {
		log.Printf("Warning: failed to save playlist: %v\n\n警告: 保存播放列表失败: %v", err, err)
	}

	for _, page := range p.app.pages {
		if libPage, ok := page.(*Library); ok {
			delete(libPage.selected, songPath)
			if libPage.searchQuery != "" {
				libPage.filterSongs()
			}
			break
		}
	}

	p.filterPlaylist()

	if len(p.app.Playlist) == 0 {
		p.stopPlaybackAndShowEmptyState()
	} else if wasPlayingSong {
		nextIndex := originalIndex
		if nextIndex >= len(p.app.Playlist) {
			nextIndex = len(p.app.Playlist) - 1
		}
		p.app.PlaySongWithSwitchAndRender(p.app.Playlist[nextIndex], false, false)
	}
}

// stopPlaybackAndShowEmptyState stops playback and shows an empty state for the player page.
//
// stopPlaybackAndShowEmptyState 停止播放并显示播放器页面的空状态。
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
//
// HandleSignal 在调整大小时重绘视图。
func (p *PlayList) HandleSignal(sig os.Signal) error {
	if sig == syscall.SIGWINCH {
		p.View()
	}
	return nil
}

// View renders the playlist.
//
// View 渲染播放列表。
func (p *PlayList) View() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H")

	title := "PlayList"
	titleX := (w - len(title)) / 2
	fmt.Printf("\x1b[1;%dH\x1b[1m%s\x1b[0m", titleX, title)

	listHeight := h - 4

	var footer string
	if p.isSearching || p.searchQuery != "" {
		footer = fmt.Sprintf("Search: %s", p.searchQuery)
	} else {
		footer = ""
	}
	if len(footer) > w {
		footer = "..." + footer[len(footer)-w+3:]
	}
	footerX := (w - len(footer)) / 2
	if footerX < 1 {
		footerX = 1
	}
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", h, footerX, footer)
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

		style := "\x1b[32m"
		if trackPath == p.app.currentSongPath {
			style = "\x1b[31m"
		}
		if p.app.IsFileCorrupted(trackPath) {
			style = "\x1b[33m"
		}
		if trackIndex == p.cursor {
			style += "\x1b[7m"
		}

		prefix := "✓"
		if p.app.IsFileCorrupted(trackPath) {
			prefix = "⚠"
		}
		line := fmt.Sprintf("%s %s", prefix, trackName)
		if runewidth.StringWidth(line) > w-1 {
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
				fmt.Printf("\x1b[%d;%dH┃", i+3, w)
			} else {
				fmt.Printf("\x1b[%d;%dH│", i+3, w)
			}
		}
	}
}

// Tick for PlayList does nothing, as it's event-driven.
//
// PlayList的Tick方法不执行任何操作，因为它是事件驱动的。
func (p *PlayList) Tick() {}
