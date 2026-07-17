package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
	"golang.org/x/term"
)

// songExistsInPlaylist checks if a song exists in the current playlist.
//
// songExistsInPlaylist 检查歌曲是否存在于当前播放列表中。
func songExistsInPlaylist(songPath string, playlist []string) bool {
	return slices.Contains(playlist, songPath)
}

// Key constants for special keys.
//
// 特殊按键的常量定义。
const (
	KeyArrowUp = 1000 + iota
	KeyArrowDown
	KeyArrowLeft
	KeyArrowRight
	KeyEnter
	KeyBackspace
)

// consumeCSISequence reads and discards a CSI (Control Sequence Introducer) escape
// sequence from the terminal. CSI sequences start with ESC [ followed by parameter
// bytes (0x30-0x3F), intermediate bytes (0x20-0x2F), and a final byte (0x40-0x7E).
// Only arrow key sequences (ending in A/B/C/D) are forwarded; all other CSI
// sequences are silently discarded to prevent ESC from leaking as a quit signal.
//
// consumeCSISequence 从终端读取并丢弃一个CSI（控制序列引导符）转义序列。
// CSI序列以 ESC [ 开头，后跟参数字节（0x30-0x3F）、中间字节（0x20-0x2F）和终止字节（0x40-0x7E）。
// 只有箭头键序列（以A/B/C/D结尾）会被转发；所有其他CSI序列会被静默丢弃，
// 以防止ESC泄露为退出信号。
func consumeCSISequence(keys chan rune, keyCh chan<- rune) {
	for {
		select {
		case b := <-keys:
			if b >= 0x40 && b <= 0x7E {
				switch b {
				case 'A':
					keyCh <- KeyArrowUp
				case 'B':
					keyCh <- KeyArrowDown
				case 'C':
					keyCh <- KeyArrowRight
				case 'D':
					keyCh <- KeyArrowLeft
				}
				return
			}
		case <-time.After(25 * time.Millisecond):
			return
		}
	}
}

// App represents the main TUI application and holds shared state.
//
// App 代表主TUI应用程序并持有共享状态。
type App struct {
	player           *audioPlayer
	mprisServer      *MPRISServer
	pages            []Page
	currentPageIndex int
	Playlist         []string
	LibraryPath      string      // Root path of the music library. / 音乐库的根路径。
	currentSongPath  string      // Path of the currently playing song. / 当前播放歌曲的路径。
	playMode         int         // Play mode: 0=repeat one, 1=repeat all, 2=random. / 播放模式: 0=单曲循环, 1=列表循环, 2=随机播放。
	volume           float64     // Saved volume setting. / 保存的音量设置。
	linearVolume     float64     // 0.0 to 1.0 linear volume for display. / 用于显示的线性音量（0.0到1.0）。
	playbackRate     float64     // Saved playback rate setting. / 保存的播放速度设置。
	actionQueue      chan func() // Action queue for thread-safe UI updates. / 用于线程安全UI更新的操作队列。
	sampleRate       beep.SampleRate

	// Play history. / 播放历史记录。
	playHistory         []string // Stores up to 100 played songs. / 存储最多100首播放过的歌曲。
	historyIndex        int      // Current position in the play history. / 在播放历史中的当前位置。
	isNavigatingHistory bool     // True if navigating through history. / 如果正在历史记录中导航，则为true。

	// Corrupted file tracking. / 损坏文件跟踪。
	corruptedFiles map[string]bool // Records corrupted FLAC files. / 记录损坏的FLAC文件。

	// Single song mode flag. / 单曲播放模式标志。
	isSingleSongMode bool // True if in single song playback mode. / 如果处于单曲播放模式，则为true。

	// Random mode transition tracking. / 随机模式切换跟踪。
	switchedToRandom bool // True if just switched to random mode and haven't played yet. / 如果刚切换到随机模式且尚未播放。
}

// Page defines the interface for a TUI page.
//
// Page 定义了TUI页面的接口。
type Page interface {
	Init()
	HandleKey(key rune) (Page, bool, error)
	HandleSignal(sig os.Signal) error
	View()
	Tick()
}

// switchToPage switches the application to the page at the given index.
//
// switchToPage 将应用程序切换到给定索引的页面。
func (a *App) switchToPage(index int) {
	if index >= 0 && index < len(a.pages) && index != a.currentPageIndex {
		a.currentPageIndex = index
		if GlobalConfig.App.DefaultPage == 3 {
			if err := SavePage(index); err != nil {
				l.Warnf("failed to save page: %v\n\n警告: 保存页面失败: %v", err, err)
			}
		}
		newPage := a.pages[a.currentPageIndex]
		fmt.Print("\x1b[2J\x1b[3J\x1b[H") // Clear screen completely
		newPage.Init()
		newPage.View()
	}
}

