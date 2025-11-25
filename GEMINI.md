# GEMINI.md - Project Context

This document provides a comprehensive overview of the `bm` project for a Gemini-based AI assistant.

## 1. Project Overview

`bm` is a terminal-based music player written in Go. It is designed to play single audio files (specifically `.flac` files were tested) and render their album art directly in a compatible terminal using Sixel graphics.

The application is architected as a **multi-page TUI application**. A central "App" engine manages shared services and an event loop, while distinct "Pages" are responsible for UI rendering and handling user input. This allows for features like background audio playback while navigating different views.

### Key Technologies & Features:

*   **Language:** Go
*   **Audio Playback:** `github.com/gopxl/beep/v2`
*   **Album Art Display:** `github.com/mattn/go-sixel` for rendering images in the terminal.
*   **Media Controls:** `github.com/godbus/dbus/v5` for MPRIS D-Bus integration, allowing control via system media keys and widgets.
*   **Metadata:** `github.com/dhowden/tag` for reading metadata (title, artist, album art) from audio files.
*   **Architecture:** A custom, lightweight TUI engine built around a `Page` interface. This allows for extensible, independent views. The core audio playback is managed as a background service, decoupled from the UI pages.
*   **Current Pages:**
    *   `PlayerPage`: The main music player UI.
    *   `Page1`, `Page2`: Simple placeholder pages to demonstrate the multi-page capability.

## 2. Building and Running

### Dependencies

Dependencies are managed by Go Modules and are listed in the `go.mod` file. They will be downloaded automatically by Go commands.

### Running the Application

To run the application directly without building a binary, use `go run`. An audio file path is required as a command-line argument.

```bash
# Example
go run . "path/to/your/music.flac"
```

### Building the Application

To build the executable binary, use `go build`. This will create a binary named `bm` (from the module name) in the project root.

```bash
go build .
```

You can then run the compiled application:

```bash
./bm "path/to/your/music.flac"
```

### Testing

The project contains a `/test` directory, but no automated tests have been implemented yet.

**TODO:** Document the official testing procedure if one is established.

## 3. Development Conventions

### Architecture: App & Page Model

The core of the application is the `App` struct in `main.go` and the `Page` interface in `page.go`.

*   **`main.go`**: This file acts as the main TUI engine.
    *   The `main()` function handles all one-time setup: initializing the terminal, initializing the audio service (`audioPlayer` and `speaker`), setting up the MPRIS service, creating all pages, and adding them to the `App`.
    *   The `App.Run()` method contains the main event loop, which listens for keyboard input, system signals (`SIGWINCH`, `SIGINT`), and a periodic ticker.
    *   The event loop handles global actions (like `Tab` for page switching) and delegates all other events to the currently active page.

*   **`page.go`**: This file defines the crucial `Page` interface, which is the contract for all views in the application.
    ```go
    type Page interface {
        Init()
        HandleKey(key byte) (Page, error)
        HandleSignal(sig os.Signal) error
        View()
        Tick()
    }
    ```

### Adding a New Page

To add a new feature or view, follow this pattern:

1.  Create a new file (e.g., `newpage.go`).
2.  Define a struct for your page (e.g., `NewPage`).
3.  Implement all methods of the `Page` interface for your struct.
4.  In `main.go`, instantiate your new page and add it to the `app.pages` slice.

### Concurrency and State

*   **Audio Safety:** The audio stream (`beep.Streamer`) is manipulated by multiple goroutines (the main loop, the MPRIS handler, the `speaker`'s internal audio goroutine). Any call that modifies the stream's state, especially `Seek`, **must** be wrapped in `speaker.Lock()` and `speaker.Unlock()` to prevent data races.
*   **I/O Safety:** Avoid reading from `os.Stdin` in page-specific code, as a central goroutine in `main.go` is already consuming `os.Stdin` for keyboard input. The `getCellSize()` function was a source of a race condition and was moved to the initial setup in `main()` to be called only once before other goroutines start.

### Keybindings

*   **Global:** `Tab` is reserved for cycling through pages and is handled by `main.go`.
*   **Page-Specific:** All other keys are handled by the `HandleKey` method of the active page.
    *   **PlayerPage Controls:**
        *   `ESC`: Quit
        *   `Space`: Play/Pause
        *   `q` / `w`: Seek backward/forward
        *   `a` / `s`: Volume down/up
        *   `z` / `x`: Adjust playback rate
        *   `e`: Toggle between album art color and default white for text.
