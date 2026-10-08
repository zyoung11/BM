package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	_ "image/png"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dhowden/tag"
	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/effects"
	"github.com/gopxl/beep/v2/flac"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/gopxl/beep/v2/vorbis"
	"github.com/gopxl/beep/v2/wav"
	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"golang.org/x/sys/unix"

	"github.com/nfnt/resize"
)

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
	notifDisplayTimer                     int
	layoutIndicatorTicks                  int
	overrideLayout                        LayoutType // Override layout (-1=none). / 覆盖布局（-1=无）。
	currentLayout                         LayoutType
	lastLayoutSwitchTime                  time.Time // Debounce for layout switching. / 布局切换防抖。

	// Per-song cover cache: decoded image, 960x960 normalized version and
	// dominant color, keyed by song path to avoid re-decoding on every redraw.
	coverCachePath  string
	coverCacheImg   image.Image
	coverCacheNorm  image.Image
	coverCacheR     int
	coverCacheG     int
	coverCacheB     int
	coverCacheValid bool

	// Debounce mechanism for song switching. / 切歌防抖机制。
	lastSwitchTime time.Time
}

// NewPlayerPage creates a new instance of the player page.
//
// NewPlayerPage 创建一个新的播放器页面实例。
func NewPlayerPage(app *App, flacPath string, cellW, cellH int, overrideLayout int) *PlayerPage {
	return &PlayerPage{
		app:            app,
		flacPath:       flacPath,
		useCoverColor:  true,
		cellW:          cellW,
		cellH:          cellH,
		overrideLayout: LayoutType(overrideLayout),
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
		newPos := max(player.streamer.Position()-player.sampleRate.N(time.Second*5), 0)
		if err := player.streamer.Seek(newPos); err != nil {
			// ignore seek errors
		}
		speaker.Unlock()
		if mprisServer != nil {
			mprisServer.NotifySeek(p.currentPositionInMicroseconds())
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
			mprisServer.NotifySeek(p.currentPositionInMicroseconds())
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
		p.app.setPlayMode((p.app.playMode + 1) % 3)
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
	} else if IsKey(key, GlobalConfig.Keymap.Player.ToggleLayout) && !p.app.forcedTextMode {
		p.cycleLayout()
	} else if IsKey(key, GlobalConfig.Keymap.Player.ToggleNotifications) {
		p.app.notificationsEnabled = !p.app.notificationsEnabled
		p.notifDisplayTimer = 10
	} else {
		needsRedraw = false
	}

	if needsRedraw {
		p.updateStatus()
	}

	return nil, false, nil
}

// HandleSignal handles system signals, like window resizing. A resize waits
// a short moment before redrawing so the terminal settles and the size read
// by the renderer reflects the final dimensions.
//
// HandleSignal 处理系统信号，例如窗口大小调整。窗口尺寸变化后先等待片刻再
// 重绘，让终端稳定下来，使渲染时读到的尺寸反映最终结果。
func (p *PlayerPage) HandleSignal(sig os.Signal) error {
	if sig == syscall.SIGWINCH {
		time.Sleep(50 * time.Millisecond)
		p.View()
	}
	return nil
}

// View renders the player UI to the screen. While the layout indicator is
// active the screen carries the indicator alone instead of the regular
// player content.
//
// View 将播放器UI渲染到屏幕上。布局指示器生效期间屏幕上只显示指示器，
// 不显示常规播放器内容。
func (p *PlayerPage) View() {
	p.app.beginFrame()
	defer p.app.endFrame()
	if p.layoutIndicatorTicks > 0 {
		p.showLayoutIndicator()
		return
	}
	if p.flacPath == "" {
		p.displayEmptyState()
		return
	}
	p.renderWithLayout()
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
	msgX := (w - runewidth.StringWidth(msg)) / 2
	msg2X := (w - runewidth.StringWidth(msg2)) / 2
	centerRow := h / 2

	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", centerRow-1, msgX, msg)
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", centerRow+1, msg2X, msg2)
}

// cycleLayout cycles through the available layout overrides.
// auto -> narrow -> text -> image -> auto
//
// cycleLayout 循环切换可用的布局覆盖。
// 自动 -> 窄屏样式 -> 纯文本 -> 纯封面 -> 自动
func (p *PlayerPage) cycleLayout() {
	if p.app.forcedTextMode {
		return
	}
	if time.Since(p.lastLayoutSwitchTime) < time.Duration(GlobalConfig.App.LayoutDebounceMs)*time.Millisecond {
		return
	}
	p.lastLayoutSwitchTime = time.Now()

	var nextLayout LayoutType

	switch p.overrideLayout {
	case -1:
		nextLayout = LayoutSwitchNarrow
	case LayoutSwitchNarrow:
		nextLayout = LayoutSwitchText
	case LayoutSwitchText:
		nextLayout = LayoutSwitchImage
	default:
		nextLayout = -1
	}

	p.overrideLayout = nextLayout
	if err := SaveOverrideLayout(int(nextLayout)); err != nil {
		l.Warnf("Could not save layout: %v\n\n无法保存布局: %v", err, err)
	}
	p.layoutIndicatorTicks = layoutIndicatorTicks
	p.View()
}

// showLayoutIndicator draws the current layout mode name in the middle of an
// otherwise blank screen. The name stays alone on screen until the indicator
// ticks counted down by Tick run out and View restores the regular content.
//
// showLayoutIndicator 在空白屏幕中央显示当前布局模式名称。在 Tick 递减的
// 指示器计时结束前，名称单独停留在屏幕上；计时结束后 View 恢复常规内容。
func (p *PlayerPage) showLayoutIndicator() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H")

	var layoutStr string
	switch p.overrideLayout {
	case -1:
		layoutStr = "auto"
	case LayoutSwitchText:
		layoutStr = "text"
	case LayoutSwitchImage:
		layoutStr = "image"
	case LayoutSwitchNarrow:
		layoutStr = "narrow"
	default:
		layoutStr = "auto"
	}

	msgX := (w - runewidth.StringWidth(layoutStr)) / 2
	centerRow := h / 2
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", centerRow, msgX, layoutStr)
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
	if p.notifDisplayTimer > 0 {
		p.notifDisplayTimer--
	}
	if p.layoutIndicatorTicks > 0 {
		p.layoutIndicatorTicks--
		if p.layoutIndicatorTicks == 0 {
			p.View()
		}
		return
	}

	if p.flacPath == "" {
		return
	}

	p.updateStatus()
}