// PlaySong plays the specified song file.
//
// PlaySong 播放指定的歌曲文件。
func (a *App) PlaySong(songPath string) error {
	return a.PlaySongWithSwitch(songPath, true)
}

// PlaySongWithSwitch plays the specified song file, with an option to switch to the player page.
//
// PlaySongWithSwitch 播放指定的歌曲文件，并可选择是否跳转到播放页面。
func (a *App) PlaySongWithSwitch(songPath string, switchToPlayer bool) error {
	return a.PlaySongWithSwitchAndRender(songPath, switchToPlayer, true)
}

// PlaySongWithSwitchAndRender plays the specified song file, with options to switch to the player page and force a re-render.
//
// PlaySongWithSwitchAndRender 播放指定的歌曲文件，并可选择是否跳转到播放页面和是否强制重新渲染。
func (a *App) PlaySongWithSwitchAndRender(songPath string, switchToPlayer bool, forceRender bool) error {
	// Do nothing if it's the same song.
	// 如果是同一首歌，则不执行任何操作。
	if a.currentSongPath == songPath && a.player != nil {
		if switchToPlayer {
			a.switchToPage(0) // PlayerPage
		}
		return nil
	}

	// Stop current playback.
	// 停止当前播放。
	speaker.Lock()
	if a.player != nil {
		a.player.ctrl.Paused = true
	}
	speaker.Unlock()

	streamer, format, err := decodeAudioFile(songPath)
	if err != nil {
		a.MarkFileAsCorrupted(songPath)
		return fmt.Errorf("Failed to decode audio: %v\n\n解码音频失败: %v", err, err)
	}

	var playerPage *PlayerPage
	if page, ok := a.pages[0].(*PlayerPage); ok {
		playerPage = page
	}

	if err := speaker.ReInit(format.SampleRate, format.SampleRate.N(time.Second/30)); err != nil {
		streamer.Close()
		return fmt.Errorf("Failed to reinit speaker: %v\n\n重新初始化扬声器失败: %v", err, err)
	}

	audioStream := streamer

	player, err := newAudioPlayer(audioStream, format, a.volume, a.playbackRate)
	if err != nil {
		streamer.Close()
		return fmt.Errorf("Failed to create player: %v\n\n创建播放器失败: %v", err, err)
	}

	if a.mprisServer != nil {
		a.mprisServer.StopService()
	}
	mprisServer, err := NewMPRISServer(a, player, songPath)
	if err == nil {
		if err := mprisServer.Start(); err == nil {
			mprisServer.StartUpdateLoop()
			mprisServer.UpdatePlaybackStatus(true)
			mprisServer.UpdateMetadata()
		}
	}

	speaker.Lock()
	a.player = player
	a.mprisServer = mprisServer
	a.currentSongPath = songPath
	speaker.Unlock()

	a.addToPlayHistory(songPath)

	speaker.Play(a.player.volume)

	if switchToPlayer {
		a.currentPageIndex = 0 // Directly set the page index
		playerPage.UpdateSong(songPath)

		if forceRender {
			// This is for song changes during runtime.
			// Clear the screen and redraw the page.
			fmt.Print("\x1b[2J\x1b[3J\x1b[H")
			playerPage.Init()
			playerPage.View()
		}
		// If forceRender is false (autostart), do nothing more.
		// The initial render is handled by app.Run().
	} else {
		// When not switching to player page, just update the song path without rendering
		playerPage.UpdateSong(songPath)
	}

	return nil
}

// addToPlayHistory adds a song to the play history.
// Only records in random mode (playMode == 2).
// Ensures uniqueness: if the song already exists, removes the old entry first.
//
// addToPlayHistory 添加歌曲到播放历史记录。
// 仅在随机模式（playMode == 2）下记录。
// 确保唯一性：如果歌曲已存在，先删除旧记录。
func (a *App) addToPlayHistory(songPath string) {
	if a.playMode != 2 {
		a.isNavigatingHistory = false
		if err := SaveCurrentSong(songPath, a.LibraryPath); err != nil {
			l.Warnf("failed to save current song: %v\n\n警告: 保存当前歌曲失败: %v", err, err)
		}
		return
	}

	for i, song := range a.playHistory {
		if song == songPath {
			a.playHistory = append(a.playHistory[:i], a.playHistory[i+1:]...)
			if i < a.historyIndex {
				a.historyIndex--
			} else if i == a.historyIndex {
				a.historyIndex = len(a.playHistory) - 1
			}
			break
		}
	}

	if a.historyIndex < len(a.playHistory)-1 {
		a.playHistory = a.playHistory[:a.historyIndex+1]
	}

	a.playHistory = append(a.playHistory, songPath)

	effectiveSize := a.getEffectiveHistorySize()
	if len(a.playHistory) > effectiveSize {
		a.playHistory = a.playHistory[1:]
	}

	a.historyIndex = len(a.playHistory) - 1
	a.isNavigatingHistory = false

	// Save both play history and current song
	if err := SavePlayHistory(a.playHistory, a.LibraryPath); err != nil {
		l.Warnf("failed to save play history: %v\n\n警告: 保存播放历史失败: %v", err, err)
	}
	if err := SaveCurrentSong(songPath, a.LibraryPath); err != nil {
		l.Warnf("failed to save current song: %v\n\n警告: 保存当前歌曲失败: %v", err, err)
	}
}

