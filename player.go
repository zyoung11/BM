package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
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

// Generic min function
func min[T ~int | ~float64](a, b T) T {
	if a < b {
		return a
	}
	return b
}

// Generic max function
func max[T ~int | ~float64](a, b T) T {
	if a > b {
		return a
	}
	return b
}

// --- Page Implementation ---

// PlayerPage holds the state for the music player view.
type PlayerPage struct {
	app      *App
	flacPath string

	// UI state
	cellW, cellH                          int
	imageTop, imageHeight, imageRightEdge int
	coverColorR, coverColorG, coverColorB int
	useCoverColor                         bool
	volumeDisplayTimer                    int
	rateDisplayTimer                      int

	// 防抖机制：记录上次切歌时间
	lastSwitchTime time.Time
}

// NewPlayerPage creates a new instance of the player page.
func NewPlayerPage(app *App, flacPath string, cellW, cellH int) *PlayerPage {
	return &PlayerPage{
		app:            app,
		flacPath:       flacPath,
		useCoverColor:  true, // Default to using cover color
		cellW:          cellW,
		cellH:          cellH,
		lastSwitchTime: time.Now().Add(-2 * time.Second), // 初始化为2秒前，确保第一次切歌可用
	}
}

// Init for PlayerPage is now empty, as setup is done in the constructor.
func (p *PlayerPage) Init() {}

// UpdateSong 更新当前播放的歌曲路径
func (p *PlayerPage) UpdateSong(songPath string) {
	p.flacPath = songPath
	// 重置图像相关状态，强制重新加载专辑封面
	p.imageTop = 0
	p.imageHeight = 0
	p.imageRightEdge = 0
	// 不立即重新渲染，让Tick()方法在下一个周期自然更新
}

// HandleKey handles user key presses.
func (p *PlayerPage) HandleKey(key rune) (Page, bool, error) {
	player := p.app.player
	mprisServer := p.app.mprisServer
	needsRedraw := true // Most keys will need a status update

	if player == nil {
		// If there is no player, don't handle any keys
		return nil, false, nil
	}

	// Player-specific keybindings
	if IsKey(key, AppConfig.Keymap.Player.TogglePause) {
		speaker.Lock()
		player.ctrl.Paused = !player.ctrl.Paused
		speaker.Unlock()
		if mprisServer != nil {
			mprisServer.UpdatePlaybackStatus(!player.ctrl.Paused)
		}
	} else if IsKey(key, AppConfig.Keymap.Player.SeekBackward) {
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
	} else if IsKey(key, AppConfig.Keymap.Player.SeekForward) {
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
	} else if IsKey(key, AppConfig.Keymap.Player.VolumeDown) {
		p.volumeDisplayTimer = 10 // Show volume indicator
		speaker.Lock()
		p.app.linearVolume = max(p.app.linearVolume-0.05, 0.0)
		p.app.volume = math.Log2(p.app.linearVolume)
		if p.app.linearVolume == 0 {
			p.app.volume = -10
		}
		player.volume.Volume = p.app.volume
		speaker.Unlock()
		if mprisServer != nil {
			volume, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Volume")
			mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{"Volume": volume.Value()})
		}
	} else if IsKey(key, AppConfig.Keymap.Player.VolumeUp) {
		p.volumeDisplayTimer = 10 // Show volume indicator
		speaker.Lock()
		p.app.linearVolume = min(p.app.linearVolume+0.05, 1.0)
		p.app.volume = math.Log2(p.app.linearVolume)
		player.volume.Volume = p.app.volume
		speaker.Unlock()
		if mprisServer != nil {
			volume, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Volume")
			mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{"Volume": volume.Value()})
		}
	} else if IsKey(key, AppConfig.Keymap.Player.RateDown) {
		p.rateDisplayTimer = 10 // Show rate indicator
		speaker.Lock()
		ratio := player.resampler.Ratio() - 0.05
		player.resampler.SetRatio(min(max(ratio, 0.1), 4.0))
		p.app.playbackRate = player.resampler.Ratio()
		speaker.Unlock()
		if mprisServer != nil {
			rate, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Rate")
			mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{"Rate": rate.Value()})
		}
	} else if IsKey(key, AppConfig.Keymap.Player.RateUp) {
		p.rateDisplayTimer = 10 // Show rate indicator
		speaker.Lock()
		ratio := player.resampler.Ratio() + 0.05
		player.resampler.SetRatio(min(max(ratio, 0.1), 4.0))
		p.app.playbackRate = player.resampler.Ratio()
		speaker.Unlock()
		if mprisServer != nil {
			rate, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Rate")
			mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{"Rate": rate.Value()})
		}
	} else if IsKey(key, AppConfig.Keymap.Player.PrevSong) {
		p.playPreviousSong()
	} else if IsKey(key, AppConfig.Keymap.Player.NextSong) {
		p.playNextSong()
	} else if IsKey(key, AppConfig.Keymap.Player.TogglePlayMode) {
		p.app.playMode = (p.app.playMode + 1) % 3
	} else if IsKey(key, AppConfig.Keymap.Player.ToggleTextColor) {
		p.useCoverColor = !p.useCoverColor
	} else if IsKey(key, AppConfig.Keymap.Player.Reset) {
		p.volumeDisplayTimer = 10
		p.rateDisplayTimer = 10
		speaker.Lock()
		p.app.linearVolume = 1.0
		p.app.volume = 0
		player.volume.Volume = 0
		player.resampler.SetRatio(1.0)
		p.app.playbackRate = 1.0
		speaker.Unlock()
		if mprisServer != nil {
			volume, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Volume")
			rate, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Rate")
			mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
				"Volume": volume.Value(),
				"Rate":   rate.Value(),
			})
		}
	} else {
		needsRedraw = false // Unhandled keys don't need a redraw
	}

	if needsRedraw {
		// 只更新状态，不清屏重绘整个界面
		p.updateStatus()
	}

	return nil, false, nil // Stay on this page, no full redraw needed
}

