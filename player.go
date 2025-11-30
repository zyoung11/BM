package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"math"
	"math/rand"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/dhowden/tag"
	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/effects"
	"github.com/gopxl/beep/v2/flac"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/mattn/go-runewidth"
	"github.com/nfnt/resize"
	"golang.org/x/term"
)

// min returns the smaller of two values.
//
// min 返回两个值中较小的一个。
func min[T ~int | ~float64](a, b T) T {
	if a < b {
		return a
	}
	return b
}

// max returns the larger of two values.
//
// max 返回两个值中较大的一个。
func max[T ~int | ~float64](a, b T) T {
	if a > b {
		return a
	}
	return b
}

// --- Page Implementation ---

// PlayerPage holds the state for the music player view.
//
// PlayerPage 保存音乐播放器视图的状态。
type PlayerPage struct {
	app      *App
	flacPath string

	// UI state / UI状态
	cellW, cellH                          int
	imageTop, imageHeight, imageRightEdge int
	coverColorR, coverColorG, coverColorB int
	useCoverColor                         bool
	volumeDisplayTimer                    int
	rateDisplayTimer                      int
	resampleDisplayTimer                  int  // Timer for showing the resampling indicator. / 用于显示重采样提示的计时器。
	textTooLongForWide                    bool // True if text is too long for wide terminal mode. / 如果文本太长不适合宽终端模式则为true。
	showTextInWideMode                    bool // True if text can be shown below image in wide mode. / 如果可以在宽终端模式下在图片下方显示文本则为true。

	// Debounce mechanism for song switching. / 切歌防抖机制。
	lastSwitchTime time.Time
}

// NewPlayerPage creates a new instance of the player page.
//
// NewPlayerPage 创建一个新的播放器页面实例。
func NewPlayerPage(app *App, flacPath string, cellW, cellH int) *PlayerPage {
	return &PlayerPage{
		app:            app,
		flacPath:       flacPath,
		useCoverColor:  true,
		cellW:          cellW,
		cellH:          cellH,
		lastSwitchTime: time.Now().Add(-2 * time.Second),
	}
}

// Init for PlayerPage is a placeholder, as setup is done in the constructor.
//
// PlayerPage的Init是一个占位符，因为设置在构造函数中完成。
func (p *PlayerPage) Init() {}

// UpdateSong updates the path of the currently playing song.
//
// UpdateSong 更新当前播放歌曲的路径。
func (p *PlayerPage) UpdateSong(songPath string) {
	p.flacPath = songPath
	p.imageTop = 0
	p.imageHeight = 0
	p.imageRightEdge = 0
}

// UpdateSongWithRender updates the currently playing song path and forces a re-render.
//
// UpdateSongWithRender 更新当前播放的歌曲路径并强制重新渲染。
func (p *PlayerPage) UpdateSongWithRender(songPath string) {
	p.flacPath = songPath
	p.imageTop = 0
	p.imageHeight = 0
	p.imageRightEdge = 0
	p.View()
}