// removeFromPlayHistory removes all occurrences of a song from the play history.
//
// removeFromPlayHistory 从播放历史中删除指定歌曲的所有记录。
func (a *App) removeFromPlayHistory(songPath string) {
	newHistory := make([]string, 0, len(a.playHistory))
	newIndex := a.historyIndex
	removedBefore := 0

	for i, song := range a.playHistory {
		if song == songPath {
			if i < a.historyIndex {
				removedBefore++
			}
			continue
		}
		newHistory = append(newHistory, song)
	}

	if removedBefore > 0 {
		newIndex = a.historyIndex - removedBefore
		if newIndex < 0 {
			newIndex = 0
		}
	}
	if newIndex >= len(newHistory) {
		newIndex = len(newHistory) - 1
	}

	a.playHistory = newHistory
	a.historyIndex = newIndex

	if err := SavePlayHistory(a.playHistory, a.LibraryPath); err != nil {
		l.Warnf("failed to save play history: %v\n\n警告: 保存播放历史失败: %v", err, err)
	}
}

// recordCurrentSongToHistory records the current song to play history.
// Used when switching to random mode or exiting while in random mode.
//
// recordCurrentSongToHistory 记录当前歌曲到播放历史。
// 用于切换到随机模式或在随机模式下退出时。
func (a *App) recordCurrentSongToHistory() {
	if a.currentSongPath == "" {
		return
	}

	// Remove existing entry if present
	for i, song := range a.playHistory {
		if song == a.currentSongPath {
			a.playHistory = append(a.playHistory[:i], a.playHistory[i+1:]...)
			if i < a.historyIndex {
				a.historyIndex--
			} else if i == a.historyIndex {
				a.historyIndex = len(a.playHistory) - 1
			}
			break
		}
	}

	a.playHistory = append(a.playHistory, a.currentSongPath)

	effectiveSize := a.getEffectiveHistorySize()
	if len(a.playHistory) > effectiveSize {
		a.playHistory = a.playHistory[1:]
	}

	a.historyIndex = len(a.playHistory) - 1

	if err := SavePlayHistory(a.playHistory, a.LibraryPath); err != nil {
		l.Warnf("failed to save play history: %v\n\n警告: 保存播放历史失败: %v", err, err)
	}
}

// getEffectiveHistorySize returns the effective history size limit.
// It is the minimum of MaxHistorySize and the playlist length.
//
// getEffectiveHistorySize 返回有效的历史记录大小限制。
// 它是 MaxHistorySize 和播放列表长度的最小值。
func (a *App) getEffectiveHistorySize() int {
	playlistLen := len(a.Playlist)
	if playlistLen == 0 {
		return GlobalConfig.App.MaxHistorySize
	}
	if playlistLen < GlobalConfig.App.MaxHistorySize {
		return playlistLen
	}
	return GlobalConfig.App.MaxHistorySize
}

// setCurrentSong updates the current song path and saves it to storage.
//
// setCurrentSong 更新当前歌曲路径并保存到存储。
func (a *App) setCurrentSong(songPath string) {
	a.currentSongPath = songPath
	if err := SaveCurrentSong(songPath, a.LibraryPath); err != nil {
		l.Warnf("failed to save current song: %v\n\n警告: 保存当前歌曲失败: %v", err, err)
	}
}