// currentPositionInMicroseconds is a helper to get the player position for MPRIS.
//
// currentPositionInMicroseconds 是一个辅助函数，用于为MPRIS获取播放器位置。
func (p *PlayerPage) currentPositionInMicroseconds() int64 {
	if p.app.player == nil {
		return 0
	}
	speaker.Lock()
	pos := p.app.player.streamer.Position()
	speaker.Unlock()
	return int64(float64(pos) / float64(p.app.player.sampleRate) * 1e6)
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

	if p.app.switchedToRandom {
		p.app.recordCurrentSongToHistory()
		p.app.switchedToRandom = false
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
			nextIndex = p.app.pickRandomIndex(p.flacPath)
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
			p.app.stopPlaybackAndClear()
			return
		}

		triedIndices[nextIndex] = true
		nextSong := p.app.Playlist[nextIndex]

		err := p.app.PlaySongWithSwitchAndRender(nextSong, true, true)
		if err == nil {
			if len(p.app.Playlist) > 1 {
				title, artist, _ := getSongMetadata(nextSong)
				coverPath := saveCoverArt(nextSong)
				p.app.sendNotification(artist, title, coverPath)
			}
			return
		}
		p.app.MarkFileAsCorrupted(nextSong)

		nextIndex = (nextIndex + 1) % len(p.app.Playlist)

		if nextIndex == currentIndex {
			p.app.stopPlaybackAndClear()
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

	if p.app.switchedToRandom {
		p.app.recordCurrentSongToHistory()
		p.app.switchedToRandom = false
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
			p.app.stopPlaybackAndClear()
			return
		}

		triedIndices[prevIndex] = true
		prevSong := p.app.Playlist[prevIndex]

		err := p.app.PlaySongWithSwitchAndRender(prevSong, true, true)
		if err == nil {
			if len(p.app.Playlist) > 1 {
				title, artist, _ := getSongMetadata(prevSong)
				coverPath := saveCoverArt(prevSong)
				p.app.sendNotification(artist, title, coverPath)
			}
			return
		}
		p.app.MarkFileAsCorrupted(prevSong)

		if prevIndex == 0 {
			prevIndex = len(p.app.Playlist) - 1
		} else {
			prevIndex = prevIndex - 1
		}

		if prevIndex == currentIndex {
			p.app.stopPlaybackAndClear()
			return
		}
	}
}