// HandleKey handles user key presses for the player page.
//
// HandleKey 处理播放器页面的用户按键。
func (p *PlayerPage) HandleKey(key rune) (Page, bool, error) {
	player := p.app.player
	mprisServer := p.app.mprisServer
	needsRedraw := true

	if player == nil {
		return nil, false, nil
	}

	if IsKey(key, GlobalConfig.Keymap.Player.TogglePause) {
		speaker.Lock()
		player.ctrl.Paused = !player.ctrl.Paused
		speaker.Unlock()
		if mprisServer != nil {
			mprisServer.UpdatePlaybackStatus(!player.ctrl.Paused)
		}
	} else if IsKey(key, GlobalConfig.Keymap.Player.SeekBackward) {
		speaker.Lock()
		newPos := player.streamer.Position() - player.sampleRate.N(time.Second*5)
		if newPos < 0 {
			newPos = 0
		}
		if err := player.streamer.Seek(newPos); err != nil {
			// ignore seek errors
		}
		speaker.Unlock()
		if mprisServer != nil {
			mprisServer.UpdatePosition(p.currentPositionInMicroseconds())
		}
	} else if IsKey(key, GlobalConfig.Keymap.Player.SeekForward) {
		speaker.Lock()
		newPos := player.streamer.Position() + player.sampleRate.N(time.Second*5)
		if newPos >= player.streamer.Len() {
			newPos = player.streamer.Len() - 1
		}
		if err := player.streamer.Seek(newPos); err != nil {
			// ignore seek errors
		}
		speaker.Unlock()
		if mprisServer != nil {
			mprisServer.UpdatePosition(p.currentPositionInMicroseconds())
		}
	} else if IsKey(key, GlobalConfig.Keymap.Player.VolumeDown) {
		p.volumeDisplayTimer = 10
		speaker.Lock()
		p.app.linearVolume = max(p.app.linearVolume-0.05, 0.0)
		p.app.volume = math.Log2(p.app.linearVolume)
		if p.app.linearVolume == 0 {
			p.app.volume = -10
		}
		player.volume.Volume = p.app.volume
		speaker.Unlock()
		p.app.SaveSettings()
		if mprisServer != nil {
			volume, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Volume")
			mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{"Volume": volume.Value()})
		}
	} else if IsKey(key, GlobalConfig.Keymap.Player.VolumeUp) {
		p.volumeDisplayTimer = 10
		speaker.Lock()
		p.app.linearVolume = min(p.app.linearVolume+0.05, 1.0)
		p.app.volume = math.Log2(p.app.linearVolume)
		player.volume.Volume = p.app.volume
		speaker.Unlock()
		p.app.SaveSettings()
		if mprisServer != nil {
			volume, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Volume")
			mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{"Volume": volume.Value()})
		}
	} else if IsKey(key, GlobalConfig.Keymap.Player.RateDown) {
		p.rateDisplayTimer = 10
		speaker.Lock()
		ratio := player.resampler.Ratio() - 0.05
		player.resampler.SetRatio(min(max(ratio, 0.1), 4.0))
		p.app.playbackRate = player.resampler.Ratio()
		speaker.Unlock()
		p.app.SaveSettings()
		if mprisServer != nil {
			rate, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Rate")
			mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{"Rate": rate.Value()})
		}
	} else if IsKey(key, GlobalConfig.Keymap.Player.RateUp) {
		p.rateDisplayTimer = 10
		speaker.Lock()
		ratio := player.resampler.Ratio() + 0.05
		player.resampler.SetRatio(min(max(ratio, 0.1), 4.0))
		p.app.playbackRate = player.resampler.Ratio()
		speaker.Unlock()
		p.app.SaveSettings()
		if mprisServer != nil {
			rate, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Rate")
			mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{"Rate": rate.Value()})
		}
	} else if IsKey(key, GlobalConfig.Keymap.Player.PrevSong) {
		p.playPreviousSong()
	} else if IsKey(key, GlobalConfig.Keymap.Player.NextSong) {
		p.playNextSong()
	} else if IsKey(key, GlobalConfig.Keymap.Player.TogglePlayMode) {
		p.app.playMode = (p.app.playMode + 1) % 3
	} else if IsKey(key, GlobalConfig.Keymap.Player.ToggleTextColor) {
		p.useCoverColor = !p.useCoverColor
	} else if IsKey(key, GlobalConfig.Keymap.Player.Reset) {
		p.volumeDisplayTimer = 10
		p.rateDisplayTimer = 10
		speaker.Lock()
		p.app.linearVolume = 1.0
		p.app.volume = 0
		player.volume.Volume = 0
		player.resampler.SetRatio(1.0)
		p.app.playbackRate = 1.0
		speaker.Unlock()
		p.app.SaveSettings()
		if mprisServer != nil {
			volume, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Volume")
			rate, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Rate")
			mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
				"Volume": volume.Value(),
				"Rate":   rate.Value(),
			})
		}
	} else {
		needsRedraw = false
	}

	if needsRedraw {
		p.updateStatus()
	}

	return nil, false, nil
}

// HandleSignal handles system signals, like window resizing.
//
// HandleSignal 处理系统信号，例如窗口大小调整。
func (p *PlayerPage) HandleSignal(sig os.Signal) error {
	if sig == syscall.SIGWINCH {
		p.View()
	}
	return nil
}

// View renders the player UI to the screen.
//
// View 将播放器UI渲染到屏幕上。
func (p *PlayerPage) View() {
	if p.flacPath == "" {
		p.displayEmptyState()
		return
	}
	p.imageTop, p.imageHeight, p.imageRightEdge, p.coverColorR, p.coverColorG, p.coverColorB = p.displayAlbumArt()
	p.updateStatus()
}

// displayEmptyState displays the empty state when the playlist is empty.
//
// displayEmptyState 在播放列表为空时显示空状态。
func (p *PlayerPage) displayEmptyState() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H")

	title := "Player"
	titleX := (w - len(title)) / 2
	fmt.Printf("\x1b[1;%dH\x1b[1m%s\x1b[0m", titleX, title)

	msg := "PlayList is empty"
	msg2 := "Add songs from the Library tab"
	msgX := (w - len(msg)) / 2
	msg2X := (w - len(msg2)) / 2
	centerRow := h / 2

	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", centerRow-1, msgX, msg)
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", centerRow+1, msg2X, msg2)

	footer := ""
	footerX := (w - len(footer)) / 2
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", h, footerX, footer)
}

// Tick is called periodically by the main loop to update dynamic elements like timers and progress bars.
//
// Tick 由主循环定期调用，以更新计时器和进度条等动态元素。
func (p *PlayerPage) Tick() {
	if p.volumeDisplayTimer > 0 {
		p.volumeDisplayTimer--
	}
	if p.rateDisplayTimer > 0 {
		p.rateDisplayTimer--
	}
	if p.resampleDisplayTimer > 0 {
		p.resampleDisplayTimer--
	}

	if p.flacPath == "" {
		return
	}

	p.updateStatus()
	p.checkSongEndAndHandleNext()

	if p.app.mprisServer != nil {
		p.app.mprisServer.UpdatePosition(p.currentPositionInMicroseconds())
	}
}

// currentPositionInMicroseconds is a helper to get the player position for MPRIS.
//
// currentPositionInMicroseconds 是一个辅助函数，用于为MPRIS获取播放器位置。
func (p *PlayerPage) currentPositionInMicroseconds() int64 {
	if p.app.player == nil {
		return 0
	}
	pos := p.app.player.streamer.Position()
	return int64(float64(pos) / float64(p.app.player.sampleRate) * 1e6)
}