// HandleSignal handles system signals, like window resizing.
func (p *PlayerPage) HandleSignal(sig os.Signal) error {
	if sig == syscall.SIGWINCH {
		// Window size changed, we need to do a full redraw.
		p.View()
	}
	return nil
}

// View renders the player UI to the screen.
func (p *PlayerPage) View() {
	// 如果没有歌曲路径，显示空状态
	if p.flacPath == "" {
		p.displayEmptyState()
		return
	}

	// displayAlbumArt clears the screen and draws the art.
	p.imageTop, p.imageHeight, p.imageRightEdge, p.coverColorR, p.coverColorG, p.coverColorB = p.displayAlbumArt()
	// updateStatus draws the text and progress bar over it.
	p.updateStatus()
}

// displayEmptyState 显示播放列表为空的状态
func (p *PlayerPage) displayEmptyState() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H") // Clear screen

	// 标题
	title := "Player"
	titleX := (w - len(title)) / 2
	fmt.Printf("\x1b[1;%dH\x1b[1m%s\x1b[0m", titleX, title)

	// 空状态消息
	msg := "PlayList is empty"
	msg2 := "Add songs from the Library tab"
	msgX := (w - len(msg)) / 2
	msg2X := (w - len(msg2)) / 2
	centerRow := h / 2

	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", centerRow-1, msgX, msg)
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", centerRow+1, msg2X, msg2)

	// 页面切换提示
	footer := ""
	footerX := (w - len(footer)) / 2
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", h, footerX, footer)
}

// Tick is called periodically by the main loop to update dynamic elements.
func (p *PlayerPage) Tick() {
	if p.volumeDisplayTimer > 0 {
		p.volumeDisplayTimer--
	}
	if p.rateDisplayTimer > 0 {
		p.rateDisplayTimer--
	}

	// 如果没有歌曲，不需要更新
	if p.flacPath == "" {
		return
	}

	// We only need to update the status (progress bar, etc.), not the whole album art.
	p.updateStatus()

	// 检查歌曲是否结束，并根据播放模式处理下一首
	p.checkSongEndAndHandleNext()

	// Also, notify the MPRIS server of the position change.
	if p.app.mprisServer != nil {
		p.app.mprisServer.UpdatePosition(p.currentPositionInMicroseconds())
	}
}

// currentPositionInMicroseconds is a helper to get the player position for MPRIS.
func (p *PlayerPage) currentPositionInMicroseconds() int64 {
	if p.app.player == nil {
		return 0
	}
	pos := p.app.player.streamer.Position()
	return int64(float64(pos) / float64(p.app.player.sampleRate) * 1e6)
}