// playPreviousInRandomMode handles the logic for "previous" in random mode by using play history.
// When at the beginning of history, cycles back to the current song.
//
// playPreviousInRandomMode 通过使用播放历史来处理随机模式下的“上一首”逻辑。
// 当到达历史记录开头时，循环回到当前歌曲。
func (p *PlayerPage) playPreviousInRandomMode() {
	if len(p.app.playHistory) == 0 {
		return
	}

	if p.app.historyIndex <= 0 {
		p.app.historyIndex = len(p.app.playHistory) - 1
		currentSong := p.app.playHistory[p.app.historyIndex]
		if p.isSongInPlaylist(currentSong) {
			p.playSongFromHistory(currentSong, true)
		}
		p.lastSwitchTime = time.Now()
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

// pickRandomIndex selects a random index from the playlist for shuffle mode.
// It uses a sliding window: excludes the last N unique songs from play history
// to avoid frequent repeats. The window is capped at playlistLen-2 to ensure
// at least one candidate is always available.
// N = 0: pure random (disabled).
// N < 0 or N >= playlistLen: treat as the maximum possible window, meaning
//
//	songs cycle through the entire playlist before any repeats.
//
// N > 0: exclude last N unique songs from history.
//
// pickRandomIndex 使用滑动窗口从播放列表中随机选择下一首。
// 排除播放历史中最近 N 首不重复歌曲以避免频繁重复。
// 窗口上限为 playlistLen-2，保证始终至少有一个候选。
// N = 0: 纯随机（禁用）。
// N < 0 或 N >= 歌单长度: 使用最大窗口，歌单内所有歌曲循环一遍后才重复。
// N > 0: 排除最近 N 首不重复歌曲。
func (a *App) pickRandomIndex(currentPath string) int {
	playlistLen := len(a.Playlist)
	if playlistLen == 0 {
		return 0
	}
	if playlistLen == 1 {
		return 0
	}

	currentIndex := slices.Index(a.Playlist, currentPath)

	n := GlobalConfig.App.ShuffleHistoryWindow
	if n == 0 {
		for {
			idx := rand.Intn(playlistLen)
			if idx != currentIndex {
				return idx
			}
		}
	}

	maxN := playlistLen - 2
	if n < 0 || n > maxN {
		n = maxN
	}
	if n < 0 {
		n = 0
	}

	recentlyPlayed := make(map[string]bool, n)
	count := 0
	for i := len(a.playHistory) - 1; i >= 0 && count < n; i-- {
		song := a.playHistory[i]
		if song != currentPath && !recentlyPlayed[song] {
			recentlyPlayed[song] = true
			count++
		}
	}

	candidates := make([]int, 0, playlistLen)
	for i, song := range a.Playlist {
		if i == currentIndex {
			continue
		}
		if !recentlyPlayed[song] {
			candidates = append(candidates, i)
		}
	}

	if len(candidates) == 0 {
		for {
			idx := rand.Intn(playlistLen)
			if idx != currentIndex {
				return idx
			}
		}
	}

	return candidates[rand.Intn(len(candidates))]
}

// playRandomSong plays a random song from the playlist.
//
// playRandomSong 从播放列表中随机播放一首歌曲。
func (p *PlayerPage) playRandomSong() {
	if len(p.app.Playlist) == 0 {
		return
	}

	randomIndex := p.app.pickRandomIndex(p.flacPath)

	p.app.PlaySongWithSwitchAndRender(p.app.Playlist[randomIndex], true, true)

	p.lastSwitchTime = time.Now()
}

// isSongInPlaylist checks if a song is in the current playlist.
//
// isSongInPlaylist 检查歌曲是否在当前播放列表中。
func (p *PlayerPage) isSongInPlaylist(songPath string) bool {
	return slices.Contains(p.app.Playlist, songPath)
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

	p.app.stopCurrentPlayback()
	p.app.invalidatePendingNext()

	streamer, format, err := decodeAudioFile(songPath)
	if err != nil {
		p.app.MarkFileAsCorrupted(songPath)
		return fmt.Errorf("Failed to decode audio: %v\n\n解码音频失败: %v", err, err)
	}

	if p.app.sampleRate != format.SampleRate {
		if err := speaker.ReInit(format.SampleRate, format.SampleRate.N(time.Second/30)); err != nil {
			streamer.Close()
			return fmt.Errorf("Failed to reinit speaker: %v\n\n重新初始化扬声器失败: %v", err, err)
		}
		p.app.sampleRate = format.SampleRate
	}

	audioStream := streamer

	player, err := newAudioPlayer(audioStream, format, p.app.volume, p.app.playbackRate, songPath, p.app.playMode == 0)
	if err != nil {
		streamer.Close()
		return fmt.Errorf("Failed to create player: %v\n\n创建播放器失败: %v", err, err)
	}

	if player.queue != nil {
		p.app.armQueueExhaust(player.queue)
	}

	speaker.Lock()
	p.app.player = player
	speaker.Unlock()

	speaker.Play(p.app.player.volume)

	p.app.setCurrentSong(songPath)

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
	p.app.mprisServer = mprisServer
	speaker.Unlock()

	// Reset cover image position and dimensions
	// 重置封面图片位置和尺寸
	p.imageTop = 0
	p.imageHeight = 0
	p.imageRightEdge = 0

	if switchToPlayer {
		p.UpdateSong(songPath)
		fmt.Print("\x1b[2J\x1b[3J\x1b[H")
		p.app.currentPageIndex = 0
		p.View()
	} else {
		// When not switching to player page, just update the song path without rendering
		// 当不切换到播放器页面时，只更新歌曲路径而不渲染
		p.UpdateSong(songPath)
	}

	if len(p.app.Playlist) > 1 {
		title, artist, _ := getSongMetadata(songPath)
		coverPath := saveCoverArt(songPath)
		p.app.sendNotification(artist, title, coverPath)
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
	streamer   beep.StreamSeekCloser
	ctrl       *beep.Ctrl
	resampler  *beep.Resampler
	volume     *effects.Volume
	queue      *gaplessQueue
}

// newAudioPlayer builds the playback chain around the decoded song. Repeat-one
// mode loops through Loop2; list/random modes sequence through the gapless
// queue so the next song can be handed off without a gap.
//
// newAudioPlayer 围绕已解码歌曲构建播放链。单曲循环模式用 Loop2 循环；
// 列表/随机模式通过无缝队列顺序播放，使下一首可以无间隙接续。
func newAudioPlayer(streamer beep.StreamSeekCloser, format beep.Format, volumeLevel float64, playbackRate float64, songPath string, loop bool) (*audioPlayer, error) {
	var inner beep.Streamer = streamer
	var q *gaplessQueue
	if loop {
		loopStreamer, err := beep.Loop2(streamer)
		if err != nil {
			return nil, fmt.Errorf("Failed to create loop streamer: %v\n\n创建循环流失败: %v", err, err)
		}
		inner = loopStreamer
	} else {
		q = newGaplessQueue(streamer, songPath)
		inner = q
	}
	ctrl := &beep.Ctrl{Streamer: inner}
	resampler := beep.ResampleRatio(4, 1, ctrl)
	volume := &effects.Volume{Streamer: resampler, Base: 2}
	volume.Volume = volumeLevel
	resampler.SetRatio(playbackRate)
	p := &audioPlayer{format.SampleRate, streamer, ctrl, resampler, volume, q}
	if q != nil {
		q.player = p
	}
	return p, nil
}

// saveCoverArt extracts the cover art from an audio file and saves it to a temporary file.
// If the audio file has no cover, it tries to find an image in the same folder (if enabled in config).
// If no folder image is found, it uses the default cover image.
// It returns the path to the temporary file.
func saveCoverArt(audioPath string) string {
	coverImg := getCoverFromAudioFile(audioPath)

	if coverImg == nil && GlobalConfig != nil && GlobalConfig.App.EnableFolderCovers {
		coverImg = getFolderCoverImage(audioPath)
	}

	if coverImg == nil {
		defaultCoverPath := getDefaultCoverPath()
		if defaultCoverPath != "" {
			if img, err := loadImageFile(defaultCoverPath); err == nil {
				coverImg = img
			}
		}
	}

	if coverImg == nil {
		return ""
	}

	coverImg = resize.Thumbnail(256, 256, coverImg, resize.Bilinear)

	tempFile, err := os.CreateTemp("", "bm-cover-*.png")
	if err != nil {
		return ""
	}
	defer tempFile.Close()

	if err := png.Encode(tempFile, coverImg); err != nil {
		os.Remove(tempFile.Name())
		return ""
	}

	trackTempCoverFile(tempFile.Name())
	return tempFile.Name()
}

// tempCoverFiles tracks temporary cover files created at runtime so they can
// be removed on exit.
//
// tempCoverFiles 记录运行时创建的临时封面文件，以便退出时删除。
var tempCoverFiles []string

// trackTempCoverFile registers a temporary cover file for cleanup on exit.
//
// trackTempCoverFile 登记临时封面文件，在退出时统一清理。
func trackTempCoverFile(path string) {
	tempCoverFiles = append(tempCoverFiles, path)
}

// cleanupTempCoverFiles removes all tracked temporary cover files.
//
// cleanupTempCoverFiles 删除所有已登记的临时封面文件。
func cleanupTempCoverFiles() {
	for _, path := range tempCoverFiles {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			l.Warnf("could not remove temp cover file: %v\n\n警告: 无法删除临时封面文件: %v", err, err)
		}
	}
	tempCoverFiles = nil
}

// --- TUI / Drawing ---
// All drawing functions are now methods on PlayerPage to access state.

// refreshCellSize updates the player's cell pixel dimensions using the TIOCGWINSZ ioctl.
// This accounts for DPI changes when the terminal is moved between displays.
//
// refreshCellSize 使用 TIOCGWINSZ ioctl 更新播放器的字符单元格像素尺寸。
// 这可以应对终端在显示器之间移动时 DPI 变化的情况。
func (p *PlayerPage) refreshCellSize() {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return
	}

	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return
	}

	if w > 0 && ws.Xpixel > 0 {
		p.cellW = int(ws.Xpixel) / w
	}
	if h > 0 && ws.Ypixel > 0 {
		p.cellH = int(ws.Ypixel) / h
	}
	if p.cellW == 0 {
		p.cellW = 1
	}
	if p.cellH == 0 {
		p.cellH = 1
	}
}