// checkSongEndAndHandleNext checks if the song has ended and handles the next song according to the play mode.
//
// checkSongEndAndHandleNext 检查歌曲是否结束，并根据播放模式处理下一首。
func (p *PlayerPage) checkSongEndAndHandleNext() {
	if p.app.player == nil || len(p.app.Playlist) == 0 {
		return
	}

	currentPos := p.app.player.streamer.Position()
	totalLen := p.app.player.streamer.Len()

	if totalLen > 0 && currentPos >= totalLen-p.app.player.sampleRate.N(time.Second) {
		if p.app.playMode == 0 {
			return
		}

		if p.app.playMode == 1 || p.app.playMode == 2 {
			p.playNextSong()
		}
	}
}

// playNextSong plays the next song based on the current play mode, with debouncing.
//
// playNextSong 根据当前播放模式播放下一首歌曲（带防抖）。
func (p *PlayerPage) playNextSong() {
	if time.Since(p.lastSwitchTime) < time.Duration(GlobalConfig.App.SwitchDebounceMs)*time.Millisecond {
		return
	}

	if len(p.app.Playlist) == 0 {
		return
	}

	currentIndex := -1
	for i, song := range p.app.Playlist {
		if song == p.flacPath {
			currentIndex = i
			break
		}
	}

	if currentIndex == -1 {
		return
	}

	var nextIndex int

	switch p.app.playMode {
	case 1: // List loop / 列表循环
		nextIndex = (currentIndex + 1) % len(p.app.Playlist)
	case 2: // Random / 随机播放
		if p.app.isNavigatingHistory && p.app.historyIndex < len(p.app.playHistory)-1 {
			p.playNextInRandomMode()
			return
		} else {
			nextIndex = rand.Intn(len(p.app.Playlist))
			if nextIndex == currentIndex && len(p.app.Playlist) > 1 {
				nextIndex = (nextIndex + 1) % len(p.app.Playlist)
			}
		}
	default: // Single repeat or manual switch / 单曲循环或手动切换
		nextIndex = (currentIndex + 1) % len(p.app.Playlist)
	}

	p.tryPlayNextSong(currentIndex, nextIndex)

	p.lastSwitchTime = time.Now()
}

// tryPlayNextSong attempts to play the next song, skipping it if the file is corrupted.
//
// tryPlayNextSong 尝试播放下一首歌曲，如果文件损坏则跳过。
func (p *PlayerPage) tryPlayNextSong(currentIndex, nextIndex int) {
	triedIndices := make(map[int]bool)

	for {
		if triedIndices[nextIndex] {
			if p.app.player != nil {
				speaker.Lock()
				p.app.player.ctrl.Paused = true
				speaker.Unlock()
			}
			p.app.currentSongPath = ""
			if playerPage, ok := p.app.pages[0].(*PlayerPage); ok {
				playerPage.UpdateSong("")
			}
			return
		}

		triedIndices[nextIndex] = true
		nextSong := p.app.Playlist[nextIndex]

		err := p.app.PlaySongWithSwitchAndRender(nextSong, true, true)
		if err == nil {
			return
		}
		p.app.MarkFileAsCorrupted(nextSong)

		nextIndex = (nextIndex + 1) % len(p.app.Playlist)

		if nextIndex == currentIndex {
			if p.app.player != nil {
				speaker.Lock()
				p.app.player.ctrl.Paused = true
				speaker.Unlock()
			}
			p.app.currentSongPath = ""
			if playerPage, ok := p.app.pages[0].(*PlayerPage); ok {
				playerPage.UpdateSong("")
			}
			return
		}
	}
}

// playPreviousSong plays the previous song, with debouncing.
//
// playPreviousSong 播放上一首歌曲（带防抖）。
func (p *PlayerPage) playPreviousSong() {
	if time.Since(p.lastSwitchTime) < time.Duration(GlobalConfig.App.SwitchDebounceMs)*time.Millisecond {
		return
	}

	if len(p.app.Playlist) == 0 {
		return
	}

	if p.app.playMode == 2 {
		p.playPreviousInRandomMode()
		return
	}

	currentIndex := -1
	for i, song := range p.app.Playlist {
		if song == p.flacPath {
			currentIndex = i
			break
		}
	}

	if currentIndex == -1 {
		return
	}

	var prevIndex int

	if currentIndex == 0 {
		prevIndex = len(p.app.Playlist) - 1
	} else {
		prevIndex = currentIndex - 1
	}

	p.tryPlayPreviousSong(currentIndex, prevIndex)
	p.lastSwitchTime = time.Now()
}