// checkSongEndAndHandleNext 检查歌曲是否结束，并根据播放模式处理下一首
func (p *PlayerPage) checkSongEndAndHandleNext() {
	if p.app.player == nil || len(p.app.Playlist) == 0 {
		return
	}

	// 检查歌曲是否结束（位置接近末尾）
	currentPos := p.app.player.streamer.Position()
	totalLen := p.app.player.streamer.Len()

	// 如果歌曲接近结束（最后1秒），根据播放模式处理
	if totalLen > 0 && currentPos >= totalLen-p.app.player.sampleRate.N(time.Second) {
		// 单曲循环模式：自动循环当前歌曲，不需要额外处理
		if p.app.playMode == 0 {
			return
		}

		// 列表循环或随机播放模式：播放下一首
		if p.app.playMode == 1 || p.app.playMode == 2 {
			p.playNextSong()
		}
	}
}

// playNextSong 根据播放模式播放下一首歌曲（带防抖）
func (p *PlayerPage) playNextSong() {
	// 检查防抖期（1秒内只能切歌一次）
	if time.Since(p.lastSwitchTime) < time.Second {
		return // 在防抖期内，忽略切歌操作
	}

	if len(p.app.Playlist) == 0 {
		return
	}

	// 找到当前歌曲在播放列表中的位置
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

	// 根据播放模式决定下一首
	switch p.app.playMode {
	case 1: // 列表循环
		nextIndex = (currentIndex + 1) % len(p.app.Playlist)
	case 2: // 随机播放
		// 检查是否正在历史记录中导航
		if p.app.isNavigatingHistory && p.app.historyIndex < len(p.app.playHistory)-1 {
			// 在历史记录中向前切歌
			p.playNextInRandomMode()
			return
		} else {
			// 不在历史记录中，随机播放
			nextIndex = rand.Intn(len(p.app.Playlist))
			// 避免随机到同一首歌
			if nextIndex == currentIndex && len(p.app.Playlist) > 1 {
				nextIndex = (nextIndex + 1) % len(p.app.Playlist)
			}
		}
	default: // 单曲循环或手动切换
		nextIndex = (currentIndex + 1) % len(p.app.Playlist)
	}

	// 尝试播放下一首歌曲，如果失败则继续尝试下一首
	p.tryPlayNextSong(currentIndex, nextIndex)

	// 更新切歌时间
	p.lastSwitchTime = time.Now()
}