// updateStatus redraws the dynamic text and progress elements with the layout
// chosen by renderWithLayout so periodic ticks match the first draw exactly.
//
// updateStatus 使用 renderWithLayout 选定的布局重绘动态文本与进度条，
// 保证周期性刷新与首次绘制的位置完全一致。
func (p *PlayerPage) updateStatus() {
	if p.app.currentPageIndex != 0 || p.flacPath == "" || p.layoutIndicatorTicks > 0 {
		return
	}

	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return
	}

	if w < minLayoutWidth || h < minLayoutHeight {
		return
	}

	p.renderTextByLayout(p.currentLayout, w, h)
}

// updateRightPanel renders the song info and the progress bar in the panel
// right of the cover for the wide layout. The panel is as wide as the capped
// progress bar plus its side padding, and both share its horizontal center.
//
// updateRightPanel 为宽屏布局在封面右侧的面板中渲染歌曲信息与进度条。
// 面板宽度为封顶后的进度条宽度加两侧留白，两者共享面板的水平中心。
func (p *PlayerPage) updateRightPanel(w int) {
	if p.imageHeight < rightPanelMinHeight {
		return
	}

	bar := progressBarWidth(w)
	panelWidth := bar + 2*progressBarPad
	centerCol := p.imageRightEdge + panelWidth/2

	partHeight := p.imageHeight / 3
	titleRow := p.imageTop + partHeight + partHeight/2 - 1
	albumRow := titleRow + 2
	progressRow := p.imageTop + (2 * partHeight) + partHeight/2

	if progressRow-albumRow < 1 {
		return
	}
	if titleRow < p.imageTop {
		titleRow = p.imageTop
	}
	if progressRow >= p.imageTop+p.imageHeight {
		progressRow = p.imageTop + p.imageHeight - 1
	}

	p.drawSongInfo(titleRow, centerCol, panelWidth-2)

	p.drawProgressBar(progressRow, centerCol-bar/2, bar, p.getColorCode())
}