// tryPlayPreviousSong attempts to play the previous song, skipping it if the file is corrupted.
//
// tryPlayPreviousSong 尝试播放上一首歌曲，如果文件损坏则跳过。
func (p *PlayerPage) tryPlayPreviousSong(currentIndex, prevIndex int) {
	triedIndices := make(map[int]bool)

	for {
		if triedIndices[prevIndex] {
			if p.app.player != nil {
				speaker.Lock()
				p.app.player.ctrl.Paused = true
				speaker.Unlock()
			}
			p.app.currentSongPath = ""
			if playerPage, ok := p.app.pages[0].(*PlayerPage); ok {
				playerPage.UpdateSong("")
			}
			return
		}

		triedIndices[prevIndex] = true
		prevSong := p.app.Playlist[prevIndex]

		err := p.app.PlaySongWithSwitchAndRender(prevSong, true, true)
		if err == nil {
			return
		}
		p.app.MarkFileAsCorrupted(prevSong)

		if prevIndex == 0 {
			prevIndex = len(p.app.Playlist) - 1
		} else {
			prevIndex = prevIndex - 1
		}

		if prevIndex == currentIndex {
			if p.app.player != nil {
				speaker.Lock()
				p.app.player.ctrl.Paused = true
				speaker.Unlock()
			}
			p.app.currentSongPath = ""
			if playerPage, ok := p.app.pages[0].(*PlayerPage); ok {
				playerPage.UpdateSong("")
			}
			return
		}
	}
}

// playPreviousInRandomMode handles the logic for "previous" in random mode by using play history.
//
// playPreviousInRandomMode 通过使用播放历史来处理随机模式下的“上一首”逻辑。
func (p *PlayerPage) playPreviousInRandomMode() {
	if p.app.historyIndex <= 0 {
		p.playRandomSong()
		return
	}

	p.app.isNavigatingHistory = true
	p.app.historyIndex--
	prevSong := p.app.playHistory[p.app.historyIndex]

	if p.isSongInPlaylist(prevSong) {
		p.playSongFromHistory(prevSong, true)
	} else {
		p.playPreviousInRandomMode()
	}

	p.lastSwitchTime = time.Now()
}

// playRandomSong plays a random song from the playlist.
//
// playRandomSong 从播放列表中随机播放一首歌曲。
func (p *PlayerPage) playRandomSong() {
	if len(p.app.Playlist) == 0 {
		return
	}

	currentIndex := -1
	for i, song := range p.app.Playlist {
		if song == p.flacPath {
			currentIndex = i
			break
		}
	}

	var randomIndex int
	if len(p.app.Playlist) > 1 {
		for {
			randomIndex = rand.Intn(len(p.app.Playlist))
			if randomIndex != currentIndex {
				break
			}
		}
	} else {
		randomIndex = 0
	}

	p.app.PlaySongWithSwitchAndRender(p.app.Playlist[randomIndex], true, true)

	p.lastSwitchTime = time.Now()
}

// isSongInPlaylist checks if a song is in the current playlist.
//
// isSongInPlaylist 检查歌曲是否在当前播放列表中。
func (p *PlayerPage) isSongInPlaylist(songPath string) bool {
	for _, song := range p.app.Playlist {
		if song == songPath {
			return true
		}
	}
	return false
}

// playSongFromHistory plays a song from history without adding a new history entry.
//
// playSongFromHistory 从历史记录中播放歌曲，而不添加新的历史记录条目。
func (p *PlayerPage) playSongFromHistory(songPath string, switchToPlayer bool) error {
	if p.app.currentSongPath == songPath && p.app.player != nil {
		if switchToPlayer {
			p.app.switchToPage(0)
		}
		return nil
	}

	speaker.Lock()
	if p.app.player != nil {
		p.app.player.ctrl.Paused = true
	}
	speaker.Unlock()

	f, err := os.Open(songPath)
	if err != nil {
		return fmt.Errorf("打开文件失败: %v", err)
	}

	streamer, format, err := flac.Decode(f)
	if err != nil {
		f.Close()
		p.app.MarkFileAsCorrupted(songPath)
		return fmt.Errorf("解码FLAC失败: %v", err)
	}

	var audioStream beep.StreamSeeker = streamer
	if format.SampleRate != p.app.sampleRate {
		p.resampleDisplayTimer = 10
		// Only update status if we're on the player page and not during initial startup
		// 只有在播放页面且不是初始启动时才更新状态
		if p.app.currentPageIndex == 0 && p.flacPath != "" {
			p.updateStatus()
		}

		// Use high-quality resampling with go-audio-resampler (最高质量)
		resampledStream, err := highQualityResample(streamer, format.SampleRate, p.app.sampleRate)
		if err != nil {
			f.Close()
			return fmt.Errorf("高质量重采样失败: %v", err)
		}
		audioStream = resampledStream
	}

	speaker.Init(p.app.sampleRate, p.app.sampleRate.N(time.Second/30))

	player, err := newAudioPlayer(audioStream, format, p.app.volume, p.app.playbackRate)
	if err != nil {
		f.Close()
		return fmt.Errorf("创建播放器失败: %v", err)
	}

	if p.app.mprisServer != nil {
		p.app.mprisServer.StopService()
	}
	mprisServer, err := NewMPRISServer(p.app, player, songPath)
	if err == nil {
		if err := mprisServer.Start(); err == nil {
			mprisServer.StartUpdateLoop()
			mprisServer.UpdatePlaybackStatus(true)
			mprisServer.UpdateMetadata()
		}
	}

	speaker.Lock()
	p.app.player = player
	p.app.mprisServer = mprisServer
	p.app.currentSongPath = songPath
	speaker.Unlock()

	// Save the current song when playing from history
	if err := SaveCurrentSong(songPath, p.app.LibraryPath); err != nil {
		log.Printf("Warning: failed to save current song from history: %v\n\n警告: 从历史记录保存当前歌曲失败: %v", err, err)
	}

	speaker.Play(p.app.player.volume)

	p.resampleDisplayTimer = 0

	// Reset cover image position and dimensions
	// 重置封面图片位置和尺寸
	p.imageTop = 0
	p.imageHeight = 0
	p.imageRightEdge = 0

	if switchToPlayer {
		// Only update the player page UI when switching to the player page
		// 只有在切换到播放器页面时才更新播放器页面UI
		p.UpdateSongWithRender(songPath)
		fmt.Print("\x1b[2J\x1b[3J\x1b[H")
		p.app.currentPageIndex = 0
		p.Init()
		p.View()
	} else {
		// When not switching to player page, just update the song path without rendering
		// 当不切换到播放器页面时，只更新歌曲路径而不渲染
		p.UpdateSong(songPath)
	}

	return nil
}

