package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/gopxl/beep/v2/speaker"
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
	needRedraw := true

	switch key {
	case '\x1b': // ESC
		return nil, fmt.Errorf("user quit")
	case '\r': // Enter key - play current song
		if len(p.app.Playlist) > 0 && p.cursor >= 0 && p.cursor < len(p.app.Playlist) {
			songPath := p.app.Playlist[p.cursor]
			if err := p.app.PlaySong(songPath); err != nil {
				// 可以在这里显示错误信息，暂时忽略
			}
		}
		needRedraw = false // PlaySong会处理页面切换和重绘
	case 'k', 'w', KeyArrowUp:
		if len(p.app.Playlist) > 0 {
			p.cursor = (p.cursor - 1 + len(p.app.Playlist)) % len(p.app.Playlist)
		}
	case 'j', 's', KeyArrowDown:
		if len(p.app.Playlist) > 0 {
			p.cursor = (p.cursor + 1) % len(p.app.Playlist)
		}
	case ' ': // Remove current song from playlist
		p.removeCurrentSong()
	}

	if needRedraw {
		p.View() // Redraw only when needed
	}
	return nil, nil
}

// removeCurrentSong removes the song at the current cursor position from the playlist
// and deselects it in the Library page.
func (p *PlayList) removeCurrentSong() {
	if p.cursor >= 0 && p.cursor < len(p.app.Playlist) {
		songPath := p.app.Playlist[p.cursor] // Get path before removal
		wasPlayingSong := (p.app.currentSongPath == songPath)

		// Remove from playlist
		p.app.Playlist = append(p.app.Playlist[:p.cursor], p.app.Playlist[p.cursor+1:]...)

		// Find Library page and update its selection state
		for _, page := range p.app.pages {
			if libPage, ok := page.(*Library); ok {
				delete(libPage.selected, songPath)
				break
			}
		}

		// Adjust cursor if it's out of bounds after removal
		if p.cursor >= len(p.app.Playlist) && len(p.app.Playlist) > 0 {
			p.cursor = len(p.app.Playlist) - 1
		} else if len(p.app.Playlist) == 0 {
			p.cursor = 0 // Or handle empty list state
		}

		// 如果删除的是正在播放的歌曲
		if wasPlayingSong {
			if len(p.app.Playlist) > 0 {
				// 播放下一首歌曲（播放当前cursor位置的歌曲，如果超出则播放最后一首）
				nextIndex := p.cursor
				if nextIndex >= len(p.app.Playlist) {
					nextIndex = len(p.app.Playlist) - 1
				}
				nextSong := p.app.Playlist[nextIndex]
				p.app.PlaySongWithSwitch(nextSong, false) // 不跳转页面
			} else {
				// PlayList为空，停止播放并显示空状态
				p.stopPlaybackAndShowEmptyState()
			}
		}
	}
}

// stopPlaybackAndShowEmptyState 停止播放并显示空状态
func (p *PlayList) stopPlaybackAndShowEmptyState() {
	// 停止当前播放
	if p.app.player != nil {
		speaker.Lock()
		p.app.player.ctrl.Paused = true
		speaker.Unlock()
	}

	// 清空播放状态
	p.app.player = nil
	p.app.currentSongPath = ""

	// 停止MPRIS服务
	if p.app.mprisServer != nil {
		p.app.mprisServer.StopService()
		p.app.mprisServer = nil
	}

	// 更新PlayerPage显示空状态
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
	footer := ""
	footerX := (w - len(footer)) / 2
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", h, footerX, footer)

	if len(p.app.Playlist) == 0 {
		msg := "PlayList is empty"
		msg2 := "Add songs from the Library tab"
		msgX := (w - len(msg)) / 2
		msg2X := (w - len(msg2)) / 2
		centerRow := h / 2

		fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", centerRow-1, msgX, msg)
		fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", centerRow+1, msg2X, msg2)
		return
	}

	listHeight := h - 4 // Title, blank line, footer, blank line
	if listHeight < 0 {
		listHeight = 0
	}

	// Adjust offset for scrolling
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+listHeight {
		p.offset = p.cursor - listHeight + 1
	}

	// Draw playlist items
	for i := 0; i < listHeight; i++ {
		trackIndex := p.offset + i
		if trackIndex >= len(p.app.Playlist) {
			break
		}

		trackPath := p.app.Playlist[trackIndex]
		trackName := filepath.Base(trackPath)

		// Styling - check if this is the currently playing song
		style := "\x1b[32m" // Green text for selected
		if trackPath == p.app.currentSongPath {
			style = "\x1b[31m" // Red text for currently playing song
		}
		if trackIndex == p.cursor {
			style += "\x1b[7m" // Reverse video for cursor
		}

		// Truncate line to leave space for scrollbar
		line := fmt.Sprintf("✓ %s", trackName) // Checkmark for selected
		if len(line) > w-1 {
			line = line[:w-1]
		}

		fmt.Printf("\x1b[%d;1H\x1b[K%s%s\x1b[0m", i+3, style, line) // Start list from line 3
	}

	// Draw Scrollbar if needed
	totalItems := len(p.app.Playlist)
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