// drawSongInfo draws the centered title, artist and album lines starting at
// infoRow. Strings wider than maxTextWidth are shortened with an ellipsis; a
// non-positive value keeps them untouched.
//
// drawSongInfo 从 infoRow 行开始绘制居中的标题、艺术家与专辑三行。
// 宽度超过 maxTextWidth 的字符串以省略号截断；传入非正值则不截断。
func (p *PlayerPage) drawSongInfo(infoRow, centerCol, maxTextWidth int) {
	title, artist, album := getSongMetadata(p.flacPath)
	if maxTextWidth > 0 {
		title = truncateToWidthFromStart(title, maxTextWidth)
		artist = truncateToWidthFromStart(artist, maxTextWidth)
		album = truncateToWidthFromStart(album, maxTextWidth)
	}

	colorCode := p.getColorCode()
	titleWidth := runewidth.StringWidth(title)
	artistWidth := runewidth.StringWidth(artist)
	albumWidth := runewidth.StringWidth(album)

	fmt.Printf("\x1b[%d;%dH\x1b[K%s\x1b[1m%s\x1b[0m", infoRow, centerCol-titleWidth/2, colorCode, title)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", infoRow+1, centerCol-artistWidth/2, colorCode, artist)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", infoRow+2, centerCol-albumWidth/2, colorCode, album)
}