// playNextInRandomMode handles the logic for "next" in random mode by using play history.
//
// playNextInRandomMode 通过使用播放历史来处理随机模式下的“下一首”逻辑。
func (p *PlayerPage) playNextInRandomMode() {
	if p.app.historyIndex >= len(p.app.playHistory)-1 {
		p.playRandomSong()
		p.app.isNavigatingHistory = false
		return
	}

	p.app.isNavigatingHistory = true
	p.app.historyIndex++
	nextSong := p.app.playHistory[p.app.historyIndex]

	if p.isSongInPlaylist(nextSong) {
		p.playSongFromHistory(nextSong, true)
	} else {
		p.playNextInRandomMode()
	}

	p.lastSwitchTime = time.Now()
}

// --- Audio Player (now just a data structure, no logic) ---

type audioPlayer struct {
	sampleRate beep.SampleRate
	streamer   beep.StreamSeeker
	ctrl       *beep.Ctrl
	resampler  *beep.Resampler
	volume     *effects.Volume
	position   int
	initialVol float64
}

func newAudioPlayer(streamer beep.StreamSeeker, format beep.Format, volumeLevel float64, playbackRate float64) (*audioPlayer, error) {
	loopStreamer := beep.Loop(-1, streamer)
	ctrl := &beep.Ctrl{Streamer: loopStreamer}
	resampler := beep.ResampleRatio(4, 1, ctrl)
	volume := &effects.Volume{Streamer: resampler, Base: 2}
	volume.Volume = volumeLevel
	resampler.SetRatio(playbackRate)
	return &audioPlayer{format.SampleRate, streamer, ctrl, resampler, volume, 0, 0}, nil
}

// --- TUI / Drawing ---
// All drawing functions are now methods on PlayerPage to access state.

