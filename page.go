package main

import "os"

// Page defines the interface for a TUI page.
type Page interface {
	// Init initializes the page.
	Init()

	// HandleKey handles a key press event. It can return a new page
	// to switch to, or nil to stay on the current page.
	// It returns an error to signal the application should quit.
	HandleKey(key byte) (Page, error)

	// HandleSignal handles a system signal (e.g., SIGWINCH for resize).
	HandleSignal(sig os.Signal) error

	// View renders the page's UI. It's called after Init and after every Update.
	View()

	// Tick is called on a regular interval for updates.
	Tick()
}
