# BM - Terminal Music Player

## Build & Development
- `go build -o bm main.go` - Build the binary
- `./bm <music.flac>` - Run with FLAC file
- No tests or linting configured

## Code Style Guidelines
- **Imports**: Group standard lib, third-party, local imports
- **Formatting**: Use gofmt, no trailing whitespace
- **Naming**: camelCase for functions/vars, PascalCase for exported
- **Error handling**: Return errors, log fatal errors only in main
- **Types**: Use generics for min/max functions
- **Comments**: Chinese comments for user-facing explanations

## Architecture & Design
- **Audio**: beep/v2 for playback with FLAC support, uses Loop2 for infinite looping
- **Display**: sixel for terminal image rendering with resize for scaling
- **Layout**: Adaptive based on terminal width (80 chars threshold)

## Visual Design Features
- **Wide Terminal**: Image left, info right - text centered in available space
- **Narrow Terminal**: Image top, info bottom - each line individually centered
- **Visual Centering**: Text positions account for string length for true visual balance
- **Absolute Centering**: Narrow mode ensures image is perfectly centered (width adjustments)
- **Minimalist Display**: Only shows title, artist, album - no labels or controls
- **Progress Bar**: Wide terminal shows Unicode "─" progress bar with dimmed played section

## Key Functions
- `displayAlbumArt()` - Extracts and displays album art with adaptive layout
- `playMusic()` - Main playback loop with keyboard controls
- `updateRightPanel()` / `updateBottomStatus()` - Layout-specific info display
- `getSongMetadata()` - Reads FLAC metadata from tags
- `updateRightPanel()` - Displays song info + progress bar in wide terminal mode

## Controls
- Space: Play/Pause
- q/w: Seek -5/+5 seconds
- a/s: Volume down/up
- z/x: Speed down/up
- ESC: Exit