func (p *PlayerPage) displayAlbumArt() (imageTop, imageHeight, imageRightEdge, coverColorR, coverColorG, coverColorB int) {
	// This function is very large. It remains mostly the same, but now it's a method.
	// We access flacPath and cell sizes via `p`.
	time.Sleep(50 * time.Millisecond) // This might be removed later for performance
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		fmt.Print("\x1b[2J\x1b[H")
		fmt.Println("无法获取终端尺寸")
		return 0, 0, 0, 255, 255, 255
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H") // Clear screen

	var finalImgH int
	var imageWidthInChars, imageHeightInChars int
	var startCol, startRow int
	var coverImg image.Image

	showNothing := w < 23 || h < 5
	showTextOnly := h < 13
	isWideTerminal := w >= 100 && (float64(w)/float64(h) > 2.0 || h < 20) && !showNothing && !showTextOnly

	f, err := os.Open(p.flacPath)
	if err == nil {
		defer f.Close()
		m, err := tag.ReadFrom(f)
		if err == nil {
			if pic := m.Picture(); pic != nil {
				if img, _, err := image.Decode(bytes.NewReader(pic.Data)); err == nil {
					coverImg = img
					var pixelW, pixelH int
					if showNothing || showTextOnly {
						pixelW, pixelH = 0, 0
					} else if isWideTerminal {
						pixelW = (w - 30) * p.cellW
						pixelH = (h - 1) * p.cellH
					} else {
						pixelW = w * p.cellW
						pixelH = (h - 2) * p.cellH
					}

					if pixelW < 10 {
						pixelW = 10
					}
					if pixelH < 10 {
						pixelH = 10
					}

					normalizedImg := resize.Resize(960, 960, img, resize.Lanczos3)
					scaledImg := resize.Thumbnail(uint(pixelW), uint(pixelH), normalizedImg, resize.Lanczos3)
					finalImgW, finalImgH := scaledImg.Bounds().Dx(), scaledImg.Bounds().Dy()

					if p.cellW == 0 {
						p.cellW = 1
					}
					if p.cellH == 0 {
						p.cellH = 1
					}

					imageWidthInChars = (finalImgW + p.cellW - 1) / p.cellW
					imageHeightInChars = (finalImgH + p.cellH - 1) / p.cellH

					if imageWidthInChars > w {
						imageWidthInChars = w
					}
					if imageHeightInChars > h {
						imageHeightInChars = h
					}

					title, artist, album := getSongMetadata(p.flacPath)
					maxTextLength := max(max(len(title), len(artist)), len(album))

					showNothing = w < 23 || h < 5
					showTextOnly = h < 13 && !showNothing
					showInfoOnly := (w < maxTextLength || h < 10) && !showNothing && !showTextOnly

					p.textTooLongForWide = false
					p.showTextInWideMode = false
					if isWideTerminal {
						availableWidth := w - (startCol + imageWidthInChars)
						if availableWidth < maxTextLength+10 {
							p.textTooLongForWide = true
							// Check if we can show text below image in wide mode
							// 检查是否可以在宽终端模式下在图片下方显示文本
							if h-imageHeightInChars >= 5 { // Need at least 5 rows for text / 至少需要5行来显示文本
								p.showTextInWideMode = true
							}
						}
					}

					if showNothing || showTextOnly {
						startCol, startRow, imageWidthInChars, imageHeightInChars = 0, 0, 0, 0
					} else if showInfoOnly {
						startCol, startRow = (w-imageWidthInChars)/2, (h-imageHeightInChars)/2
					} else if isWideTerminal && !p.textTooLongForWide {
						// Normal wide terminal mode - image on left, text on right
						startCol, startRow = 1, (h-imageHeightInChars+1)/2
					} else if isWideTerminal && p.textTooLongForWide && p.showTextInWideMode {
						// Text too long for right panel, but can show below image - use narrow terminal layout
						startCol, startRow = (w-imageWidthInChars)/2, 2
					} else if isWideTerminal && p.textTooLongForWide && !p.showTextInWideMode {
						// Text too long and cannot show below image - center the image only
						startCol, startRow = (w-imageWidthInChars)/2, (h-imageHeightInChars+1)/2
					} else {
						// Normal narrow terminal mode
						startCol, startRow = (w-imageWidthInChars)/2, 2
					}

					if startCol < 1 {
						startCol = 1
					}
					if startRow < 1 {
						startRow = 1
					}
					if startCol+imageWidthInChars > w {
						imageWidthInChars = w - startCol
					}
					if startRow+imageHeightInChars > h {
						imageHeightInChars = h - startRow
					}

					if !showNothing && !showTextOnly {
						fmt.Printf("\x1b[%d;%dH", startRow, startCol)
						_ = NewEncoder(os.Stdout).Encode(scaledImg)
						if imageWidthInChars > 0 && startCol+imageWidthInChars <= w {
							fillStartCol := startCol + imageWidthInChars
							for row := startRow; row < startRow+imageHeightInChars; row++ {
								fmt.Printf("\x1b[%d;%dH\x1b[K", row, fillStartCol)
							}
						}
					}
				}
			}
		}
	}

	if coverImg != nil {
		r, g, b := analyzeCoverColor(coverImg)
		coverColorR, coverColorG, coverColorB = r, g, b
	} else {
		coverColorR, coverColorG, coverColorB = 255, 255, 255
	}

	imageRightEdgeVal := 0
	if isWideTerminal {
		imageRightEdgeVal = startCol + imageWidthInChars
	}
	if imageHeightInChars == 0 && finalImgH > 0 && p.cellH > 0 {
		imageHeightInChars = 1
	}

	return startRow, imageHeightInChars, imageRightEdgeVal, coverColorR, coverColorG, coverColorB
}

func (p *PlayerPage) updateStatus() {
	if p.app.currentPageIndex != 0 || p.flacPath == "" {
		return
	}

	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return
	}

	title, artist, album := getSongMetadata(p.flacPath)
	maxTextLength := max(max(len(title), len(artist)), len(album))

	showNothing := w < 23 || h < 5
	if showNothing {
		return
	}

	showTextOnly := h < 13 && !showNothing
	if showTextOnly {
		p.updateTextOnlyMode(w, h)
		return
	}

	showInfoOnly := (w < maxTextLength || h < 10) && !showNothing && !showTextOnly
	if showInfoOnly {
		return
	}

	isWideTerminal := w >= 100 && (float64(w)/float64(h) > 2.0 || h < 20)

	if isWideTerminal {
		availableWidth := w - p.imageRightEdge
		if availableWidth < maxTextLength+10 {
			isWideTerminal = false
		}
	}

	if isWideTerminal && !p.textTooLongForWide {
		// Normal wide terminal mode - show image on left, text on right
		if p.imageRightEdge > 0 && w-p.imageRightEdge >= 30 {
			p.updateRightPanel(w)
		}
	} else if isWideTerminal && p.textTooLongForWide && p.showTextInWideMode {
		// Text too long for right panel, but can show below image - use narrow terminal layout
		imageBottomRow := p.imageTop + p.imageHeight
		if h-imageBottomRow >= 5 {
			p.updateBottomStatus(imageBottomRow, w, h)
		}
	} else if isWideTerminal && p.textTooLongForWide && !p.showTextInWideMode {
		// Text too long and cannot show below image - don't show text, just show centered image
		// No need to call updateRightPanel or updateBottomStatus
	} else {
		// Normal narrow terminal mode
		imageBottomRow := p.imageTop + p.imageHeight
		if h-imageBottomRow >= 5 {
			p.updateBottomStatus(imageBottomRow, w, h)
		}
	}
}