// tryPlayNextSong 尝试播放下一首歌曲，如果文件损坏则跳过
func (p *PlayerPage) tryPlayNextSong(currentIndex, nextIndex int) {
	// 记录已尝试的索引，避免无限循环
	triedIndices := make(map[int]bool)

	// 从nextIndex开始尝试播放
	for {
		// 如果已经尝试过这个索引，说明所有歌曲都损坏了
		if triedIndices[nextIndex] {
			// 所有歌曲都损坏，停止播放
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

		// 尝试播放歌曲
		err := p.app.PlaySongWithSwitch(nextSong, false)
		if err == nil {
			// 播放成功
			return
		}
		// 播放失败，标记文件为损坏
		p.app.MarkFileAsCorrupted(nextSong)

		// 播放失败，尝试下一首
		nextIndex = (nextIndex + 1) % len(p.app.Playlist)

		// 如果回到当前歌曲，说明已经尝试了所有歌曲
		if nextIndex == currentIndex {
			// 所有歌曲都损坏，停止播放
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

// playPreviousSong 播放上一首歌曲（带防抖）
func (p *PlayerPage) playPreviousSong() {
	// 检查防抖期（1秒内只能切歌一次）
	if time.Since(p.lastSwitchTime) < time.Second {
		return // 在防抖期内，忽略切歌操作
	}

	if len(p.app.Playlist) == 0 {
		return
	}

	// 随机播放模式下使用历史记录
	if p.app.playMode == 2 {
		p.playPreviousInRandomMode()
		return
	}

	// 找到当前歌曲在播放列表中的位置
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

	// 计算上一首索引
	if currentIndex == 0 {
		// 如果是第一首，循环到最后一首
		prevIndex = len(p.app.Playlist) - 1
	} else {
		prevIndex = currentIndex - 1
	}

	// 尝试播放上一首歌曲，如果失败则继续尝试上一首
	p.tryPlayPreviousSong(currentIndex, prevIndex)

	// 更新切歌时间
	p.lastSwitchTime = time.Now()
}

// tryPlayPreviousSong 尝试播放上一首歌曲，如果文件损坏则跳过
func (p *PlayerPage) tryPlayPreviousSong(currentIndex, prevIndex int) {
	// 记录已尝试的索引，避免无限循环
	triedIndices := make(map[int]bool)

	// 从prevIndex开始尝试播放
	for {
		// 如果已经尝试过这个索引，说明所有歌曲都损坏了
		if triedIndices[prevIndex] {
			// 所有歌曲都损坏，停止播放
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

		// 尝试播放歌曲
		err := p.app.PlaySongWithSwitch(prevSong, false)
		if err == nil {
			// 播放成功
			return
		}
		// 播放失败，标记文件为损坏
		p.app.MarkFileAsCorrupted(prevSong)

		// 播放失败，尝试上一首
		if prevIndex == 0 {
			prevIndex = len(p.app.Playlist) - 1
		} else {
			prevIndex = prevIndex - 1
		}

		// 如果回到当前歌曲，说明已经尝试了所有歌曲
		if prevIndex == currentIndex {
			// 所有歌曲都损坏，停止播放
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

// playPreviousInRandomMode 随机播放模式下的上一首逻辑
func (p *PlayerPage) playPreviousInRandomMode() {
	// 检查是否有历史记录
	if p.app.historyIndex <= 0 {
		// 没有历史记录，随机播放一首
		p.playRandomSong()
		return
	}

	// 设置导航标志
	p.app.isNavigatingHistory = true

	// 移动到历史记录中的上一首
	p.app.historyIndex--
	prevSong := p.app.playHistory[p.app.historyIndex]

	// 检查歌曲是否还在播放列表中
	if p.isSongInPlaylist(prevSong) {
		// 播放历史记录中的歌曲（不调用 addToPlayHistory）
		p.playSongFromHistory(prevSong, false)
	} else {
		// 歌曲不在播放列表中，继续向前查找
		p.playPreviousInRandomMode()
	}

	// 更新切歌时间
	p.lastSwitchTime = time.Now()
}

// playRandomSong 随机播放一首歌曲
func (p *PlayerPage) playRandomSong() {
	if len(p.app.Playlist) == 0 {
		return
	}

	// 找到当前歌曲在播放列表中的位置
	currentIndex := -1
	for i, song := range p.app.Playlist {
		if song == p.flacPath {
			currentIndex = i
			break
		}
	}

	var randomIndex int
	if len(p.app.Playlist) > 1 {
		// 随机选择一首不同的歌曲
		for {
			randomIndex = rand.Intn(len(p.app.Playlist))
			if randomIndex != currentIndex {
				break
			}
		}
	} else {
		randomIndex = 0
	}

	// 播放随机选择的歌曲
	p.app.PlaySongWithSwitch(p.app.Playlist[randomIndex], false)

	// 更新切歌时间
	p.lastSwitchTime = time.Now()
}

// isSongInPlaylist 检查歌曲是否在播放列表中
func (p *PlayerPage) isSongInPlaylist(songPath string) bool {
	for _, song := range p.app.Playlist {
		if song == songPath {
			return true
		}
	}
	return false
}

// isCurrentSongInHistory 检查当前歌曲是否在历史记录中
func (p *PlayerPage) isCurrentSongInHistory() bool {
	if p.app.historyIndex < 0 || p.app.historyIndex >= len(p.app.playHistory) {
		return false
	}
	return p.app.playHistory[p.app.historyIndex] == p.flacPath
}

// playSongFromHistory 从历史记录播放歌曲（不添加新的历史记录）
func (p *PlayerPage) playSongFromHistory(songPath string, switchToPlayer bool) error {
	// 如果是同一首歌，不做任何操作
	if p.app.currentSongPath == songPath && p.app.player != nil {
		if switchToPlayer {
			// 切换到播放页面
			p.app.switchToPage(0) // PlayerPage
		}
		return nil
	}

	// 停止当前播放
	speaker.Lock()
	if p.app.player != nil {
		p.app.player.ctrl.Paused = true
	}
	speaker.Unlock()

	// 加载新文件
	f, err := os.Open(songPath)
	if err != nil {
		return fmt.Errorf("打开文件失败: %v", err)
	}

	streamer, format, err := flac.Decode(f)
	if err != nil {
		f.Close()
		return fmt.Errorf("解码FLAC失败: %v", err)
	}

	// 重新初始化speaker（如果采样率不同）
	speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/30))

	// 创建新的播放器
	player, err := newAudioPlayer(streamer, format, p.app.volume, p.app.playbackRate)
	if err != nil {
		f.Close()
		return fmt.Errorf("创建播放器失败: %v", err)
	}

	// 更新MPRIS服务
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

	// 更新应用状态
	speaker.Lock()
	p.app.player = player
	p.app.mprisServer = mprisServer
	p.app.currentSongPath = songPath
	speaker.Unlock()

	// 注意：这里不调用 addToPlayHistory，因为我们是在历史记录中导航

	// 开始播放
	speaker.Play(p.app.player.volume)

	// 更新PlayerPage
	p.UpdateSong(songPath)
	// 无论是否跳转页面，都重新渲染播放页面
	if !switchToPlayer {
		// 不跳转页面时，清理屏幕并重新渲染
		fmt.Print("\x1b[2J\x1b[3J\x1b[H") // 完全清理屏幕
		p.Init()
		p.View()
	}

	// 根据参数决定是否切换到播放页面
	if switchToPlayer {
		fmt.Print("\x1b[2J\x1b[3J\x1b[H") // 完全清理屏幕
		p.app.currentPageIndex = 0        // 直接设置页面索引
		p.Init()
		p.View()
	}

	return nil
}

// playNextInRandomMode 随机播放模式下的下一首逻辑
func (p *PlayerPage) playNextInRandomMode() {
	// 检查是否在历史记录末尾
	if p.app.historyIndex >= len(p.app.playHistory)-1 {
		// 在历史记录末尾，随机播放一首
		p.playRandomSong()
		// 到达历史记录末尾，重置导航标志
		p.app.isNavigatingHistory = false
		return
	}

	// 设置导航标志
	p.app.isNavigatingHistory = true

	// 移动到历史记录中的下一首
	p.app.historyIndex++
	nextSong := p.app.playHistory[p.app.historyIndex]

	// 检查歌曲是否还在播放列表中
	if p.isSongInPlaylist(nextSong) {
		// 播放历史记录中的歌曲（不调用 addToPlayHistory）
		p.playSongFromHistory(nextSong, false)
	} else {
		// 歌曲不在播放列表中，继续向后查找
		p.playNextInRandomMode()
	}

	// 更新切歌时间
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
	initialVol float64 // 初始音量，用于重置
}

func newAudioPlayer(streamer beep.StreamSeeker, format beep.Format, volumeLevel float64, playbackRate float64) (*audioPlayer, error) {
	// 默认使用无限循环（单曲循环模式）
	loopStreamer := beep.Loop(-1, streamer)
	ctrl := &beep.Ctrl{Streamer: loopStreamer}
	resampler := beep.ResampleRatio(4, 1, ctrl)
	volume := &effects.Volume{Streamer: resampler, Base: 2}
	// 使用传入的音量和播放速度设置
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

					if showNothing || showTextOnly {
						startCol, startRow, imageWidthInChars, imageHeightInChars = 0, 0, 0, 0
					} else if showInfoOnly {
						startCol, startRow = (w-imageWidthInChars)/2, (h-imageHeightInChars)/2
					} else if isWideTerminal {
						startCol, startRow = 1, (h-imageHeightInChars)/2
					} else {
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
		if p.imageRightEdge > 0 && w-p.imageRightEdge >= 30 {
			p.updateRightPanel(w)
		}
	} else {
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

	// --- Indicators (Volume & Rate) ---
	indicatorRow := row - 1
	// Ensure we don't draw at or above the first row, and there's a progress bar to align with.
	if indicatorRow > 0 && width > 0 {
		// Only clear the indicator area, not the whole line
		fmt.Printf("\x1b[%d;%dH\x1b[K", indicatorRow, startCol)

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
		for i := 0; i < playedChars; i++ {
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
	// This function remains unchanged.
	f, err := os.Open(flacPath)
	if err != nil {
		return "未知", "未知", "未知"
	}
	defer f.Close()
	m, err := tag.ReadFrom(f)
	if err != nil {
		return "未知", "未知", "未知"
	}
	title, artist, album = m.Title(), m.Artist(), m.Album()
	if title == "" {
		title = "未知"
	}
	if artist == "" {
		artist = "未知"
	}
	if album == "" {
		album = "未知"
	}
	return title, artist, album
}

func analyzeCoverColor(img image.Image) (r, g, b int) {
	// This function remains unchanged.
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pr, pg, pb, _ := img.At(x, y).RGBA()
			r8, g8, b8 := int(pr>>8), int(pg>>8), int(pb>>8)
			brightness := 0.2126*float64(r8) + 0.7152*float64(g8) + 0.0722*float64(b8)
			isBright := brightness > 160
			isNotGray := math.Abs(float64(r8)-float64(g8)) > 25 || math.Abs(float64(g8)-float64(b8)) > 25
			isNotWhite := !(r8 > 220 && g8 > 220 && b8 > 220)
			if isBright && isNotGray && isNotWhite {
				return r8, g8, b8
			}
		}
	}
	return 255, 255, 255
}
