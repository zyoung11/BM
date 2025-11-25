package main

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/term"
)

// Page1 is a simple placeholder page.
type Page1 struct {
	app *App
}

// NewPage1 creates a new instance of Page1.
func NewPage1(app *App) *Page1 {
	return &Page1{app: app}
}

// Init for Page1 does nothing.
func (p *Page1) Init() {}

// HandleKey for Page1 does nothing, but returns an error on ESC to quit.
func (p *Page1) HandleKey(key byte) (Page, error) {
	if key == '\x1b' { // ESC
		return nil, fmt.Errorf("user quit")
	}
	return nil, nil
}

// HandleSignal for Page1 redraws the view on resize.
func (p *Page1) HandleSignal(sig os.Signal) error {
	if sig == syscall.SIGWINCH {
		p.View()
	}
	return nil
}

// View for Page1 clears the screen and shows a message.
func (p *Page1) View() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H") // Clear screen

	message := "This is Page 1"
	x := (w - len(message)) / 2
	y := h / 2
	fmt.Printf("\x1b[%d;%dH%s", y, x, message)
}

// Tick for Page1 does nothing.
func (p *Page1) Tick() {}