func (p *PlayerPage) updateRightPanel(w int) {
	if p.imageHeight < 5 {
		return
	}

	title, artist, album := getSongMetadata(p.flacPath)

	// Use runewidth for accurate string width calculation
	titleWidth := runewidth.StringWidth(title)
	artistWidth := runewidth.StringWidth(artist)
	albumWidth := runewidth.StringWidth(album)

	availableWidth := w - p.imageRightEdge
	centerCol := p.imageRightEdge + availableWidth/2

	partHeight := p.imageHeight / 3
	artistRow := p.imageTop + partHeight + partHeight/2
	titleRow := artistRow - 1
	albumRow := artistRow + 1
	progressRow := p.imageTop + (2 * partHeight) + partHeight/2

	if progressRow-albumRow < 1 {
		return
	}
	if titleRow < p.imageTop {
		titleRow, artistRow, albumRow = p.imageTop, p.imageTop+1, p.imageTop+2
	}
	if progressRow >= p.imageTop+p.imageHeight {
		progressRow = p.imageTop + p.imageHeight - 1
	}

	colorCode := p.getColorCode()
	// Use individual centering for each line (same as narrow terminal mode)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s\x1b[1m%s\x1b[0m", titleRow, centerCol-titleWidth/2, colorCode, title)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", artistRow, centerCol-artistWidth/2, colorCode, artist)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", albumRow, centerCol-albumWidth/2, colorCode, album)

	progressBarStartCol := p.imageRightEdge + 5
	progressBarWidth := w - progressBarStartCol - 2 // Reduced by 1 character
	if progressBarWidth < 10 {
		return
	}

	p.drawProgressBar(progressRow, progressBarStartCol, progressBarWidth, colorCode)
}

func (p *PlayerPage) updateBottomStatus(startRow, w, h int) {
	title, artist, album := getSongMetadata(p.flacPath)
	availableRows := h - startRow
	infoRow := startRow + availableRows/3
	progressRow := startRow + 2*availableRows/3 + (h-(startRow+2*availableRows/3))/2
	centerCol := w / 2

	colorCode := p.getColorCode()
	// Use runewidth for accurate string width calculation
	titleWidth := runewidth.StringWidth(title)
	artistWidth := runewidth.StringWidth(artist)
	albumWidth := runewidth.StringWidth(album)

	fmt.Printf("\x1b[%d;%dH\x1b[K%s\x1b[1m%s\x1b[0m", infoRow, centerCol-titleWidth/2, colorCode, title)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", infoRow+1, centerCol-artistWidth/2, colorCode, artist)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", infoRow+2, centerCol-albumWidth/2, colorCode, album)

	progressBarStartCol := 5
	progressBarWidth := w - 10
	if progressBarWidth < 10 {
		progressBarWidth = 10
	}

	p.drawProgressBar(progressRow, progressBarStartCol, progressBarWidth, colorCode)
}

func (p *PlayerPage) updateTextOnlyMode(w, h int) {
	title, artist, album := getSongMetadata(p.flacPath)
	centerRow, centerCol := h/2, w/2

	colorCode := p.getColorCode()
	// Use runewidth for accurate string width calculation
	titleWidth := runewidth.StringWidth(title)
	artistWidth := runewidth.StringWidth(artist)
	albumWidth := runewidth.StringWidth(album)

	fmt.Printf("\x1b[%d;%dH\x1b[K%s\x1b[1m%s\x1b[0m", centerRow-1, centerCol-titleWidth/2, colorCode, title)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", centerRow, centerCol-artistWidth/2, colorCode, artist)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", centerRow+1, centerCol-albumWidth/2, colorCode, album)

	progressBarStartCol := 5
	progressBarWidth := w - 10
	if progressBarWidth < 10 {
		progressBarWidth = 10
	}
	progressRow := centerRow + 3

	p.drawProgressBar(progressRow, progressBarStartCol, progressBarWidth, colorCode)
}

