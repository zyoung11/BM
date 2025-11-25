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

// App represents the main TUI application and holds shared state.

type App struct {

	player           *audioPlayer

	mprisServer      *MPRISServer

	pages            []Page

	currentPageIndex int

}



// Run starts the application's main event loop.

func (a *App) Run() error {

	// Terminal is already in raw mode, just manage screen buffer and cursor

	fmt.Print("\x1b[?1049h\x1b[?25l") // Enter alternate screen buffer and hide cursor

	defer fmt.Print("\x1b[?1049l\x1b[?25h") // Exit alternate screen buffer and show cursor



	// --- Event Channels ---

	sigCh := make(chan os.Signal, 1)

	signal.Notify(sigCh, syscall.SIGWINCH, syscall.SIGINT)

	defer signal.Stop(sigCh)



	keyCh := make(chan byte, 1)

	go func() {

		buf := make([]byte, 1)

		for {

			n, err := os.Stdin.Read(buf)

			if err != nil {

				return

			}

			if n > 0 {

				keyCh <- buf[0]

			}

		}

	}()



	ticker := time.NewTicker(time.Second / 2)

	defer ticker.Stop()



	// --- Initial Draw ---

	a.pages[a.currentPageIndex].Init()

	a.pages[a.currentPageIndex].View()



	// --- Main Event Loop ---

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



	// --- Pre-flight Setup (before Run loop) ---

	// Set terminal to raw mode *before* starting key reader goroutine

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))

	if err != nil {

		log.Fatalf("设置原始模式失败: %v", err)

	}

	defer term.Restore(int(os.Stdin.Fd()), oldState)



	// Get cell size once, now that we are in raw mode and before the key reader starts

	cellW, cellH, err := getCellSize()

	if err != nil {

		// On failure, we can't really continue with image display

		log.Fatalf("无法获取终端单元格尺寸: %v", err)

	}





	// --- Audio Service Initialization ---

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



	// --- MPRIS Service Initialization ---

	mprisServer, err := NewMPRISServer(player, flacPath)

	if err != nil {

		log.Printf("MPRIS 服务启动失败: %v", err)

	} else {

		if err := mprisServer.Start(); err != nil {

			log.Printf("MPRIS 服务注册失败: %v", err)

		} else {

			defer mprisServer.StopService()

			mprisServer.StartUpdateLoop()

			mprisServer.UpdatePlaybackStatus(true)

			mprisServer.UpdateMetadata()

		}

	}



	// --- App Initialization ---

	app := &App{

		player:           player,

		mprisServer:      mprisServer,

		currentPageIndex: 0,

	}



	// --- Create and add pages ---

	playerPage := NewPlayerPage(app, flacPath, cellW, cellH)

	page1 := NewPage1(app)

	page2 := NewPage2(app)

	app.pages = []Page{playerPage, page1, page2}



	// --- Start Audio Playback ---

	speaker.Play(app.player.volume)



	// --- Run App ---

	if err := app.Run(); err != nil {

		log.Fatalf("应用运行时出现错误: %v", err)

	}

}




