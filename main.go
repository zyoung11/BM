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
	player      *audioPlayer
	mprisServer *MPRISServer
	currentPage Page
}

// Run starts the application's main event loop.
func (a *App) Run() error {
	// --- Terminal Setup ---
	fmt.Print("\x1b[?1049h\x1b[?25l")       // Enter alternate screen buffer and hide cursor
	defer fmt.Print("\x1b[?1049l\x1b[?25h") // Exit alternate screen buffer and show cursor

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("设置原始模式失败: %v", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

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
				// This can happen on exit, so we just stop the goroutine
				return
			}
			if n > 0 {
				keyCh <- buf[0]
			}
		}
	}()

	ticker := time.NewTicker(time.Second / 2) // Update more frequently for smoother progress
	defer ticker.Stop()

	// --- Initial Draw ---
	a.currentPage.Init()
	a.currentPage.View()

	// --- Main Event Loop ---
	for {
		select {
		case key := <-keyCh:
			newPage, err := a.currentPage.HandleKey(key)
			if err != nil { // A non-nil error from HandleKey signals a quit request
				return nil
			}
			if newPage != nil {
				a.currentPage = newPage
				a.currentPage.Init()
				// After a page switch, always do a full redraw.
				a.currentPage.View()
			}
			// Redrawing is now the responsibility of the HandleKey method.

		case sig := <-sigCh:
			if sig == syscall.SIGINT {
				return nil // Ctrl+C to quit
			}
			// Delegate other signals (like SIGWINCH) to the page
			if err := a.currentPage.HandleSignal(sig); err != nil {
				return err
			}
			// Redrawing is now the responsibility of the HandleSignal method.

		case <-ticker.C:
			// Let the page handle its tick-based updates
			a.currentPage.Tick()
			// The Tick handler is responsible for its own redraws.
		}
	}
}

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("用法: %s <music.flac>", os.Args[0])
	}
	flacPath := os.Args[1]

	// --- Audio Service Initialization ---
	f, err := os.Open(flacPath)
	if err != nil {
		log.Fatalf("打开文件失败: %v", err)
	}
	// Note: We don't defer f.Close() here because the streamer needs it.
	// It will be closed by the MPRIS server when the app exits.

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
		// Non-fatal, the app can run without MPRIS
		log.Printf("MPRIS 服务启动失败: %v", err)
	} else {
		if err := mprisServer.Start(); err != nil {
			log.Printf("MPRIS 服务注册失败: %v", err)
		} else {
			// Clean up MPRIS on exit
			defer mprisServer.StopService()
			mprisServer.StartUpdateLoop()
			mprisServer.UpdatePlaybackStatus(true)
			mprisServer.UpdateMetadata()
		}
	}

	// --- App Initialization ---
	app := &App{
		player:      player,
		mprisServer: mprisServer,
	}

	// --- Create initial page ---
	// This will be our PlayerPage, which we'll create in player.go
	playerPage := NewPlayerPage(app, flacPath)
	app.currentPage = playerPage

	// --- Start Audio Playback ---
	// This is non-blocking
	speaker.Play(app.player.volume)

	// --- Run App ---
	// This is blocking and will run the main loop
	if err := app.Run(); err != nil {
		log.Fatalf("应用运行时出现错误: %v", err)
	}
}