// sanitizePlayHistory cleans up the play history at startup:
// 1. Removes songs not in the playlist
// 2. Removes duplicate entries (keeps the newest)
// 3. Trims history from the oldest if it exceeds the limit
//
// sanitizePlayHistory 在启动时清理播放历史：
// 1. 删除不在播放列表中的歌曲
// 2. 删除重复项（保留最新的）
// 3. 如果超出限制，从最旧的开始删除
func sanitizePlayHistory(history []string, playlist []string) []string {
	if len(history) == 0 {
		return history
	}

	playlistSet := make(map[string]bool, len(playlist))
	for _, song := range playlist {
		playlistSet[song] = true
	}

	seen := make(map[string]bool)
	cleaned := make([]string, 0, len(history))
	for _, song := range slices.Backward(history) {
		if !playlistSet[song] {
			continue
		}
		if seen[song] {
			continue
		}
		seen[song] = true
		cleaned = append(cleaned, song)
	}

	for i, j := 0, len(cleaned)-1; i < j; i, j = i+1, j-1 {
		cleaned[i], cleaned[j] = cleaned[j], cleaned[i]
	}

	effectiveSize := GlobalConfig.App.MaxHistorySize
	if len(playlist) > 0 && len(playlist) < effectiveSize {
		effectiveSize = len(playlist)
	}
	if len(cleaned) > effectiveSize {
		cleaned = cleaned[len(cleaned)-effectiveSize:]
	}

	return cleaned
}

// NextSong switches to the next song.
//
// NextSong 切换到下一首歌曲。
func (a *App) NextSong() {
	a.actionQueue <- func() {
		if playerPage, ok := a.pages[0].(*PlayerPage); ok {
			playerPage.playNextSong()
		}
	}
}

// PreviousSong switches to the previous song.
//
// PreviousSong 切换到上一首歌曲。
func (a *App) PreviousSong() {
	a.actionQueue <- func() {
		if playerPage, ok := a.pages[0].(*PlayerPage); ok {
			playerPage.playPreviousSong()
		}
	}
}

// SaveSettings saves the current volume and playback rate to the storage file.
//
// SaveSettings 将当前的音量和播放速度保存到存储文件。
func (a *App) SaveSettings() {
	if !GlobalConfig.App.RememberVolume && !GlobalConfig.App.RememberPlaybackRate {
		return
	}

	storageData, err := loadStorageData()
	if err != nil {
		l.Warnf("could not load storage data to save settings: %v\n\n无法加载存储数据以保存设置: %v", err, err)
		return
	}

	if GlobalConfig.App.RememberVolume {
		roundedVolume := math.Round(a.linearVolume*100) / 100
		storageData.Volume = &roundedVolume
	}
	if GlobalConfig.App.RememberPlaybackRate {
		roundedPlaybackRate := math.Round(a.playbackRate*100) / 100
		storageData.PlaybackRate = &roundedPlaybackRate
	}

	if err := saveStorageData(storageData); err != nil {
		l.Warnf("could not save settings to storage: %v\n\n无法保存设置到存储: %v", err, err)
	}
}

// Run starts the application's main event loop.
//
// Run 启动应用程序的主事件循环。
func (a *App) Run() error {
	// Screen is now cleared and cursor is handled in main
	// 屏幕清理和光标处理现在在 main 函数中进行

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH, syscall.SIGINT)
	defer signal.Stop(sigCh)

	keyCh := make(chan rune)
	go func() {
		// This goroutine reads runes and sends them to a channel,
		// decoupling raw input reading from the logic of parsing escape sequences.
		//
		// 这个goroutine读取rune并将其发送到一个通道，
		// 将原始输入读取与解析转义序列的逻辑解耦。
		keys := make(chan rune)
		go func() {
			reader := bufio.NewReader(os.Stdin)
			for {
				r, _, err := reader.ReadRune()
				if err != nil {
					close(keys)
					return
				}
				keys <- r
			}
		}()

		for {
			r, ok := <-keys
			if !ok {
				return
			}

			// If it's not an escape character, process it directly.
			// 如果不是转义字符，直接处理。
			if r != '\x1b' {
				switch r {
				case '\r', '\n':
					keyCh <- KeyEnter
				case 8, 127:
					keyCh <- KeyBackspace
				default:
					keyCh <- r
				}
				continue
			}

			// It's an escape character. Use a timeout to check for more characters.
			// 是一个转义字符。使用超时来检查是否还有更多字符。
			select {
			case nextRune := <-keys:
				if nextRune == '[' {
					consumeCSISequence(keys, keyCh)
				} else {
					// It's an Alt+key sequence. Set the high bit to encode the Alt modifier.
					// 这是Alt+键序列。设置高位来编码Alt修饰符。
					if nextRune > 0 && nextRune < 0x80 {
						keyCh <- nextRune | 0x80
					} else {
						keyCh <- nextRune
					}
				}
			case <-time.After(25 * time.Millisecond):
				// Standalone ESC press.
				// 单独的ESC键按下。
				keyCh <- r
			}
		}
	}()

	ticker := time.NewTicker(time.Second / 2)
	defer ticker.Stop()

	fmt.Print("\x1b[2J\x1b[3J\x1b[H")
	a.pages[a.currentPageIndex].Init()
	a.pages[a.currentPageIndex].View()

	for {
		currentPage := a.pages[a.currentPageIndex]
		select {
		case action := <-a.actionQueue:
			action()

		case key := <-keyCh:
			if IsKey(key, GlobalConfig.Keymap.Global.Quit) {
				if isInSearchMode(currentPage) {
					_, needsRedraw, err := currentPage.HandleKey(key)
					if err != nil {
						return nil
					}
					if needsRedraw {
						currentPage.View()
					}
				} else {
					if a.switchedToRandom {
						a.recordCurrentSongToHistory()
					}
					return nil
				}
			} else if isActivelySearching(currentPage) {
				// In search mode, pass all keys to the page's handler first.
				// 在搜索模式下，优先将所有按键传递给页面的处理器。
				_, needsRedraw, err := currentPage.HandleKey(key)
				if err != nil {
					return nil
				}
				if needsRedraw {
					currentPage.View()
				}
			} else if IsKey(key, GlobalConfig.Keymap.Global.CyclePages) {
				a.switchToPage((a.currentPageIndex + 1) % len(a.pages))
			} else if IsKey(key, GlobalConfig.Keymap.Global.SwitchToPlayer) {
				a.switchToPage(0) // PlayerPage
			} else if IsKey(key, GlobalConfig.Keymap.Global.SwitchToPlayList) {
				a.switchToPage(1) // PlayListPage
			} else if IsKey(key, GlobalConfig.Keymap.Global.SwitchToLibrary) {
				a.switchToPage(2) // LibraryPage
			} else {
				_, needsRedraw, err := currentPage.HandleKey(key)
				if err != nil {
					return nil
				}
				if needsRedraw {
					currentPage.View()
				}
			}

		case sig := <-sigCh:
			if sig == syscall.SIGINT {
				if a.switchedToRandom {
					a.recordCurrentSongToHistory()
				}
				return nil
			}
			if err := currentPage.HandleSignal(sig); err != nil {
				return err
			}

		case <-ticker.C:
			currentPage.Tick()
		}
	}
}

