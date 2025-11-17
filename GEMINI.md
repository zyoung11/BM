# Gemini Project Analysis: bm

## Project Overview

This is a command-line utility written in Go that displays the cover art from a music file directly in a Sixel-compatible terminal, and now also plays the audio from that file. The program takes a path to a music file (e.g., `.flac`) as a command-line argument.

The core of the program is its sophisticated image and audio rendering pipeline:
1.  **Pixel-Perfect Sizing:** On startup, the application queries the terminal directly using ANSI escape codes (`CSI 16t`) to determine the exact pixel dimensions of a single character cell (the font size).
2.  **Dynamic Resizing:** By combining the character cell size with the terminal's dimensions (in rows and columns), the program calculates the total available pixel area for the image. It listens for `SIGWINCH` signals and recalculates these dimensions whenever the terminal window is resized.
3.  **Aspect-Ratio Preserving Scaling:** The extracted cover art is then scaled down using a `Thumbnail` function to fit perfectly within the calculated available screen space, preserving its original aspect ratio. This ensures the image is as large as possible without distortion or cropping.
4.  **Direct Sixel Rendering:** The final image is encoded into the Sixel format and printed directly to the standard output.
5.  **Conditional Alignment:** The image's alignment changes based on available space. If the terminal window is very wide (width >= 2 * image width), the image is left-aligned and vertically centered. Otherwise (in tall, square, or moderately wide windows), it is horizontally centered and vertically top-aligned.
6.  **Flicker-Free UI Updates:** The UI is rendered in two parts: a static layout (cover art) drawn only on startup and resize, and dynamic player status information (position, volume, etc.) which is updated locally without clearing the entire screen, preventing flicker.

The program features a robust, single-threaded event loop that correctly handles `SIGWINCH` resize signals, keyboard input for player controls, and `SIGINT` for graceful exit, providing a smooth and responsive user experience. It manually controls the terminal's alternate screen buffer and raw input mode to ensure stable rendering without relying on larger TUI frameworks.

**Key Technologies:**
- **Language:** Go
- **Audio Playback:** `github.com/gopxl/beep/v2`
- **Image Rendering (Sixel):** `github.com/mattn/go-sixel`
- **Image Scaling:** `github.com/nfnt/resize`
- **Metadata Extraction:** `github.com/dhowden/tag`
- **Terminal Control:** Manual ANSI escape sequences, `golang.org/x/term` for raw mode and character dimensions, `os/signal` for resize events.

## Building and Running

### Building
To build the executable, run the following command from the project root directory:
```sh
go build .
```
This will create a binary named `bm`.

### Running
To run the program, execute the compiled binary with a path to a music file:
```sh
./bm path/to/your/musicfile.flac
```
The program will clear the screen, display the cover art and player controls, and dynamically adjust its size if the terminal window is resized.

**Controls:**
-   `Esc` or `Ctrl+C`: Exit the program.
-   `Space`: Pause/Resume playback.
-   `q` / `w`: Seek backward/forward by 5 seconds.
-   `a` / `s`: Decrease/Increase volume.
-   `z` / `x`: Decrease/Increase playback speed.

## Development Conventions

- **Single File:** All application logic is contained within `main.go`.
- **CLI Argument:** The path to the music file is provided as a command-line argument.
- **Pixel Size Query:** The program relies on the terminal's ability to respond to `\x1b[16t` (CSI 16t) to determine font metrics. If a terminal does not support this, the program will fail on startup.
- **Error Handling:** The program uses `log.Fatalf` for critical errors during setup (like failing to query terminal size), which terminates the application.
- **Safety Margin for Image Width:** A 99% safety margin is applied to the calculated pixel width to account for potential terminal padding or borders, ensuring the image is not cropped.
- **Helper Functions:** Generic `max` and `min` functions are used for clamping values.
- **Flicker-Free Rendering:** Drawing logic is separated into `drawLayout` (static elements) and `updateStatus` (dynamic elements) to prevent screen flicker.
- **Dependencies:** Go modules are used for dependency management. Run `go mod tidy` to ensure dependencies are clean after making changes.