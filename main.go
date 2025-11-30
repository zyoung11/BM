package main

import (
	"bufio"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	"unicode"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/flac"
	"github.com/gopxl/beep/v2/speaker"
	"golang.org/x/term"
)

// fuzzyMatch performs a case-insensitive fuzzy search with Unicode support.
// It returns a score indicating the quality of the match (higher is better), or 0 if no match is found.
//
// fuzzyMatch 函数执行一个不区分大小写的、支持Unicode的模糊搜索。
// 它返回一个表示匹配质量的分数（越高越好），如果没有找到匹配项则返回0。
func fuzzyMatch(query, text string) int {
	queryRunes := []rune(query)
	textRunes := []rune(text)

	if len(queryRunes) == 0 {
		return 100
	}

	queryIdx := 0
	firstMatchIndex := -1
	lastMatchIndex := -1
	consecutiveMatches := 0
	maxConsecutive := 0

	for i, textRune := range textRunes {
		if unicodeFold(textRune) == unicodeFold(queryRunes[queryIdx]) {
			if firstMatchIndex == -1 {
				firstMatchIndex = i
			}
			lastMatchIndex = i

			consecutiveMatches++
			if consecutiveMatches > maxConsecutive {
				maxConsecutive = consecutiveMatches
			}

			queryIdx++
			if queryIdx == len(queryRunes) {
				break
			}
		} else {
			consecutiveMatches = 0
		}
	}

	if queryIdx < len(queryRunes) {
		return 0
	}

	score := 100

	matchSpread := lastMatchIndex - firstMatchIndex
	if matchSpread > 0 {
		spreadPenalty := (matchSpread * 10) / len(textRunes)
		score -= spreadPenalty
	}

	if maxConsecutive > 1 {
		consecutiveBonus := maxConsecutive * 5
		score += consecutiveBonus
	}

	if firstMatchIndex == 0 {
		score += 20
	}

	if len(textRunes) < 50 {
		score += (50 - len(textRunes)) / 5
	}

	if score < 1 {
		score = 1
	}

	return score
}

