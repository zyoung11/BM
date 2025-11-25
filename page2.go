package main

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/term"
)

// Page2 is a simple placeholder page.
type Page2 struct {
	app *App
}

// NewPage2 creates a new instance of Page2.
func NewPage2(app *App) *Page2 {
	return &Page2{app: app}
}

// Init for Page2 does nothing.
func (p *Page2) Init() {}

// HandleKey for Page2 does nothing, but returns an error on ESC to quit.
func (p *Page2) HandleKey(key byte) (Page, error) {
	if key == '\x1b' { // ESC
		return nil, fmt.Errorf("user quit")
	}
	return nil, nil
}

// HandleSignal for Page2 redraws the view on resize.
func (p *Page2) HandleSignal(sig os.Signal) error {
	if sig == syscall.SIGWINCH {
		p.View()
	}
	return nil
}

// View for Page2 clears the screen and shows a message.
func (p *Page2) View() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H") // Clear screen

	message := "This is Page 2"
	x := (w - len(message)) / 2
	y := h / 2
	fmt.Printf("\x1b[%d;%dH%s", y, x, message)
}

// Tick for Page2 does nothing.
func (p *Page2) Tick() {}
