# GEMINI.md - Project Context

This document provides a comprehensive overview of the `bm` project for a Gemini-based AI assistant.

## 1. Project Overview

`bm` is a terminal-based music player written in Go. It began as a player for single audio files, rendering album art in the terminal, and is being refactored to support a music library and playlists.

The application is architected as a **multi-page TUI application**. A central "App" engine manages shared services and an event loop, while distinct "Pages" are responsible for UI rendering and handling user input. This allows for features like background audio playback while navigating different views.

### Key Technologies & Features:

*   **Language:** Go
*   **Audio Playback:** `github.com/gopxl/beep/v2`
*   **Album Art Display:** `github.com/mattn/go-sixel` for rendering images in the terminal.
*   **Media Controls:** `github.com/godbus/dbus/v5` for MPRIS D-Bus integration.
*   **Metadata:** `github.com/dhowden/tag` for reading metadata from audio files.
*   **Architecture:** A custom, lightweight TUI engine built around a `Page` interface.
*   **Current Pages:**
    *   `PlayerPage`: The main music player UI.
    *   `Library`: A page to browse `.flac` files in the local directory.
    *   `PlayList`: A page to view songs selected from the Library.

## 2. Current State & Next Steps

The application is in a transitional phase. The user has requested to build the `Library` and `PlayList` functionality **in isolation** from the core audio player first.

*   **Current Behavior:** The application still requires a single `.flac` file path as a command-line argument. The `PlayerPage` will play this file as it always has. The `Library` and `PlayList` pages are fully functional for file browsing and selection. The `Library` page now supports hierarchical browsing of `.flac` files and directories. Files and directories can be selected, and their selection state is maintained and reflected in the `PlayList`. They do not yet control what song is played.
*   **Next Major Step:** The next step is to integrate the `PlayList` page with the `audioPlayer` service. This will involve changing the application to take a directory as input and modifying the audio engine to play songs from the playlist instead of a single hardcoded file. **This work is intentionally deferred.**

## 3. Building and Running

### Running the Application

To run the application, provide a path to a `.flac` file. This file will be played by the `PlayerPage`.

```bash
# Example
go run . "path/to/your/music.flac"
```

You can switch to the `Library` and `PlayList` pages using the `Tab` key.

### Building the Application

```bash
go build .
```

## 4. Development Conventions

### Architecture: App & Page Model

The core of the application is the `App` struct in `main.go` and the `Page` interface. The `App` struct now also holds a `Playlist` slice of strings, which is the shared state between the `Library` and `PlayList` pages.

*   **`page.go`**: This file defines the crucial `Page` interface. The `HandleKey` signature has been updated to support special keys.
    ```go
    type Page interface {
        Init()
        HandleKey(key rune) (Page, error) // Now accepts a rune
        HandleSignal(sig os.Signal) error
        View()
        Tick()
    }
    ```

### Keybindings

*   **Global:**
    *   `Tab`: Cycle through pages.
    *   `1`: Switch to Player page.
    *   `2`: Switch to PlayList page.
    *   `3`: Switch to Library page.
    *   `ESC`: Quit the application from any page.
*   **PlayerPage Controls:**
    *   `Space`: Play/Pause
    *   `q` / `w`: Seek backward/forward
    *   `a` / `s`: Volume down/up
    *   `z` / `x`: Adjust playback rate
    *   `e`: Toggle text color
*   **Library Controls:**
    *   `Up Arrow` / `k` / `w`: Navigate up (circularly).
    *   `Down Arrow` / `j` / `s`: Navigate down (circularly).
    *   `Right Arrow` / `l` / `d`: Enter selected directory.
    *   `Left Arrow` / `h` / `a`: Exit current directory.
    *   `Space`:
        *   On a file: Toggle selection. Automatically adds/removes the file from the playlist. Cursor advances (non-circularly).
        *   On a directory: Toggle selection of all `.flac` files within that directory (and its subdirectories). Cursor advances (non-circularly).
*   **PlayList Controls:**
    *   `Up Arrow` / `k` / `w`: Navigate up (circularly).
    *   `Down Arrow` / `j` / `s`: Navigate down (circularly).
    *   `Space`: Remove the currently highlighted song from the playlist.
