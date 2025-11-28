package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/flac"
	"github.com/gopxl/beep/v2/speaker"
	"golang.org/x/term"
)

// fuzzyMatch performs a case-insensitive fuzzy search.
// It returns a score indicating the quality of the match (higher is better).
// Returns 0 if no match is found.
func fuzzyMatch(query, text string) int {
	queryRunes := []rune(strings.ToLower(query))
	textRunes := []rune(strings.ToLower(text))
	if len(queryRunes) == 0 {
		return 100 // Empty query matches everything with high score
	}

	queryIdx := 0
	firstMatchIndex := -1
	lastMatchIndex := -1
	consecutiveMatches := 0
	maxConsecutive := 0

	for i, textRune := range textRunes {
		if textRune == queryRunes[queryIdx] {
			if firstMatchIndex == -1 {
				firstMatchIndex = i
			}
			lastMatchIndex = i

			// Track consecutive matches
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

	// If we didn't match all query characters, return 0
	if queryIdx < len(queryRunes) {
		return 0
	}

	// Calculate score based on various factors
	score := 100

	// Penalty for match spread (closer matches are better)
	matchSpread := lastMatchIndex - firstMatchIndex
	if matchSpread > 0 {
		spreadPenalty := (matchSpread * 10) / len(textRunes)
		score -= spreadPenalty
	}

	// Bonus for consecutive matches
	if maxConsecutive > 1 {
		consecutiveBonus := maxConsecutive * 5
		score += consecutiveBonus
	}

	// Bonus for exact prefix match
	if firstMatchIndex == 0 {
		score += 20
	}

	// Bonus for shorter text (more relevant)
	if len(textRunes) < 50 {
		score += (50 - len(textRunes)) / 5
	}

	// Ensure score is at least 1
	if score < 1 {
		score = 1
	}

	return score
}

// Key constants for special keys
const (
	KeyArrowUp = 1000 + iota
	KeyArrowDown
	KeyArrowLeft
	KeyArrowRight
	KeyEnter
	KeyBackspace
)

// App represents the main TUI application and holds shared state.
type App struct {
	player           *audioPlayer
	mprisServer      *MPRISServer
	pages            []Page
	currentPageIndex int
	Playlist         []string
	currentSongPath  string      // 当前播放的歌曲路径
	playMode         int         // 播放模式: 0=单曲循环, 1=列表循环, 2=随机播放
	volume           float64     // 保存的音量设置
	linearVolume     float64     // 0.0 to 1.0 linear volume for display
	playbackRate     float64     // 保存的播放速度设置
	actionQueue      chan func() // Action queue for thread-safe UI updates

	// 播放历史记录
	playHistory         []string // 播放历史记录，最多100条
	historyIndex        int      // 当前在历史记录中的位置
	isNavigatingHistory bool     // 是否正在历史记录中导航
}

// Page defines the interface for a TUI page.
type Page interface {
	Init()
	HandleKey(key rune) (Page, error)
	HandleSignal(sig os.Signal) error
	View()
	Tick()
}

// switchToPage switches the application to the page at the given index.
func (a *App) switchToPage(index int) {
	if index >= 0 && index < len(a.pages) && index != a.currentPageIndex {
		a.currentPageIndex = index
		newPage := a.pages[a.currentPageIndex]
		// 清理屏幕并重新初始化
		fmt.Print("\x1b[2J\x1b[3J\x1b[H") // Clear screen completely
		newPage.Init()
		newPage.View()
	}
}

// PlaySong 播放指定的歌曲文件
func (a *App) PlaySong(songPath string) error {
	return a.PlaySongWithSwitch(songPath, true)
}

// PlaySongWithSwitch 播放指定的歌曲文件，可选择是否跳转到播放页面
func (a *App) PlaySongWithSwitch(songPath string, switchToPlayer bool) error {
	// 如果是同一首歌，不做任何操作
	if a.currentSongPath == songPath && a.player != nil {
		if switchToPlayer {
			// 切换到播放页面
			a.switchToPage(0) // PlayerPage
		}
		return nil
	}

	// 停止当前播放
	speaker.Lock()
	if a.player != nil {
		a.player.ctrl.Paused = true
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
	player, err := newAudioPlayer(streamer, format, a.volume, a.playbackRate)
	if err != nil {
		f.Close()
		return fmt.Errorf("创建播放器失败: %v", err)
	}

	// 更新MPRIS服务
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

	// 更新应用状态
	speaker.Lock()
	a.player = player
	a.mprisServer = mprisServer
	a.currentSongPath = songPath
	speaker.Unlock()

	// 记录播放历史
	a.addToPlayHistory(songPath)

	// 开始播放
	speaker.Play(a.player.volume)

	// 更新PlayerPage
	if playerPage, ok := a.pages[0].(*PlayerPage); ok {
		playerPage.UpdateSong(songPath)
		// 无论是否跳转页面，都重新渲染播放页面
		if !switchToPlayer {
			// 不跳转页面时，清理屏幕并重新渲染
			fmt.Print("\x1b[2J\x1b[3J\x1b[H") // 完全清理屏幕
			playerPage.Init()
			playerPage.View()
		}
	}

	// 根据参数决定是否切换到播放页面
	if switchToPlayer {
		fmt.Print("\x1b[2J\x1b[3J\x1b[H") // 完全清理屏幕
		a.currentPageIndex = 0            // 直接设置页面索引
		playerPage := a.pages[0].(*PlayerPage)
		playerPage.Init()
		playerPage.View()
	}

	return nil
}

// addToPlayHistory 添加歌曲到播放历史记录
func (a *App) addToPlayHistory(songPath string) {
	// 如果当前在历史记录中间位置，删除当前位置之后的所有记录
	if a.historyIndex < len(a.playHistory)-1 {
		a.playHistory = a.playHistory[:a.historyIndex+1]
	}

	// 添加新记录
	a.playHistory = append(a.playHistory, songPath)

	// 限制历史记录最多100条
	if len(a.playHistory) > 100 {
		a.playHistory = a.playHistory[1:]
	}

	// 更新历史索引到最新位置
	a.historyIndex = len(a.playHistory) - 1

	// 添加新记录时，重置导航标志（表示用户开始新的播放路径）
	a.isNavigatingHistory = false
}

// NextSong 切换到下一首歌曲
func (a *App) NextSong() {
	a.actionQueue <- func() {
		if playerPage, ok := a.pages[0].(*PlayerPage); ok {
			playerPage.playNextSong()
		}
	}
}

// PreviousSong 切换到上一首歌曲
func (a *App) PreviousSong() {
	a.actionQueue <- func() {
		if playerPage, ok := a.pages[0].(*PlayerPage); ok {
			playerPage.playPreviousSong()
		}
	}
}

// Run starts the application's main event loop.
func (a *App) Run() error {
	fmt.Print("\x1b[?1049h\x1b[?25l")
	defer fmt.Print("\x1b[?1049l\x1b[?25h")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH, syscall.SIGINT)
	defer signal.Stop(sigCh)

	keyCh := make(chan rune)
	go func() {
		buf := make([]byte, 3)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			if n > 0 {
				key := rune(buf[0])
				if key == '\x1b' && n > 1 && buf[1] == '[' {
					switch buf[2] {
					case 'A':
						keyCh <- KeyArrowUp
					case 'B':
						keyCh <- KeyArrowDown
					case 'C':
						keyCh <- KeyArrowRight
					case 'D':
						keyCh <- KeyArrowLeft
					}
				} else {
					switch key {
					case '\r', '\n': // Handle Enter (CR and LF)
						keyCh <- KeyEnter
					case 8, 127: // Handle Backspace (ASCII 8 and 127)
						keyCh <- KeyBackspace
					default:
						keyCh <- key
					}
				}
			}
		}
	}()

	ticker := time.NewTicker(time.Second / 2)
	defer ticker.Stop()

	a.pages[a.currentPageIndex].Init()
	a.pages[a.currentPageIndex].View()

	for {
		currentPage := a.pages[a.currentPageIndex]
		select {
		case action := <-a.actionQueue:
			action()

		case key := <-keyCh:
			// Global keybindings
			if IsKey(key, AppConfig.Keymap.Global.Quit) {
				return nil // Exit the application
			} else if IsKey(key, AppConfig.Keymap.Global.CyclePages) {
				a.switchToPage((a.currentPageIndex + 1) % len(a.pages))
			} else if IsKey(key, AppConfig.Keymap.Global.SwitchToPlayer) {
				a.switchToPage(0) // PlayerPage
			} else if IsKey(key, AppConfig.Keymap.Global.SwitchToPlayList) {
				a.switchToPage(1) // PlayListPage
			} else if IsKey(key, AppConfig.Keymap.Global.SwitchToLibrary) {
				a.switchToPage(2) // LibraryPage
			} else {
				// Pass the key to the current page's handler
				_, err := currentPage.HandleKey(key)
				if err != nil {
					// Specific error checks can be done here if needed
					return nil // Assume any error from HandleKey means quit
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

func main() {
	// Load configuration first
	if err := LoadConfig(); err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	if len(os.Args) != 2 {
		log.Fatalf("用法: %s <music_directory>", os.Args[0])
	}
	dirPath := os.Args[1]

	// 验证输入路径是目录
	info, err := os.Stat(dirPath)
	if err != nil {
		log.Fatalf("无法访问路径: %v", err)
	}
	if !info.IsDir() {
		log.Fatalf("输入路径必须是目录，不是文件")
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		log.Fatalf("设置原始模式失败: %v", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	cellW, cellH, err := getCellSize()
	if err != nil {
		log.Fatalf("无法获取终端单元格尺寸: %v", err)
	}

	// 初始化speaker，但不立即播放任何音频
	sampleRate := beep.SampleRate(44100)
	speaker.Init(sampleRate, sampleRate.N(time.Second/30))

	app := &App{
		player:              nil, // 延迟初始化
		mprisServer:         nil, // 延迟初始化
		currentPageIndex:    2,   // 默认显示Library页面
		Playlist:            make([]string, 0),
		playMode:            0,                     // 默认单曲循环
		volume:              0,                     // 默认音量0（100%）
		linearVolume:        1.0,                   // 默认线性音量1.0（100%）
		playbackRate:        1.0,                   // 默认播放速度1.0
		actionQueue:         make(chan func(), 10), // Initialize the action queue
		playHistory:         make([]string, 0),     // 初始化播放历史记录
		historyIndex:        -1,                    // 初始历史索引
		isNavigatingHistory: false,                 // 初始不在历史记录导航中
	}

	playerPage := NewPlayerPage(app, "", cellW, cellH) // 空的初始路径
	playListPage := NewPlayList(app)
	libraryPage := NewLibraryWithPath(app, dirPath) // 传递初始目录路径
	app.pages = []Page{playerPage, playListPage, libraryPage}

	if err := app.Run(); err != nil {
		log.Fatalf("应用运行时出现错误: %v", err)
	}
}
