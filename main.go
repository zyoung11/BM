package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gopxl/beep/v2/flac"
	"github.com/gopxl/beep/v2/speaker"
	"golang.org/x/term"
)

// Key constants for special keys
const (
	KeyArrowUp    = 1000 + iota
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
}

// Page defines the interface for a TUI page.
type Page interface {
	Init()
	HandleKey(key rune) (Page, error)
	HandleSignal(sig os.Signal) error
	View()
	Tick()
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
			if key == '\t' {
				a.currentPageIndex = (a.currentPageIndex + 1) % len(a.pages)
				newPage := a.pages[a.currentPageIndex]
				newPage.Init()
				newPage.View()
				continue
			}

			_, err := currentPage.HandleKey(key)
			if err != nil {
				return nil
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
		log.Fatalf("用法: %s <music.flac>", os.Args[0])
	}
	flacPath := os.Args[1]

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		log.Fatalf("设置原始模式失败: %v", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	cellW, cellH, err := getCellSize()
	if err != nil {
		log.Fatalf("无法获取终端单元格尺寸: %v", err)
	}

	f, err := os.Open(flacPath)
	if err != nil {
		log.Fatalf("打开文件失败: %v", err)
	}

	streamer, format, err := flac.Decode(f)
	if err != nil {
		log.Fatalf("解码FLAC失败: %v", err)
	}

	speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/30))

	player, err := newAudioPlayer(streamer, format)
	if err != nil {
		log.Fatalf("创建播放器失败: %v", err)
	}

	mprisServer, err := NewMPRISServer(player, flacPath)
	if err == nil {
		if err := mprisServer.Start(); err == nil {
			defer mprisServer.StopService()
			mprisServer.StartUpdateLoop()
			mprisServer.UpdatePlaybackStatus(true)
			mprisServer.UpdateMetadata()
		}
	}

	app := &App{
		player:           player,
		mprisServer:      mprisServer,
		currentPageIndex: 0,
		Playlist:         make([]string, 0),
	}

	playerPage := NewPlayerPage(app, flacPath, cellW, cellH)
	playListPage := NewPlayList(app)
	libraryPage := NewLibrary(app)
	app.pages = []Page{playerPage, playListPage, libraryPage}

	speaker.Play(app.player.volume)

	if err := app.Run(); err != nil {
		log.Fatalf("应用运行时出现错误: %v", err)
	}
}