func (p *PlayerPage) drawProgressBar(row, startCol, width int, colorCode string) {
	// 检查player是否可用
	if p.app.player == nil {
		return
	}

	// --- Indicators (Volume & Rate & Resample) ---
	indicatorRow := row - 1
	// Ensure we don't draw at or above the first row, and there's a progress bar to align with.
	if indicatorRow > 0 && width > 0 {
		// Only clear the indicator area, not the whole line
		fmt.Printf("\x1b[%d;%dH\x1b[K", indicatorRow, startCol)

		// Draw Resample Indicator (居中显示，优先级最高)
		if p.resampleDisplayTimer > 0 {
			resampleStr := "↻ Resampling"
			resampleWidth := runewidth.StringWidth(resampleStr)
			resampleStartCol := startCol + (width-resampleWidth)/2
			if resampleStartCol < startCol {
				resampleStartCol = startCol
			}
			fmt.Printf("\x1b[%d;%dH%s%s\x1b[0m", indicatorRow, resampleStartCol, colorCode, resampleStr)
		} else {
			// 如果没有重采样提示，显示音量和播放速度
			// Draw Volume Indicator
			if p.volumeDisplayTimer > 0 {
				// With the new linear volume, we can just multiply by 100
				volPercent := int(math.Round(p.app.linearVolume * 100))
				volStr := fmt.Sprintf("%d%%", volPercent)
				fmt.Printf("\x1b[%d;%dH%s%s\x1b[0m", indicatorRow, startCol, colorCode, volStr)
			}

			// Draw Rate Indicator
			if p.rateDisplayTimer > 0 {
				rateVal := p.app.player.resampler.Ratio()
				rateStr := fmt.Sprintf("%.2fx", rateVal)
				// Align the end of the string with the end of the progress bar
				rateWidth := runewidth.StringWidth(rateStr)
				rateStartCol := startCol + width - rateWidth
				if rateStartCol < startCol { // Ensure it doesn't overlap with volume
					rateStartCol = startCol + 7 // A safe offset
				}
				fmt.Printf("\x1b[%d;%dH%s%s\x1b[0m", indicatorRow, rateStartCol, colorCode, rateStr)
			}
		}
	}

	currentPos := p.app.player.streamer.Position()
	totalLen := p.app.player.streamer.Len()
	progress := 0.0
	if totalLen > 0 {
		progress = float64(currentPos) / float64(totalLen)
	}

	playedChars := int(float64(width) * progress)

	// 根据播放状态和播放模式显示图标
	icon := "⏸"
	if p.app.player.ctrl.Paused {
		icon = "▶"
	}

	// 播放模式图标
	modeIcon := "⟳" // 默认单曲循环
	switch p.app.playMode {
	case 1:
		modeIcon = "⇆" // 列表循环
	case 2:
		modeIcon = "⤮" // 随机播放
	}

	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s", row, startCol-2, colorCode, icon)

	var bar string
	if playedChars > 0 {
		bar += fmt.Sprintf("\x1b[2m%s", colorCode) // Dim played part
		for range playedChars {
			bar += "━"
		}
		bar += "\x1b[0m"
	}
	bar += colorCode // Unplayed part
	for i := playedChars; i < width; i++ {
		bar += "━"
	}

	fmt.Printf("\x1b[0m\x1b[%d;%dH%s", row, startCol, bar)
	fmt.Printf("\x1b[0m\x1b[%d;%dH\x1b[K%s%s\x1b[0m", row, startCol+width+1, colorCode, modeIcon)
}

func (p *PlayerPage) getColorCode() string {
	if p.useCoverColor {
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", p.coverColorR, p.coverColorG, p.coverColorB)
	}
	return "\x1b[37m" // White
}

// --- Misc Helper Functions ---

func getCellSize() (width, height int, err error) {
	// This function remains unchanged.
	fmt.Print("\x1b[16t")
	var buf []byte
	var b [1]byte
	for {
		n, err := os.Stdin.Read(b[:])
		if err != nil {
			return 0, 0, err
		}
		if n == 0 {
			continue
		}
		buf = append(buf, b[0])
		if b[0] == 't' {
			break
		}
	}
	if !(len(buf) > 2 && buf[0] == '\x1b' && buf[1] == '[' && buf[len(buf)-1] == 't') {
		return 0, 0, fmt.Errorf("无法解析的终端响应格式: %q", buf)
	}
	content := buf[2 : len(buf)-1]
	parts := bytes.Split(content, []byte(";"))
	if len(parts) != 3 {
		return 0, 0, fmt.Errorf("预期的响应分段为3, 实际为 %d: %q", len(parts), buf)
	}
	if string(parts[0]) != "6" {
		return 0, 0, fmt.Errorf("预期的响应代码为 6, 实际为 %s", parts[0])
	}
	h, err := strconv.Atoi(string(parts[1]))
	if err != nil {
		return 0, 0, err
	}
	w, err := strconv.Atoi(string(parts[2]))
	if err != nil {
		return 0, 0, err
	}
	return w, h, nil
}

func getSongMetadata(flacPath string) (title, artist, album string) {
	f, err := os.Open(flacPath)
	if err != nil {
		return "...", "...", "..."
	}
	defer f.Close()
	m, err := tag.ReadFrom(f)
	if err != nil {
		return "...", "...", "..."
	}
	title, artist, album = m.Title(), m.Artist(), m.Album()
	if title == "" {
		title = "..."
	}
	if artist == "" {
		artist = "..."
	}
	if album == "" {
		album = "..."
	}
	return title, artist, album
}

func analyzeCoverColor(img image.Image) (r, g, b int) {
	bounds := img.Bounds()
	colorCount := make(map[[3]int]int)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pr, pg, pb, _ := img.At(x, y).RGBA()
			r8, g8, b8 := int(pr>>8), int(pg>>8), int(pb>>8)
			brightness := 0.2126*float64(r8) + 0.7152*float64(g8) + 0.0722*float64(b8)
			isBright := brightness > 160
			isNotGray := math.Abs(float64(r8)-float64(g8)) > 25 || math.Abs(float64(g8)-float64(b8)) > 25
			isNotWhite := !(r8 > 220 && g8 > 220 && b8 > 220)
			if isBright && isNotGray && isNotWhite {
				color := [3]int{r8, g8, b8}
				colorCount[color]++
			}
		}
	}
	maxCount := 0
	var dominantColor [3]int
	for color, count := range colorCount {
		if count > maxCount {
			maxCount = count
			dominantColor = color
		}
	}

	if maxCount > 0 {
		return dominantColor[0], dominantColor[1], dominantColor[2]
	}

	return 255, 255, 255
}
