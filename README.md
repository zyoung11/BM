# BM - Terminal Music Player

![](/home/zy/zy/XM/GO/BM/image.png)

BM is a modern terminal music player written in Go, featuring a rich set of functions and a beautiful TUI interface. It supports FLAC, MP3, WAV, and OGG audio formats, providing album cover display, playlist management, fuzzy search, and more. This project exists purely because I love [kew](https://github.com/ravachol/kew) so much, but I'm not familiar with C language, so I wrote this terminal music player in Go that better suits my aesthetic and habits.

## Features

### Audio Playback

- **Multi-format support**: FLAC, MP3, WAV, OGG
- **Playback control**: Play/Pause, Fast forward/Rewind (5-second intervals)
- **Volume control**: Logarithmic volume curve with fine adjustment
- **Speed control**: 0.1x to 4.0x playback speed control
- **High-quality resampling**: Support for multiple resampling quality options

### Terminal Interface

- **Responsive design**: Adapts to terminal dimensions
- **Album cover display**: Supports Kitty, Sixel, iTerm2 image protocols
- **Smart color scheme**: Extracts colors from album covers for UI
- **Multi-page system**: Player, Playlist, and Library main pages

### Media Management

- **Filesystem browsing**: Complete directory navigation functionality
- **Fuzzy search**: Supports Chinese and English fuzzy matching
- **Playlist**: Dynamic playlist management
- **Playback history**: Records up to 100 playback history entries
- **Corrupted file detection**: Automatically marks unplayable files

### System Integration

- **MPRIS2 support**: Complete D-Bus MPRIS2 interface
- **Desktop notifications**: Sends notifications on song changes
- **Global shortcuts**: Supports system media keys
- **Configuration persistence**: Automatically saves settings and state

### Highly Configurable

- **Key mappings**: Fully customizable all shortcuts
- **Playback modes**: Single loop, list loop, shuffle, memory mode
- **Startup behavior**: Configurable default page and auto-play
- **Image protocol**: Auto-detection or manual specification of terminal image protocol

## Installation

### Building from Source

1. **Install Go 1.25.4 or higher**

   ```bash
   # Arch Linux
   sudo pacman -S go
   ```

2. **Clone repository and build**

   ```bash
   git clone https://github.com/zyoung11/BM.git
   cd BM
   go build .
   ```

3. **Install to system path (optional)**

   ```bash
   sudo cp bm /usr/local/bin/
   ```

### Precompiled Binaries

Download the binary for your platform from the [Releases](https://github.com/yourusername/bm/releases) page, grant execute permissions, and you're ready to use it.

## Usage

### Basic Usage

```bash
# Start player (specify music library directory)
bm /path/to/music/library

# Play single audio file
bm /path/to/song.flac

# Start player (interactive library selection)
bm

# Show help information
bm help
```

### Operation Guide

#### Global Shortcuts
| Key | Function |
|------|------|
| `ESC` | Exit program |
| `TAB` | Cycle through pages |
| `1` | Switch to player page |
| `2` | Switch to playlist page |
| `3` | Switch to library page |

#### Player Page
| Key | Function |
|------|------|
| `Space` | Play/Pause |
| `E` / `L` | Fast forward 5 seconds |
| `Q` / `H` | Rewind 5 seconds |
| `W` / `↑` | Increase volume |
| `S` / `↓` | Decrease volume |
| `X` / `K` | Increase playback speed |
| `Z` / `J` | Decrease playback speed |
| `D` / `→` | Next song |
| `A` / `←` | Previous song |
| `R` | Toggle playback mode |
| `C` | Toggle text color (cover color/white) |
| `Backspace` | Reset volume and playback speed |

#### Library Page
| Key | Function |
|------|------|
| `K` / `W` / `↑` | Navigate up |
| `J` / `S` / `↓` | Navigate down |
| `L` / `D` / `→` | Enter directory |
| `H` / `A` / `←` | Exit directory |
| `Space` | Toggle selection of current item |
| `E` | Toggle selection of all items |
| `F` | Enter search mode |

#### Playlist Page
| Key | Function |
|------|------|
| `K` / `W` / `↑` | Navigate up |
| `J` / `S` / `↓` | Navigate down |
| `Space` | Remove song from playlist |
| `Enter` | Play selected song |
| `F` | Enter search mode |

#### Search Mode
| Key | Function |
|------|------|
| `Enter` | Confirm search |
| `ESC` | Exit search |
| `Backspace` | Delete search character |

## Configuration

Configuration file is located at `~/.config/BM/config.toml` and will be automatically created on first run.

### Key Mapping Configuration

The configuration file supports complete key mapping customization, supporting single keys or key lists. Refer to the generated default configuration file for detailed settings.

## Cover Support

### Cover Source Priority

1. Embedded cover in audio file
2. Image files in the same directory (preferentially selects files containing keywords like cover, folder, album)
3. Default cover image (`~/.config/BM/default.jpg`)

### Supported Image Formats

- JPEG/JPG
- PNG

### Terminal Image Protocols
- **Kitty**: Modern terminals (Kitty, WezTerm, Ghostty, Rio)
- **Sixel**: Traditional terminal support
- **iTerm2**: macOS iTerm2 terminal
- **Auto**: Automatically detects best available protocol

## MPRIS2 Integration

BM implements a complete MPRIS2 (Media Player Remote Interfacing Specification) interface, supporting:

- System media key control (Play/Pause/Next/Previous)
- Playback status synchronization
- Metadata transmission
- Volume control
- Playback position synchronization

## Project Structure

```
bm/
├── main.go              # Application entry and core logic
├── config.go            # Configuration management (TOML)
├── player.go            # Audio playback and UI rendering
├── library.go           # Media library browsing and management
├── playlist.go          # Playlist management
├── storage.go           # Persistent storage (JSON)
├── mpris.go             # MPRIS2 D-Bus interface
├── term_renderer.go     # Terminal image rendering
├── notification.go      # Desktop notifications
├── sixel.go             # Sixel image encoder
├── default_config.toml  # Default configuration file
├── default.jpg          # Default cover image
├── go.mod               # Go module definition
└── README.md            # Project documentation
```

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

Thanks to the following open source projects:

- [beep](https://github.com/gopxl/beep) - Go audio playback library
- [tag](https://github.com/dhowden/tag) - Audio metadata reading
- [dbus](https://github.com/godbus/dbus) - D-Bus Go bindings
- [go-term](https://golang.org/x/term) - Terminal control library

---

**BM** - Beautiful music playback experience for terminal users 🎶