// isActivelySearching checks if the user is currently typing in a search prompt.
//
// isActivelySearching 检查用户当前是否正在输入搜索提示。
func isActivelySearching(page Page) bool {
	if lib, ok := page.(*Library); ok {
		return lib.isSearching
	}
	if pl, ok := page.(*PlayList); ok {
		return pl.isSearching
	}
	return false
}

// isInSearchMode checks if the current page is in search mode.
//
// isInSearchMode 检查当前页面是否处于搜索模式。
func isInSearchMode(page Page) bool {
	if lib, ok := page.(*Library); ok {
		return lib.isSearching || lib.searchQuery != ""
	}
	if pl, ok := page.(*PlayList); ok {
		return pl.isSearching || pl.searchQuery != ""
	}
	return false
}

func main() {
	if os.Getenv("TMUX") != "" || os.Getenv("ZELLIJ") != "" {
		l.Fatalf("BM does not support running inside tmux or zellij\n\nBM 不支持在tmux或zellij里运行")
	}

	if len(os.Args) >= 2 {
		arg := os.Args[1]
		if arg == "help" || arg == "-h" || arg == "-help" || arg == "--help" {
			displayHelp()
			return
		}

		info, err := os.Stat(arg)
		if err == nil && !info.IsDir() {
			ext := filepath.Ext(arg)
			ext = strings.ToLower(ext)
			if ext == ".flac" || ext == ".mp3" || ext == ".wav" || ext == ".ogg" {
				fmt.Print("\x1b[?1049h\x1b[?25l")
				defer fmt.Print("\x1b[2J\x1b[?1049l\x1b[?25h")

				oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
				if err != nil {
					l.Fatalf("failed to set raw mode: %v\n\n设置原始模式失败: %v", err, err)
				}
				defer term.Restore(int(os.Stdin.Fd()), oldState)

				if err := runSingleSong(arg); err != nil {
					l.Fatalf("failed to play single song: %v\n\n播放单曲失败: %v", err, err)
				}
				return
			}
		}
	}

	dirPath, err := validateInputsAndConfig()
	if err != nil {
		l.Fatalf("%v", err)
	}

	fmt.Print("\x1b[?1049h\x1b[?25l")
	defer fmt.Print("\x1b[2J\x1b[?1049l\x1b[?25h")

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		l.Fatalf("failed to set raw mode: %v\n\n设置原始模式失败: %v", err, err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	if err := runApplication(dirPath); err != nil {
		l.Fatalf("%v", err)
	}
}

func validateInputsAndConfig() (string, error) {
	if err := LoadConfig(); err != nil {
		return "", fmt.Errorf("Failed to load config: %v\n\n加载配置失败: %v", err, err)
	}

	if GlobalConfig.App.PlaylistHistory && !GlobalConfig.App.RememberLibraryPath {
		return "", fmt.Errorf("Configuration error: 'playlist_history' cannot be true when 'remember_library_path' is false\n\n配置错误: 'playlist_history' 为 true 时 'remember_library_path' 不能为 false")
	}

	if GlobalConfig.App.AutostartLastPlayed && !GlobalConfig.App.PlaylistHistory {
		return "", fmt.Errorf("Configuration error: 'autostart_last_played' cannot be true when 'playlist_history' is false\n\n配置错误: 'autostart_last_played' 为 true 时 'playlist_history' 不能为 false")
	}

	var dirPath string
	if len(os.Args) >= 2 {
		dirPath = os.Args[1]
	}

	if GlobalConfig.App.RememberLibraryPath && dirPath == "" {
		storageData, err := loadStorageData()
		if err != nil {
			return "", fmt.Errorf("Error loading storage data: %v\n\n加载存储数据时出错: %v", err, err)
		}
		if storageData.LibraryPath == "" {
			return "", fmt.Errorf("`remember_library_path` is enabled but no path has been saved yet.\nPlease provide a directory path once for future use.\n\nUsage: %s <music_directory>\n\n`remember_library_path` 已启用，但尚未保存任何路径。\n请提供一次目录路径以便将来使用。 \n\n用法: %s <music_directory>", os.Args[0], os.Args[0])
		}
		dirPath = storageData.LibraryPath
	}

	if !GlobalConfig.App.RememberLibraryPath && dirPath == "" {
		return "", fmt.Errorf("Please enter a music directory path.\nIf you want the application to remember the path, set `remember_library_path = true` in the config file.\n\nUsage: %s <music_directory>\n\n请输入音乐目录路径。\n如果希望应用记住该路径，请在配置文件中设置 `remember_library_path = true`。\n\n用法: %s <music_directory>", os.Args[0], os.Args[0])
	}

	info, err := os.Stat(dirPath)
	if err != nil {
		return "", fmt.Errorf("Unable to access path: %v\n\n无法访问路径: %v", err, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("Input path must be a directory, not a file\n\n输入路径必须是目录，而不是文件")
	}

	if GlobalConfig.App.RememberLibraryPath {
		absPath, err := filepath.Abs(dirPath)
		if err != nil {
			l.Warnf("Unable to get absolute path: %v\n\n警告: 无法获取绝对路径: %v", err, err)
		} else {
			if err := SaveLibraryPath(absPath); err != nil {
				l.Warnf("Unable to save music library path: %v\n\n警告: 无法保存音乐库路径: %v", err, err)
			}
		}
	}

	return dirPath, nil
}

func runApplication(dirPath string) error {
	storageData, err := loadStorageData()
	if err != nil {
		return fmt.Errorf("Error loading storage data: %v\n\n加载存储数据时出错: %v", err, err)
	}

	cellW, cellH, err := getCellSize()
	if err != nil {
		return fmt.Errorf("Unable to get terminal cell size: %v\n\n无法获取终端单元格尺寸: %v", err, err)
	}

	sampleRate := beep.SampleRate(44100)
	speaker.Init(sampleRate, sampleRate.N(time.Second/30))

	playlist, err := LoadPlaylist(dirPath)
	if err != nil {
		l.Warnf("Could not load playlist: %v\n\n警告: 无法加载播放列表: %v", err, err)
		playlist = make([]string, 0)
	}

	playHistory, err := LoadPlayHistory(dirPath)
	if err != nil {
		l.Warnf("Could not load play history: %v\n\n警告: 无法加载播放历史: %v", err, err)
		playHistory = make([]string, 0)
	}

	playHistory = sanitizePlayHistory(playHistory, playlist)
	if err := SavePlayHistory(playHistory, dirPath); err != nil {
		l.Warnf("Could not save sanitized play history: %v\n\n警告: 无法保存清理后的播放历史: %v", err, err)
	}

	app := &App{
		player:              nil,
		mprisServer:         nil,
		currentPageIndex:    0,
		Playlist:            playlist,
		LibraryPath:         dirPath,
		playMode:            GlobalConfig.App.DefaultPlayMode,
		volume:              0,
		linearVolume:        1.0,
		playbackRate:        1.0,
		actionQueue:         make(chan func(), 10),
		sampleRate:          sampleRate,
		playHistory:         playHistory,
		historyIndex:        len(playHistory) - 1,
		isNavigatingHistory: false,
		corruptedFiles:      make(map[string]bool),
		isSingleSongMode:    false,
		switchedToRandom:    false,
	}

	if GlobalConfig.App.DefaultPage == 3 {
		savedPage, err := LoadPage()
		if err != nil {
			l.Warnf("Could not load saved page: %v\n\n无法加载已保存的页面: %v", err, err)
			app.currentPageIndex = 0
		} else {
			app.currentPageIndex = savedPage
		}
	} else {
		app.currentPageIndex = GlobalConfig.App.DefaultPage
	}

	if GlobalConfig.App.RememberVolume && storageData.Volume != nil {
		app.linearVolume = *storageData.Volume
		if app.linearVolume == 0 {
			app.volume = -10
		} else {
			app.volume = math.Log2(app.linearVolume)
		}
	}

	if GlobalConfig.App.RememberPlaybackRate && storageData.PlaybackRate != nil {
		app.playbackRate = *storageData.PlaybackRate
	}

	// Load saved play mode
	// If default play mode is 3 (memory), use saved play mode
	savedPlayMode, err := LoadPlayMode()
	if err != nil {
		l.Warnf("Could not load saved play mode: %v\n\n无法加载已保存的播放模式: %v", err, err)
	} else if GlobalConfig.App.DefaultPlayMode == 3 {
		app.playMode = savedPlayMode
	}

	playerPage := NewPlayerPage(app, "", cellW, cellH)
	playListPage := NewPlayList(app)
	libraryPage := NewLibraryWithPath(app, dirPath)
	app.pages = []Page{playerPage, playListPage, libraryPage}

	if GlobalConfig.App.AutostartLastPlayed {
		currentSong, err := LoadCurrentSong(dirPath)
		if err != nil {
			l.Warnf("Could not load current song: %v\n\n无法加载当前歌曲: %v", err, err)
		}

		var songToPlay string
		if currentSong != "" {
			songToPlay = currentSong
		} else if len(app.playHistory) > 0 {
			songToPlay = app.playHistory[len(app.playHistory)-1]
		}

		if songToPlay != "" {
			if !songExistsInPlaylist(songToPlay, app.Playlist) {
				l.Warnf("Last played song not found in playlist: %s\n\n上次播放的歌曲未在播放列表中找到: %s", songToPlay, songToPlay)
				app.setCurrentSong("")
				if playerPage, ok := app.pages[0].(*PlayerPage); ok {
					playerPage.UpdateSong("")
				}
			} else {
				switchToPlayer := app.currentPageIndex == 0
				err := app.PlaySongWithSwitchAndRender(songToPlay, switchToPlayer, false)
				if err != nil {
					l.Warnf("Could not autostart last played song: %v\n\n无法自动启动上次播放的歌曲: %v", err, err)
				}
			}
		}
	}

	return app.Run()
}

// runSingleSong runs the application in single song playback mode.
//
// runSingleSong 以单曲播放模式运行应用程序。
func runSingleSong(songPath string) error {
	info, err := os.Stat(songPath)
	if err != nil {
		return fmt.Errorf("Unable to access file: %v\n\n无法访问文件: %v", err, err)
	}
	if info.IsDir() {
		return fmt.Errorf("Input must be an audio file, not a directory.\n\n输入必须是音频文件，而不是目录。")
	}

	absPath, err := filepath.Abs(songPath)
	if err != nil {
		return fmt.Errorf("Unable to get absolute path: %v\n\n无法获取绝对路径: %v", err, err)
	}

	if err := loadMinimalConfig(); err != nil {
		return fmt.Errorf("Failed to load minimal config: %v\n\n加载最小配置失败: %v", err, err)
	}

	cellW, cellH, err := getCellSize()
	if err != nil {
		return fmt.Errorf("Unable to get terminal cell size: %v\n\n无法获取终端单元格尺寸: %v", err, err)
	}

	sampleRate := beep.SampleRate(44100)
	speaker.Init(sampleRate, sampleRate.N(time.Second/30))

	app := &App{
		player:              nil,
		mprisServer:         nil,
		currentPageIndex:    0,
		Playlist:            []string{absPath},
		LibraryPath:         filepath.Dir(absPath),
		playMode:            0,
		volume:              0,
		linearVolume:        1.0,
		playbackRate:        1.0,
		actionQueue:         make(chan func(), 10),
		sampleRate:          sampleRate,
		playHistory:         make([]string, 0),
		historyIndex:        -1,
		isNavigatingHistory: false,
		corruptedFiles:      make(map[string]bool),
		isSingleSongMode:    true,
	}

	playerPage := NewPlayerPage(app, "", cellW, cellH)
	app.pages = []Page{playerPage}

	if err := app.PlaySongWithSwitchAndRender(absPath, true, false); err != nil {
		return fmt.Errorf("Failed to play song: %v\n\n播放歌曲失败: %v", err, err)
	}

	return app.Run()
}

// loadMinimalConfig loads minimal configuration for single song mode.
//
// loadMinimalConfig 为单曲播放模式加载最小配置。
func loadMinimalConfig() error {
	GlobalConfig = &Config{
		Keymap: Keymap{
			Global: GlobalKeymap{
				Quit:             Key{"esc"},
				CyclePages:       Key{"tab"},
				SwitchToPlayer:   Key{"1"},
				SwitchToPlayList: Key{"2"},
				SwitchToLibrary:  Key{"3"},
			},
			Player: PlayerKeymap{
				TogglePause:     Key{"space"},
				SeekForward:     Key{"e", "l"},
				SeekBackward:    Key{"q", "h"},
				VolumeUp:        Key{"w", "up"},
				VolumeDown:      Key{"s", "down"},
				RateUp:          Key{"x", "k"},
				RateDown:        Key{"z", "j"},
				NextSong:        Key{"d", "right"},
				PrevSong:        Key{"a", "left"},
				TogglePlayMode:  Key{"r"},
				ToggleTextColor: Key{"c"},
				Reset:           Key{"backspace"},
			},
			Library: LibraryKeymap{
				NavUp:           Key{"k", "w", "up"},
				NavDown:         Key{"j", "s", "down"},
				NavEnterDir:     Key{"l", "d", "right"},
				NavExitDir:      Key{"h", "a", "left"},
				ToggleSelect:    Key{"space"},
				ToggleSelectAll: Key{"e"},
				Search:          Key{"/", "f"},
				SearchMode: SearchModeKeymap{
					ConfirmSearch:   Key{"enter"},
					EscapeSearch:    Key{"esc"},
					SearchBackspace: Key{"backspace"},
				},
			},
			Playlist: PlaylistKeymap{
				NavUp:      Key{"k", "w", "up"},
				NavDown:    Key{"j", "s", "down"},
				RemoveSong: Key{"space"},
				PlaySong:   Key{"enter"},
				Search:     Key{"/", "f"},
				SearchMode: SearchModeKeymap{
					ConfirmSearch:   Key{"enter"},
					EscapeSearch:    Key{"esc"},
					SearchBackspace: Key{"backspace"},
				},
			},
		},
		App: AppConfig{
			MaxHistorySize:       100,
			SwitchDebounceMs:     1000,
			DefaultPage:          0,
			DefaultPlayMode:      0,
			RememberLibraryPath:  false,
			PlaylistHistory:      false,
			AutostartLastPlayed:  false,
			RememberVolume:       false,
			RememberPlaybackRate: false,
			DefaultColorR:        100,
			DefaultColorG:        149,
			DefaultColorB:        237,
			ImageProtocol:        "auto",
			EnableNotifications:  false,
			LibraryPath:          "",
			Storage:              "",
			DefaultCoverPath:     "",
			EnableFolderCovers:   true,
			MaxSearchDirs:        15,
		},
	}

	resolveIconSet(GlobalConfig)

	return nil
}

// MarkFileAsCorrupted marks a file as corrupted.
//
// MarkFileAsCorrupted 标记一个文件为已损坏。
func (a *App) MarkFileAsCorrupted(filePath string) {
	a.corruptedFiles[filePath] = true
}

// IsFileCorrupted checks if a file is marked as corrupted.
//
// IsFileCorrupted 检查一个文件是否被标记为已损坏。
func (a *App) IsFileCorrupted(filePath string) bool {
	return a.corruptedFiles[filePath]
}

func displayHelp() {
	const (
		reset   = "\033[0m"
		bold    = "\033[1m"
		cyan    = "\033[36m"
		green   = "\033[32m"
		yellow  = "\033[33m"
		magenta = "\033[35m"
	)

	fmt.Println()
	fmt.Println(bold + "BM Terminal Music Player" + reset)
	fmt.Println()
	fmt.Println(bold + "USAGE:" + reset)
	fmt.Println("  " + cyan + "bm" + reset + " [COMMANDS]")
	fmt.Println()
	fmt.Println(bold + "COMMANDS:" + reset)
	fmt.Println("  " + green + "bm" + reset + "                          Start player with interactive library selection")
	fmt.Println("  " + green + "bm <directory>" + reset + "              Start player with specified music library")
	fmt.Println("  " + green + "bm <audio-file>" + reset + "             Play single audio file")
	fmt.Println("  " + green + "bm help, -h, -help, --help" + reset + "  Show this help message")
	fmt.Println()
	fmt.Println(bold + "SUPPORTED FORMATS:" + reset)
	fmt.Println("  " + yellow + "FLAC, MP3, WAV, OGG" + reset)
	fmt.Println()
	fmt.Println(bold + "CONFIGURATION:" + reset)
	fmt.Println("  Configuration file: " + cyan + "~/.config/BM/config.toml" + reset)
	fmt.Println()
}