// updateNarrowStatus renders text and progress for the narrow layouts. The
// content is centered inside a virtual column band so the auto narrow layout
// and the narrow override place elements identically; rows come from the same
// narrowRows composition that placed the cover.
//
// updateNarrowStatus 为窄屏布局渲染文本和进度条。内容在虚拟列宽内居中，
// 自动窄屏布局与窄屏覆盖模式的元素位置完全一致；行号来自放置封面的同一
// narrowRows 版面。
func (p *PlayerPage) updateNarrowStatus(w, h int) {
	_, infoRow, progressRow := narrowRows(p.imageHeight, h)

	virtualWidth := min(GlobalConfig.App.ProgressBarWidth+2*progressBarPad, w)
	offset := (w - virtualWidth) / 2
	centerCol := offset + virtualWidth/2

	p.drawSongInfo(infoRow, centerCol, virtualWidth-2)

	bar := progressBarWidth(w)
	progressBarStartCol := centerCol - bar/2

	p.drawProgressBar(progressRow, progressBarStartCol, bar, p.getColorCode())
}

// textBlockStartRow returns the first row of a content block of the given
// height so the whitespace above and below it stays even, with the extra row
// on top when the whitespace count is odd.
//
// textBlockStartRow 返回给定高度内容块的起始行，使上下留白尽量相等，
// 留白总数为奇数时上面多留一行。
func textBlockStartRow(h, contentHeight int) int {
	blank := max(h-contentHeight, 0)
	return blank - blank/2 + 1
}

// updateTextOnlyMode renders the centered text block and the progress bar for
// the text-only layout.
//
// updateTextOnlyMode 为纯文本布局渲染居中的文本块与进度条。
func (p *PlayerPage) updateTextOnlyMode(w, h int) {
	infoRow := textBlockStartRow(h, textOnlyBlockHeight)
	p.drawSongInfo(infoRow, w/2, w-2)

	bar := progressBarWidth(w)
	progressBarStartCol := (w - bar) / 2

	p.drawProgressBar(infoRow+textOnlyBlockHeight-1, progressBarStartCol, bar, p.getColorCode())
}

// updateSwitchTextMode renders centered text and progress bar for switch layout.
//
// updateSwitchTextMode 为切换布局渲染居中的文本和进度条。
// updateSwitchTextMode renders centered text and progress bar for the text
// layout the O key cycles to.
//
// updateSwitchTextMode 为 O 键循环到的文本布局渲染居中的文字与进度条。
func (p *PlayerPage) updateSwitchTextMode(w, h int) {
	infoRow := textBlockStartRow(h, switchTextBlockHeight)
	p.drawSongInfo(infoRow, w/2, w-2)

	bar := progressBarWidth(w)
	progressBarStartCol := (w - bar) / 2

	p.drawProgressBar(infoRow+switchTextBlockHeight-1, progressBarStartCol, bar, p.getColorCode())
}

func (p *PlayerPage) drawProgressBar(row, startCol, width int, colorCode string) {
	// 检查player是否可用
	if p.app.player == nil {
		return
	}

	// --- Indicators (Volume & Rate) ---
	indicatorRow := row - 1
	if indicatorRow > 0 && width > 0 {
		fmt.Printf("\x1b[%d;%dH\x1b[K", indicatorRow, startCol)

		if p.volumeDisplayTimer > 0 {
			volPercent := int(math.Round(p.app.linearVolume * 100))
			volStr := fmt.Sprintf("%d%%", volPercent)
			fmt.Printf("\x1b[%d;%dH%s%s\x1b[0m", indicatorRow, startCol, colorCode, volStr)
		}

		if p.rateDisplayTimer > 0 {
			rateVal := p.app.player.resampler.Ratio()
			rateStr := fmt.Sprintf("%.2fx", rateVal)
			rateWidth := runewidth.StringWidth(rateStr)
			rateStartCol := startCol + width - rateWidth
			if rateStartCol < startCol {
				rateStartCol = startCol + 7
			}
			fmt.Printf("\x1b[%d;%dH%s%s\x1b[0m", indicatorRow, rateStartCol, colorCode, rateStr)
		}

		if p.notifDisplayTimer > 0 {
			notifIcon := GlobalConfig.ActiveIcons.NotificationOn
			if !p.app.notificationsEnabled {
				notifIcon = GlobalConfig.ActiveIcons.NotificationOff
			}
			notifWidth := runewidth.StringWidth(notifIcon)
			notifCol := startCol + (width-notifWidth)/2
			fmt.Printf("\x1b[%d;%dH%s%s\x1b[0m", indicatorRow, notifCol, colorCode, notifIcon)
		}
	}

	speaker.Lock()
	currentPos := p.app.player.streamer.Position()
	totalLen := p.app.player.streamer.Len()
	paused := p.app.player.ctrl.Paused
	speaker.Unlock()

	progress := 0.0
	if totalLen > 0 {
		progress = float64(currentPos) / float64(totalLen)
		if totalLen-currentPos <= p.app.player.sampleRate.N(time.Second) {
			progress = 1.0
		}
	}

	playedChars := int(float64(width) * progress)

	icons := GlobalConfig.ActiveIcons

	icon := icons.Pause
	if paused {
		icon = icons.Play
	}

	modeIcon := icons.RepeatOne
	switch p.app.playMode {
	case 1:
		modeIcon = icons.RepeatAll
	case 2:
		modeIcon = icons.Shuffle
	}

	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s", row, startCol-2, colorCode, icon)

	var bar strings.Builder
	if playedChars > 0 {
		bar.WriteString(colorCode)
		for range playedChars {
			bar.WriteString(icons.ProgressFilled)
		}
	}
	bar.WriteString(colorCode)
	for i := playedChars; i < width; i++ {
		bar.WriteString(icons.ProgressEmpty)
	}

	fmt.Printf("\x1b[0m\x1b[%d;%dH%s", row, startCol, bar.String())
	fmt.Printf("\x1b[0m\x1b[%d;%dH\x1b[K%s%s\x1b[0m", row, startCol+width+1, colorCode, modeIcon)
}

