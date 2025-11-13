package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/blacktop/go-termimg"
	"github.com/dhowden/tag"
	"golang.org/x/term"
)

// Event represents a terminal event.
type Event interface{}

// KeyPressEvent is sent when a key is pressed.
type KeyPressEvent struct{}

// SignalEvent is sent when an OS signal is received.
type SignalEvent struct {
	Signal os.Signal
}

func main() {
	// --- 1. Initial Setup ---
	// Enter alternate screen buffer and ensure it's exited on return
	fmt.Print("\x1b[?1049h")
	defer fmt.Print("\x1b[?1049l")

	// Set terminal to raw mode to capture single key presses
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		log.Fatalf("failed to set terminal to raw mode: %v", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// --- 2. Main Loop ---
	for {
		// In each iteration, we draw the screen, then wait for one event.
		redraw()

		event := pollOneEvent()

		switch event.(type) {
		case KeyPressEvent:
			// Any key press, we exit.
			return
		case SignalEvent:
			// If it's a resize signal, the loop will continue and redraw.
			// We don't need to do anything extra here.
			continue
		}
	}
}

// pollOneEvent waits for a single terminal event (key press or resize) and returns it.
func pollOneEvent() Event {
	// Channel for keyboard input
	keyChan := make(chan struct{})
	go func() {
		// Read a single byte, which is enough to detect a key press
		var b [1]byte
		os.Stdin.Read(b[:])
		close(keyChan)
	}()

	// Channel for resize signals
	sigwinchChan := make(chan os.Signal, 1)
	signal.Notify(sigwinchChan, syscall.SIGWINCH)
	defer signal.Stop(sigwinchChan)

	// Wait for the first event to happen
	select {
	case <-keyChan:
		return KeyPressEvent{}
	case sig := <-sigwinchChan:
		return SignalEvent{Signal: sig}
	}
}

// redraw gets terminal size, generates a sized/centered image, and prints it.
func redraw() {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		fmt.Print("\x1b[2J\x1b[H") // Clear screen
		fmt.Println("Could not get terminal size.")
		return
	}

	// Clear screen and move cursor to top-left for drawing
	fmt.Print("\x1b[2J\x1b[H")

	// --- DEBUG: Print current size ---
	fmt.Printf("Terminal Size: %d width, %d height\n", width, height)

	sixelData, err := prepareSixelData(width, height)
	if err != nil {
		fmt.Printf("Could not prepare image: %v", err)
		return
	}

	// Move cursor below the debug text and print the Sixel data
	fmt.Print("\x1b[2;1H")
	fmt.Print(sixelData)

	// Print a quit message at the bottom
	fmt.Printf("\x1b[%d;1H", height)
	fmt.Print("Press any key to quit.")
}

func prepareSixelData(termWidth, termHeight int) (string, error) {

picture := getCover("/home/zy/zy/XM/GO/BM/Nujabes/Nujabes,Shing02 - Luv(sic.), Pt. 3.flac")
	if picture == nil {
		return "", fmt.Errorf("failed to get cover art")
	}

	tmpDir, err := os.MkdirTemp("", "cov")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pipePath := tmpDir + "/cover.jpg"
	if err := syscall.Mkfifo(pipePath, 0600); err != nil {
		return "", fmt.Errorf("failed to create fifo: %w", err)
	}

	writerErrChan := make(chan error, 1)
	go func() {
		f, err := os.OpenFile(pipePath, os.O_WRONLY, 0600)
		if err != nil {
			writerErrChan <- err
			return
		}
		defer f.Close()
		_, err = io.Copy(f, bytes.NewReader(picture.Data))
		writerErrChan <- err
	}()

	img, err := termimg.Open(pipePath)
	if err != nil {
		return "", fmt.Errorf("failed to open image from pipe: %w", err)
	}

	// Render by setting the width to the terminal width and let the library
	// calculate the height to preserve the aspect ratio.
	rendered, err := img.
		Width(termWidth).
		Protocol(termimg.Sixel).
		Render()
	if err != nil {
		return "", fmt.Errorf("failed to render image: %w", err)
	}

	if err := <-writerErrChan; err != nil {
		return "", fmt.Errorf("writer goroutine failed: %w", err)
	}

	return rendered, nil
}

func getCover(filePath string) *tag.Picture {
	f, err := os.Open(filePath)
	if err != nil {
		log.Printf("无法打开文件: %v", err)
		return nil
	}
	defer f.Close()
	meta, err := tag.ReadFrom(f)
	if err != nil {
		log.Printf("读取元数据失败: %v", err)
		return nil
	}
	pic := meta.Picture()
	if pic == nil {
		log.Printf("未找到封面图片")
		return nil
	}
	return pic
}