// unicodeFold performs Unicode-aware case folding for case-insensitive comparison.
//
// unicodeFold 函数执行支持Unicode的大小写折叠，用于不区分大小写的比较。
func unicodeFold(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return unicode.ToLower(r)
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

	f, err := os.Open(songPath)
	if err != nil {
		return fmt.Errorf("failed to open file: %v\n\n打开文件失败: %v", err, err)
	}

	streamer, format, err := flac.Decode(f)
	if err != nil {
		f.Close()
		a.MarkFileAsCorrupted(songPath)
		return fmt.Errorf("解码FLAC失败: %v", err)
	}

	var playerPage *PlayerPage
	if page, ok := a.pages[0].(*PlayerPage); ok {
		playerPage = page
	}

	// Resample if necessary and use a buffer to create a seekable stream (StreamSeeker).
	// 如果需要，进行重采样，并使用缓冲区创建一个可跳转的流 (StreamSeeker)。
	var audioStream beep.StreamSeeker = streamer
	if format.SampleRate != a.sampleRate {
		if playerPage != nil {
			playerPage.resampleDisplayTimer = 10 // Show for 10 ticks (about 5s) / 显示10个tick周期（约5秒）
			// Force immediate UI update to show resampling indicator only if we're on player page and not during initial startup
			// 只有在播放页面且不是初始启动时才强制立即更新UI以显示重采样指示器
			if a.currentPageIndex == 0 && playerPage.flacPath != "" {
				playerPage.updateStatus()
			}
		}

		// Use high-quality resampling with go-audio-resampler (最高质量)
		resampledStream, err := highQualityResample(streamer, format.SampleRate, a.sampleRate)
		if err != nil {
			f.Close()
			return fmt.Errorf("高质量重采样失败: %v", err)
		}
		audioStream = resampledStream
	}

	player, err := newAudioPlayer(audioStream, format, a.volume, a.playbackRate)
	if err != nil {
		f.Close()
		return fmt.Errorf("创建播放器失败: %v", err)
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

	if playerPage != nil {
		playerPage.resampleDisplayTimer = 0
	}

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
//
// addToPlayHistory 添加歌曲到播放历史记录。
func (a *App) addToPlayHistory(songPath string) {
	if a.historyIndex < len(a.playHistory)-1 {
		a.playHistory = a.playHistory[:a.historyIndex+1]
	}

	a.playHistory = append(a.playHistory, songPath)

	if len(a.playHistory) > GlobalConfig.App.MaxHistorySize {
		a.playHistory = a.playHistory[1:]
	}

	a.historyIndex = len(a.playHistory) - 1
	a.isNavigatingHistory = false

	// Save both play history and current song
	if err := SavePlayHistory(a.playHistory, a.LibraryPath); err != nil {
		log.Printf("Warning: failed to save play history: %v\n\n警告: 保存播放历史失败: %v", err, err)
	}
	if err := SaveCurrentSong(songPath, a.LibraryPath); err != nil {
		log.Printf("Warning: failed to save current song: %v\n\n警告: 保存当前歌曲失败: %v", err, err)
	}
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
		log.Printf("Warning: could not load storage data to save settings: %v", err)
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
		log.Printf("Warning: could not save settings to storage: %v", err)
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
					// This is likely an arrow key sequence.
					// 这可能是一个方向键序列。
					select {
					case finalRune := <-keys:
						switch finalRune {
						case 'A':
							keyCh <- KeyArrowUp
						case 'B':
							keyCh <- KeyArrowDown
						case 'C':
							keyCh <- KeyArrowRight
						case 'D':
							keyCh <- KeyArrowLeft
						default:
							keyCh <- r
						}
					case <-time.After(25 * time.Millisecond):
						keyCh <- r
					}
				} else {
					// It's another sequence, like Alt+key. Treat as two separate key presses.
					// 这是其他序列，如Alt+键。视为两次单独的按键。
					keyCh <- r
					keyCh <- nextRune
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

	// Initial view rendering
	fmt.Print("\x1b[2J\x1b[3J\x1b[H") // Clear screen before first draw
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
					return nil // Exit application. / 退出应用。
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
					return nil // Assume any error from HandleKey means quit. / 假设任何来自HandleKey的错误都意味着退出。
				}
				if needsRedraw {
					currentPage.View()
				}
			}

		case sig := <-sigCh:
			if sig == syscall.SIGINT {
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
	// Check for help command first, before any terminal setup
	if len(os.Args) >= 2 {
		arg := os.Args[1]
		if arg == "help" || arg == "-help" || arg == "--help" {
			displayHelp()
			return
		}
	}

	// Check configuration and path requirements BEFORE terminal setup
	if err := LoadConfig(); err != nil {
		log.Fatalf("Error loading configuration: %v\n\n错误: 加载配置失败: %v", err, err)
	}

	if GlobalConfig.App.PlaylistHistory && !GlobalConfig.App.RememberLibraryPath {
		log.Fatalf("Configuration error: 'playlist_history' cannot be true if 'remember_library_path' is false.\n\n配置错误: 'playlist_history' 为 true 时 'remember_library_path' 不能为 false。")
	}

	// Check if remember_library_path is enabled but no path is saved
	if GlobalConfig.App.RememberLibraryPath && len(os.Args) < 2 {
		storageData, err := loadStorageData()
		if err != nil {
			log.Fatalf("Error loading storage data: %v\n\n加载存储数据时出错: %v", err, err)
		}
		if storageData.LibraryPath == "" {
			log.Fatalf("`remember_library_path` is enabled, but no path is saved yet.\nPlease run with a directory path once to save it for future use.\n\n`remember_library_path` 已启用，但尚未保存任何路径。\n请提供一次目录路径以便将来使用。 \n\nUsage: %s <music_directory>", os.Args[0])
		}
	}

	// Check if no path is provided and remember_library_path is disabled
	if !GlobalConfig.App.RememberLibraryPath && len(os.Args) < 2 {
		log.Fatalf("Please provide a music directory path.\nTo have the app remember the path for future sessions, set `remember_library_path = true` in the config file.\n\n请输入音乐目录路径。\n如果希望应用记住该路径，请在配置文件中设置 `remember_library_path = true`。\n\nUsage: %s <music_directory>", os.Args[0])
	}

	// --- Terminal Setup ---
	fmt.Print("\x1b[?1049h\x1b[?25l")
	defer fmt.Print("\x1b[2J\x1b[?1049l\x1b[?25h") // Clear screen and restore on exit

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		log.Fatalf("Failed to set raw mode: %v\n\n设置原始模式失败: %v", err, err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// --- App Logic ---
	if err := runApplication(); err != nil {
		// The deferred statements above will handle cleanup
		log.Fatalf("Application runtime error: %v\n\n应用运行时出现错误: %v", err, err)
	}
}

func runApplication() error {
	var dirPath string
	storageData, err := loadStorageData()
	if err != nil {
		return fmt.Errorf("Error loading storage data: %v\n\n加载存储数据时出错: %v", err, err)
	}

	if len(os.Args) >= 2 {
		dirPath = os.Args[1]
		info, err := os.Stat(dirPath)
		if err != nil {
			return fmt.Errorf("Unable to access path: %v\n\n无法访问路径: %v", err, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("Input path must be a directory, not a file.\n\n输入路径必须是目录，而不是文件。")
		}

		if GlobalConfig.App.RememberLibraryPath {
			absPath, err := filepath.Abs(dirPath)
			if err != nil {
				log.Printf("Warning: Unable to get absolute path: %v\n\n警告: 无法获取绝对路径: %v", err, err)
			} else {
				if err := SaveLibraryPath(absPath); err != nil {
					log.Printf("Warning: Unable to save music library path: %v\n\n警告: 无法保存音乐库路径: %v", err, err)
				}
			}
		}
	} else {
		// This branch should only be reached if remember_library_path is true and path exists
		// 这个分支应该只在 remember_library_path 为 true 且路径存在时到达
		dirPath = storageData.LibraryPath
		if _, err := os.Stat(dirPath); err != nil {
			return fmt.Errorf("The saved music library path is invalid or no longer exists: %s\n\n保存的音乐库路径无效或不存在: %s", dirPath, dirPath)
		}
	}

	cellW, cellH, err := getCellSize()
	if err != nil {
		return fmt.Errorf("Unable to get terminal cell size: %v\n\n无法获取终端单元格尺寸: %v", err, err)
	}

	sampleRate := beep.SampleRate(GlobalConfig.App.TargetSampleRate)
	speaker.Init(sampleRate, sampleRate.N(time.Second/30))

	playlist, err := LoadPlaylist(dirPath)
	if err != nil {
		log.Printf("Warning: Could not load playlist: %v\n\n警告: 无法加载播放列表: %v", err, err)
		playlist = make([]string, 0)
	}

	playHistory, err := LoadPlayHistory(dirPath)
	if err != nil {
		log.Printf("Warning: Could not load play history: %v\n\n警告: 无法加载播放历史: %v", err, err)
		playHistory = make([]string, 0)
	}

	app := &App{
		player:              nil,
		mprisServer:         nil,
		currentPageIndex:    GlobalConfig.App.DefaultPage,
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

	// Load saved play mode if enabled
	if GlobalConfig.App.RememberPlayMode {
		savedPlayMode, err := LoadPlayMode()
		if err != nil {
			log.Printf("Warning: Could not load saved play mode: %v", err)
		} else {
			app.playMode = savedPlayMode
		}
	}

	playerPage := NewPlayerPage(app, "", cellW, cellH)
	playListPage := NewPlayList(app)
	libraryPage := NewLibraryWithPath(app, dirPath)
	app.pages = []Page{playerPage, playListPage, libraryPage}

	if GlobalConfig.App.AutostartLastPlayed {
		// First try to load the current song from storage
		currentSong, err := LoadCurrentSong(dirPath)
		if err != nil {
			log.Printf("Warning: Could not load current song: %v", err)
		}

		var songToPlay string
		if currentSong != "" {
			// Use the current song from storage
			songToPlay = currentSong
			// If the current song is not the latest in play history, add it to history
			if len(app.playHistory) == 0 || app.playHistory[len(app.playHistory)-1] != songToPlay {
				app.addToPlayHistory(songToPlay)
			}
		} else if len(app.playHistory) > 0 {
			// Fallback to the last song in play history
			songToPlay = app.playHistory[len(app.playHistory)-1]
		}

		if songToPlay != "" {
			switchToPlayer := app.currentPageIndex == 0
			err := app.PlaySongWithSwitchAndRender(songToPlay, switchToPlayer, false)
			if err != nil {
				log.Printf("Warning: Could not autostart last played song: %v", err)
			}
		}
	}

	return app.Run()
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
	fmt.Println("BM Music Player")
	fmt.Println("\nUsage:")
	fmt.Printf("  bm [directory]\t\tPlay music from the specified directory.\n")
	fmt.Printf("  bm [command]\n")
	fmt.Println("\nCommands:")
	fmt.Printf("  help, -help, --help\t\tShow this help message.\n")
}