func (p *PlayerPage) getColorCode() string {
	if p.useCoverColor {
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", p.coverColorR, p.coverColorG, p.coverColorB)
	}
	return "\x1b[37m"
}

// --- Misc Helper Functions ---

// parseMetadataFromFilename attempts to extract artist and title from filename.
// Format: "Artist - Title.ext" or "Artist1,Artist2 - Title.ext"
// Returns empty strings if parsing fails.
//
// parseMetadataFromFilename 尝试从文件名中提取艺术家和标题。
// 格式: "艺术家 - 标题.扩展名" 或 "艺术家1,艺术家2 - 标题.扩展名"
// 如果解析失败则返回空字符串。
func parseMetadataFromFilename(filePath string) (title, artist, album string) {
	filename := filepath.Base(filePath)
	ext := filepath.Ext(filename)
	nameWithoutExt := filename[:len(filename)-len(ext)]

	parts := strings.SplitN(nameWithoutExt, " - ", 2)
	if len(parts) == 2 {
		artist = strings.TrimSpace(parts[0])
		title = strings.TrimSpace(parts[1])

		if len(title) > 3 && title[2] == '.' && title[3] == ' ' {
			title = title[4:]
		}
		if len(title) > 3 && title[2] == ' ' && title[3] == '-' && title[4] == ' ' {
			title = title[5:]
		}

		title = strings.TrimSpace(title)
		artist = strings.TrimSpace(artist)
	}

	return title, artist, ""
}

