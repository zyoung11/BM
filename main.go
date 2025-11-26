package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/flac"
	"github.com/gopxl/beep/v2/speaker"
	"golang.org/x/term"
)

// Key constants for special keys
const (
	KeyArrowUp = 1000 + iota
	KeyArrowDown
	KeyArrowLeft
	KeyArrowRight
)

// App represents the main TUI application and holds shared state.
type App struct {
	player           *audioPlayer
	mprisServer      *MPRISServer
	pages            []Page
	currentPageIndex int
	Playlist         []string
	currentSongPath  string // 当前播放的歌曲路径
	playMode         int    // 播放模式: 0=单曲循环, 1=列表循环, 2=随机播放
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
	player, err := newAudioPlayer(streamer, format)
	if err != nil {
		f.Close()
		return fmt.Errorf("创建播放器失败: %v", err)
	}

	// 更新MPRIS服务
	if a.mprisServer != nil {
		a.mprisServer.StopService()
	}
	mprisServer, err := NewMPRISServer(player, songPath)
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
				if buf[0] == '\x1b' && n > 1 && buf[1] == '[' {
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
					keyCh <- rune(buf[0])
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
		case key := <-keyCh:
			switch key {
			case '\t':
				a.switchToPage((a.currentPageIndex + 1) % len(a.pages))
			case '1':
				a.switchToPage(0) // PlayerPage
			case '2':
				a.switchToPage(1) // PlayListPage
			case '3':
				a.switchToPage(2) // LibraryPage
			default:
				_, err := currentPage.HandleKey(key)
				if err != nil {
					return nil
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
		player:           nil, // 延迟初始化
		mprisServer:      nil, // 延迟初始化
		currentPageIndex: 2,   // 默认显示Library页面
		Playlist:         make([]string, 0),
		playMode:         0, // 默认单曲循环
	}

	playerPage := NewPlayerPage(app, "", cellW, cellH) // 空的初始路径
	playListPage := NewPlayList(app)
	libraryPage := NewLibraryWithPath(app, dirPath) // 传递初始目录路径
	app.pages = []Page{playerPage, playListPage, libraryPage}

	if err := app.Run(); err != nil {
		log.Fatalf("应用运行时出现错误: %v", err)
	}
}