func getCellSize() (width, height int, err error) {
	if err := os.Stdin.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return 0, 0, fmt.Errorf("stdin does not support read deadlines: %w", err)
	}
	defer os.Stdin.SetReadDeadline(time.Time{})

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
		return 0, 0, fmt.Errorf("Unable to parse terminal response: %q\n\n无法解析的终端响应格式: %q", buf, buf)
	}
	content := buf[2 : len(buf)-1]
	parts := bytes.Split(content, []byte(";"))
	if len(parts) != 3 {
		return 0, 0, fmt.Errorf("Expected 3 response segments, got %d: %q\n\n预期的响应分段为3, 实际为 %d: %q", len(parts), buf, len(parts), buf)
	}
	if string(parts[0]) != "6" {
		return 0, 0, fmt.Errorf("Expected response code 6, got %s\n\n预期的响应代码为 6, 实际为 %s", parts[0], parts[0])
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

// songMetaCache caches parsed tags per file path, so repeated draws do not
// reopen and re-parse the audio file. Populated and read on the main thread
// only.
//
// songMetaCache 按文件路径缓存解析出的标签，避免重复绘制时反复打开并解析
// 音频文件。仅在主线程读写。
var songMetaCache = make(map[string]songMeta)

type songMeta struct {
	title, artist, album string
}

// getSongMetadata returns the song's title, artist and album, falling back to
// filename parsing; results are cached per path.
//
// getSongMetadata 返回歌曲的标题、艺术家与专辑，必要时回退到从文件名解析；
// 结果按路径缓存。
func getSongMetadata(flacPath string) (title, artist, album string) {
	if m, ok := songMetaCache[flacPath]; ok {
		return m.title, m.artist, m.album
	}
	title, artist, album = readSongMetadata(flacPath)
	songMetaCache[flacPath] = songMeta{title, artist, album}
	return title, artist, album
}

// readSongMetadata reads and parses the tags of an audio file without caching.
//
// readSongMetadata 读取并解析音频文件的标签，不走缓存。
func readSongMetadata(flacPath string) (title, artist, album string) {
	f, err := os.Open(flacPath)
	if err != nil {
		// Try to parse from filename as fallback
		return parseMetadataFromFilename(flacPath)
	}
	defer f.Close()
	m, err := tag.ReadFrom(f)
	if err != nil {
		return parseMetadataFromFilename(flacPath)
	}
	title, artist, album = m.Title(), m.Artist(), m.Album()

	if title == "" || artist == "" {
		filenameTitle, filenameArtist, filenameAlbum := parseMetadataFromFilename(flacPath)
		if title == "" && filenameTitle != "" {
			title = filenameTitle
		}
		if artist == "" && filenameArtist != "" {
			artist = filenameArtist
		}
		if album == "" && filenameAlbum != "" {
			album = filenameAlbum
		}
	}

	if title == "" {
		title = ""
	}
	if artist == "" {
		artist = ""
	}
	if album == "" {
		album = ""
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

	// Fallback to the configured default color when no suitable color is found
	// 当没有找到合适的颜色时，回退到配置的默认颜色
	return GlobalConfig.App.DefaultColorR, GlobalConfig.App.DefaultColorG, GlobalConfig.App.DefaultColorB
}

// decodeAudioFile decodes an audio file based on its extension.
//
// decodeAudioFile 根据文件扩展名解码音频文件。
func decodeAudioFile(filePath string) (beep.StreamSeekCloser, beep.Format, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, beep.Format{}, err
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".flac":
		return flac.Decode(f)
	case ".mp3":
		return mp3.Decode(f)
	case ".wav":
		return wav.Decode(f)
	case ".ogg":
		return vorbis.Decode(f)
	default:
		f.Close()
		return nil, beep.Format{}, fmt.Errorf("unsupported audio format: %s\n\n不支持的音频格式: %s", ext, ext)
	}
}

// getCoverFromAudioFile extracts cover art from an audio file.
//
// getCoverFromAudioFile 从音频文件中提取封面图片。
func getCoverFromAudioFile(filePath string) image.Image {
	f, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	m, err := tag.ReadFrom(f)
	if err != nil {
		return nil
	}

	pic := m.Picture()
	if pic == nil {
		return nil
	}

	img, _, err := image.Decode(bytes.NewReader(pic.Data))
	if err != nil {
		return nil
	}

	return img
}

// loadImageFile loads an image from a file path.
//
// loadImageFile 从文件路径加载图片。
func loadImageFile(filePath string) (image.Image, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}

	return img, nil
}

// getFolderCoverImage searches for image files (jpg, jpeg, png) in the same directory as the audio file.
// If multiple images are found, it randomly selects one.
// It prioritizes files with names that suggest they are cover images (cover, folder, album, etc.)
//
// getFolderCoverImage 在音频文件所在目录中搜索图片文件（jpg, jpeg, png）。
// 如果找到多个图片，则随机选择一个。
// 优先选择文件名暗示为封面的图片（cover、folder、album等）。
func getFolderCoverImage(audioPath string) image.Image {
	dir := filepath.Dir(audioPath)

	files, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var imageFiles []string
	var priorityImageFiles []string

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		name := file.Name()
		baseName := strings.ToLower(filepath.Base(name))
		ext := strings.ToLower(filepath.Ext(name))

		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
			fullPath := filepath.Join(dir, name)

			if strings.Contains(baseName, "cover") ||
				strings.Contains(baseName, "folder") ||
				strings.Contains(baseName, "album") ||
				strings.Contains(baseName, "art") ||
				strings.Contains(baseName, "front") {
				priorityImageFiles = append(priorityImageFiles, fullPath)
			} else {
				imageFiles = append(imageFiles, fullPath)
			}
		}
	}

	var selectedImage string
	if len(priorityImageFiles) > 0 {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		selectedImage = priorityImageFiles[rng.Intn(len(priorityImageFiles))]
	} else if len(imageFiles) > 0 {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		selectedImage = imageFiles[rng.Intn(len(imageFiles))]
	} else {
		return nil
	}

	img, err := loadImageFile(selectedImage)
	if err != nil {
		return nil
	}

	return img
}

// getDefaultCoverPath returns the path to the default cover image.
// It expands the ~ in the path and checks if the file exists.
//
// getDefaultCoverPath 返回默认封面图片的路径。
// 它会展开路径中的 ~ 并检查文件是否存在。
func getDefaultCoverPath() string {
	if GlobalConfig == nil || GlobalConfig.App.DefaultCoverPath == "" {
		return ""
	}

	// Expand ~ in the path
	path := GlobalConfig.App.DefaultCoverPath
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		path = filepath.Join(home, path[2:])
	}

	// Check if file exists and is a supported image format
	if _, err := os.Stat(path); err != nil {
		return ""
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
		return ""
	}

	return path
}
