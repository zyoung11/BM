package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"math/rand"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/dhowden/tag"
	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/effects"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/nfnt/resize"
	"golang.org/x/term"
)

// --- Page Implementation ---

// PlayerPage holds the state for the music player view.
type PlayerPage struct {
	app      *App
	flacPath string

	// UI state
	cellW, cellH                          int
	imageTop, imageHeight, imageRightEdge int
	coverColorR, coverColorG, coverColorB int
	useCoverColor                         bool
}

// NewPlayerPage creates a new instance of the player page.
func NewPlayerPage(app *App, flacPath string, cellW, cellH int) *PlayerPage {
	return &PlayerPage{
		app:           app,
		flacPath:      flacPath,
		useCoverColor: true, // Default to using cover color
		cellW:         cellW,
		cellH:         cellH,
	}
}

// Init for PlayerPage is now empty, as setup is done in the constructor.
func (p *PlayerPage) Init() {}

// UpdateSong 更新当前播放的歌曲路径
func (p *PlayerPage) UpdateSong(songPath string) {
	p.flacPath = songPath
	// 重置图像相关状态，强制重新加载专辑封面
	p.imageTop = 0
	p.imageHeight = 0
	p.imageRightEdge = 0
	// 不立即重新渲染，让Tick()方法在下一个周期自然更新
}

// HandleKey handles user key presses.
func (p *PlayerPage) HandleKey(key rune) (Page, error) {
	player := p.app.player
	mprisServer := p.app.mprisServer
	needsRedraw := true // Most keys will need a status update

	switch key {
	case '\x1b': // ESC to quit
		return nil, errors.New("user quit") // Signal to the main loop to exit
	case 'q', 'w': // Seek
		speaker.Lock()
		newPos := player.streamer.Position()
		if key == 'q' { // seek backward
			newPos -= player.sampleRate.N(time.Second * 5)
		} else { // 'w', seek forward
			newPos += player.sampleRate.N(time.Second * 5)
		}
		if newPos < 0 {
			newPos = 0
		}
		if newPos >= player.streamer.Len() {
			newPos = player.streamer.Len() - 1
		}
		if err := player.streamer.Seek(newPos); err != nil {
			// ignore seek errors
		}
		speaker.Unlock()
		if mprisServer != nil {
			mprisServer.UpdatePosition(p.currentPositionInMicroseconds())
		}
	case 'a', 's': // Volume
		speaker.Lock()
		if key == 'a' { // volume down
			player.volume.Volume -= 0.1
		} else { // 's', volume up
			player.volume.Volume += 0.1
		}
		speaker.Unlock()
		if mprisServer != nil {
			volume, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Volume")
			mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
				"Volume": volume.Value(),
			})
		}
	case ' ':
		speaker.Lock()
		player.ctrl.Paused = !player.ctrl.Paused
		speaker.Unlock()
		if mprisServer != nil {
			mprisServer.UpdatePlaybackStatus(!player.ctrl.Paused)
		}

	case 'z', 'x': // Rate change
		speaker.Lock()
		ratio := player.resampler.Ratio()
		if key == 'z' {
			ratio *= 15.0 / 16.0
		} else {
			ratio *= 16.0 / 15.0
		}
		player.resampler.SetRatio(min(max(ratio, 0.1), 4.0))
		speaker.Unlock()
		if mprisServer != nil {
			rate, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Rate")
			mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
				"Rate": rate.Value(),
			})
		}

	case 'e': // Toggle color
		p.useCoverColor = !p.useCoverColor

	case 'r': // Toggle play mode
		p.app.playMode = (p.app.playMode + 1) % 3

	default:
		needsRedraw = false // Unhandled keys don't need a redraw
	}

	if needsRedraw {
		p.updateStatus()
	}

	return nil, nil // Stay on this page
}

// HandleSignal handles system signals, like window resizing.
func (p *PlayerPage) HandleSignal(sig os.Signal) error {
	if sig == syscall.SIGWINCH {
		// Window size changed, we need to do a full redraw.
		p.View()
	}
	return nil
}

// View renders the player UI to the screen.
func (p *PlayerPage) View() {
	// 如果没有歌曲路径，显示空状态
	if p.flacPath == "" {
		p.displayEmptyState()
		return
	}

	// displayAlbumArt clears the screen and draws the art.
	p.imageTop, p.imageHeight, p.imageRightEdge, p.coverColorR, p.coverColorG, p.coverColorB = p.displayAlbumArt()
	// updateStatus draws the text and progress bar over it.
	p.updateStatus()
}

// displayEmptyState 显示播放列表为空的状态
func (p *PlayerPage) displayEmptyState() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H") // Clear screen

	// 标题
	title := "Player"
	titleX := (w - len(title)) / 2
	fmt.Printf("\x1b[1;%dH\x1b[1m%s\x1b[0m", titleX, title)

	// 空状态消息
	msg := "PlayList is empty"
	msg2 := "Add songs from the Library tab"
	msgX := (w - len(msg)) / 2
	msg2X := (w - len(msg2)) / 2
	centerRow := h / 2

	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", centerRow-1, msgX, msg)
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", centerRow+1, msg2X, msg2)

	// 页面切换提示
	footer := "Press Tab to switch to Library page"
	footerX := (w - len(footer)) / 2
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", h, footerX, footer)
}

// Tick is called periodically by the main loop to update dynamic elements.
func (p *PlayerPage) Tick() {
	// 如果没有歌曲，不需要更新
	if p.flacPath == "" {
		return
	}

	// We only need to update the status (progress bar, etc.), not the whole album art.
	p.updateStatus()

	// 检查歌曲是否结束，并根据播放模式处理下一首
	p.checkSongEndAndHandleNext()

	// Also, notify the MPRIS server of the position change.
	if p.app.mprisServer != nil {
		p.app.mprisServer.UpdatePosition(p.currentPositionInMicroseconds())
	}
}

// currentPositionInMicroseconds is a helper to get the player position for MPRIS.
func (p *PlayerPage) currentPositionInMicroseconds() int64 {
	if p.app.player == nil {
		return 0
	}
	pos := p.app.player.streamer.Position()
	return int64(float64(pos) / float64(p.app.player.sampleRate) * 1e6)
}

// checkSongEndAndHandleNext 检查歌曲是否结束，并根据播放模式处理下一首
func (p *PlayerPage) checkSongEndAndHandleNext() {
	if p.app.player == nil || len(p.app.Playlist) == 0 {
		return
	}

	// 检查歌曲是否结束（位置接近末尾）
	currentPos := p.app.player.streamer.Position()
	totalLen := p.app.player.streamer.Len()

	// 如果歌曲接近结束（最后1秒），根据播放模式处理
	if totalLen > 0 && currentPos >= totalLen-p.app.player.sampleRate.N(time.Second) {
		// 单曲循环模式：自动循环当前歌曲，不需要额外处理
		if p.app.playMode == 0 {
			return
		}

		// 列表循环或随机播放模式：播放下一首
		if p.app.playMode == 1 || p.app.playMode == 2 {
			p.playNextSong()
		}
	}
}

// playNextSong 根据播放模式播放下一首歌曲
func (p *PlayerPage) playNextSong() {
	if len(p.app.Playlist) == 0 {
		return
	}

	// 找到当前歌曲在播放列表中的位置
	currentIndex := -1
	for i, song := range p.app.Playlist {
		if song == p.flacPath {
			currentIndex = i
			break
		}
	}

	if currentIndex == -1 {
		return
	}

	var nextIndex int

	// 根据播放模式决定下一首
	switch p.app.playMode {
	case 1: // 列表循环
		nextIndex = (currentIndex + 1) % len(p.app.Playlist)
	case 2: // 随机播放
		nextIndex = rand.Intn(len(p.app.Playlist))
		// 避免随机到同一首歌
		if nextIndex == currentIndex && len(p.app.Playlist) > 1 {
			nextIndex = (nextIndex + 1) % len(p.app.Playlist)
		}
	default:
		return // 单曲循环不需要处理
	}

	// 播放下一首歌曲
	nextSong := p.app.Playlist[nextIndex]
	p.app.PlaySongWithSwitch(nextSong, false) // 不跳转页面，UpdateSong中会调用View()
}

// --- Audio Player (now just a data structure, no logic) ---

type audioPlayer struct {
	sampleRate beep.SampleRate
	streamer   beep.StreamSeeker
	ctrl       *beep.Ctrl
	resampler  *beep.Resampler
	volume     *effects.Volume
	position   int
}

func newAudioPlayer(streamer beep.StreamSeeker, format beep.Format) (*audioPlayer, error) {
	// 默认使用无限循环（单曲循环模式）
	loopStreamer := beep.Loop(-1, streamer)
	ctrl := &beep.Ctrl{Streamer: loopStreamer}
	resampler := beep.ResampleRatio(4, 1, ctrl)
	volume := &effects.Volume{Streamer: resampler, Base: 2}
	return &audioPlayer{format.SampleRate, streamer, ctrl, resampler, volume, 0}, nil
}

// --- TUI / Drawing ---
// All drawing functions are now methods on PlayerPage to access state.

func (p *PlayerPage) displayAlbumArt() (imageTop, imageHeight, imageRightEdge, coverColorR, coverColorG, coverColorB int) {
	// This function is very large. It remains mostly the same, but now it's a method.
	// We access flacPath and cell sizes via `p`.
	time.Sleep(50 * time.Millisecond) // This might be removed later for performance
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		fmt.Print("\x1b[2J\x1b[H")
		fmt.Println("无法获取终端尺寸")
		return 0, 0, 0, 255, 255, 255
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H") // Clear screen

	var finalImgH int
	var imageWidthInChars, imageHeightInChars int
	var startCol, startRow int
	var coverImg image.Image

	showNothing := w < 23 || h < 5
	showTextOnly := h < 13
	isWideTerminal := w >= 100 && (float64(w)/float64(h) > 2.0 || h < 20) && !showNothing && !showTextOnly

	f, err := os.Open(p.flacPath)
	if err == nil {
		defer f.Close()
		m, err := tag.ReadFrom(f)
		if err == nil {
			if pic := m.Picture(); pic != nil {
				if img, _, err := image.Decode(bytes.NewReader(pic.Data)); err == nil {
					coverImg = img
					var pixelW, pixelH int
					if showNothing || showTextOnly {
						pixelW, pixelH = 0, 0
					} else if isWideTerminal {
						pixelW = (w - 30) * p.cellW
						pixelH = (h - 1) * p.cellH
					} else {
						pixelW = w * p.cellW
						pixelH = (h - 2) * p.cellH
					}

					if pixelW < 10 {
						pixelW = 10
					}
					if pixelH < 10 {
						pixelH = 10
					}

					normalizedImg := resize.Resize(960, 960, img, resize.Lanczos3)
					scaledImg := resize.Thumbnail(uint(pixelW), uint(pixelH), normalizedImg, resize.Lanczos3)
					finalImgW, finalImgH := scaledImg.Bounds().Dx(), scaledImg.Bounds().Dy()

					if p.cellW == 0 {
						p.cellW = 1
					}
					if p.cellH == 0 {
						p.cellH = 1
					}

					imageWidthInChars = (finalImgW + p.cellW - 1) / p.cellW
					imageHeightInChars = (finalImgH + p.cellH - 1) / p.cellH

					if imageWidthInChars > w {
						imageWidthInChars = w
					}
					if imageHeightInChars > h {
						imageHeightInChars = h
					}

					title, artist, album := getSongMetadata(p.flacPath)
					maxTextLength := max(max(len(title), len(artist)), len(album))

					showNothing = w < 23 || h < 5
					showTextOnly = h < 13 && !showNothing
					showInfoOnly := (w < maxTextLength || h < 10) && !showNothing && !showTextOnly

					if showNothing || showTextOnly {
						startCol, startRow, imageWidthInChars, imageHeightInChars = 0, 0, 0, 0
					} else if showInfoOnly {
						startCol, startRow = (w-imageWidthInChars)/2, (h-imageHeightInChars)/2
					} else if isWideTerminal {
						startCol, startRow = 1, (h-imageHeightInChars)/2
					} else {
						startCol, startRow = (w-imageWidthInChars)/2, 2
					}

					if startCol < 1 {
						startCol = 1
					}
					if startRow < 1 {
						startRow = 1
					}
					if startCol+imageWidthInChars > w {
						imageWidthInChars = w - startCol
					}
					if startRow+imageHeightInChars > h {
						imageHeightInChars = h - startRow
					}

					if !showNothing && !showTextOnly {
						fmt.Printf("\x1b[%d;%dH", startRow, startCol)
						_ = NewEncoder(os.Stdout).Encode(scaledImg)
						if imageWidthInChars > 0 && startCol+imageWidthInChars <= w {
							fillStartCol := startCol + imageWidthInChars
							for row := startRow; row < startRow+imageHeightInChars; row++ {
								fmt.Printf("\x1b[%d;%dH\x1b[K", row, fillStartCol)
							}
						}
					}
				}
			}
		}
	}

	if coverImg != nil {
		r, g, b := analyzeCoverColor(coverImg)
		coverColorR, coverColorG, coverColorB = r, g, b
	} else {
		coverColorR, coverColorG, coverColorB = 255, 255, 255
	}

	imageRightEdgeVal := 0
	if isWideTerminal {
		imageRightEdgeVal = startCol + imageWidthInChars
	}
	if imageHeightInChars == 0 && finalImgH > 0 && p.cellH > 0 {
		imageHeightInChars = 1
	}

	return startRow, imageHeightInChars, imageRightEdgeVal, coverColorR, coverColorG, coverColorB
}

func (p *PlayerPage) updateStatus() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return
	}

	title, artist, album := getSongMetadata(p.flacPath)
	maxTextLength := max(max(len(title), len(artist)), len(album))

	showNothing := w < 23 || h < 5
	if showNothing {
		return
	}

	showTextOnly := h < 13 && !showNothing
	if showTextOnly {
		p.updateTextOnlyMode(w, h)
		return
	}

	showInfoOnly := (w < maxTextLength || h < 10) && !showNothing && !showTextOnly
	if showInfoOnly {
		return
	}

	isWideTerminal := w >= 100 && (float64(w)/float64(h) > 2.0 || h < 20)

	if isWideTerminal {
		if p.imageRightEdge > 0 && w-p.imageRightEdge >= 30 {
			p.updateRightPanel(w)
		}
	} else {
		imageBottomRow := p.imageTop + p.imageHeight
		if h-imageBottomRow >= 5 {
			p.updateBottomStatus(imageBottomRow, w, h)
		}
	}
}

func (p *PlayerPage) updateRightPanel(w int) {
	if p.imageHeight < 5 {
		return
	}

	title, artist, album := getSongMetadata(p.flacPath)

	texts := []string{title, artist, album}
	var totalLength int
	for _, text := range texts {
		totalLength += len(text)
	}
	avgLength := 0
	if len(texts) > 0 {
		avgLength = totalLength / len(texts)
	}

	availableWidth := w - p.imageRightEdge
	centerCol := p.imageRightEdge + availableWidth/2
	visualCenterCol := centerCol - avgLength/2
	if visualCenterCol < p.imageRightEdge+1 {
		visualCenterCol = p.imageRightEdge + 1
	}

	partHeight := p.imageHeight / 3
	artistRow := p.imageTop + partHeight + partHeight/2
	titleRow := artistRow - 1
	albumRow := artistRow + 1
	progressRow := p.imageTop + (2 * partHeight) + partHeight/2

	if progressRow-albumRow < 1 {
		return
	}
	if titleRow < p.imageTop {
		titleRow, artistRow, albumRow = p.imageTop, p.imageTop+1, p.imageTop+2
	}
	if progressRow >= p.imageTop+p.imageHeight {
		progressRow = p.imageTop + p.imageHeight - 1
	}

	colorCode := p.getColorCode()
	fmt.Printf("\x1b[%d;%dH\x1b[K%s\x1b[1m%s\x1b[0m", titleRow, visualCenterCol, colorCode, title)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", artistRow, visualCenterCol, colorCode, artist)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", albumRow, visualCenterCol, colorCode, album)

	progressBarStartCol := p.imageRightEdge + 5
	progressBarWidth := w - progressBarStartCol - 1
	if progressBarWidth < 10 {
		return
	}

	p.drawProgressBar(progressRow, progressBarStartCol, progressBarWidth, colorCode)
}

func (p *PlayerPage) updateBottomStatus(startRow, w, h int) {
	title, artist, album := getSongMetadata(p.flacPath)
	availableRows := h - startRow
	infoRow := startRow + availableRows/3
	progressRow := startRow + 2*availableRows/3 + (h-(startRow+2*availableRows/3))/2
	centerCol := w / 2

	colorCode := p.getColorCode()
	fmt.Printf("\x1b[%d;%dH\x1b[K%s\x1b[1m%s\x1b[0m", infoRow, centerCol-len(title)/2, colorCode, title)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", infoRow+1, centerCol-len(artist)/2, colorCode, artist)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", infoRow+2, centerCol-len(album)/2, colorCode, album)

	progressBarStartCol := 5
	progressBarWidth := w - 10
	if progressBarWidth < 10 {
		progressBarWidth = 10
	}

	p.drawProgressBar(progressRow, progressBarStartCol, progressBarWidth, colorCode)
}

func (p *PlayerPage) updateTextOnlyMode(w, h int) {
	title, artist, album := getSongMetadata(p.flacPath)
	centerRow, centerCol := h/2, w/2

	colorCode := p.getColorCode()
	fmt.Printf("\x1b[%d;%dH\x1b[K%s\x1b[1m%s\x1b[0m", centerRow-1, centerCol-len(title)/2, colorCode, title)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", centerRow, centerCol-len(artist)/2, colorCode, artist)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", centerRow+1, centerCol-len(album)/2, colorCode, album)

	progressBarStartCol := 5
	progressBarWidth := w - 10
	if progressBarWidth < 10 {
		progressBarWidth = 10
	}
	progressRow := centerRow + 3

	p.drawProgressBar(progressRow, progressBarStartCol, progressBarWidth, colorCode)
}

func (p *PlayerPage) drawProgressBar(row, startCol, width int, colorCode string) {
	// 检查player是否可用
	if p.app.player == nil {
		return
	}

	currentPos := p.app.player.streamer.Position()
	totalLen := p.app.player.streamer.Len()
	progress := 0.0
	if totalLen > 0 {
		progress = float64(currentPos) / float64(totalLen)
	}

	playedChars := int(float64(width) * progress)

	// 根据播放状态和播放模式显示图标
	icon := "⏸"
	if p.app.player.ctrl.Paused {
		icon = "▶"
	}

	// 播放模式图标
	modeIcon := "⟳" // 默认单曲循环
	switch p.app.playMode {
	case 1:
		modeIcon = "⇆" // 列表循环
	case 2:
		modeIcon = "⤮" // 随机播放
	}

	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s", row, startCol-2, colorCode, icon)

	var bar string
	if playedChars > 0 {
		bar += fmt.Sprintf("\x1b[2m%s", colorCode) // Dim played part
		for i := 0; i < playedChars; i++ {
			bar += "━"
		}
		bar += "\x1b[0m"
	}
	bar += colorCode // Unplayed part
	for i := playedChars; i < width; i++ {
		bar += "━"
	}

	fmt.Printf("\x1b[0m\x1b[%d;%dH%s", row, startCol, bar)
	fmt.Printf("\x1b[0m\x1b[%d;%dH\x1b[K%s%s\x1b[0m", row, startCol+width+1, colorCode, modeIcon)
}

func (p *PlayerPage) getColorCode() string {
	if p.useCoverColor {
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", p.coverColorR, p.coverColorG, p.coverColorB)
	}
	return "\x1b[37m" // White
}

// --- Misc Helper Functions ---

func getCellSize() (width, height int, err error) {
	// This function remains unchanged.
	fmt.Print("\x1b[16t")
	var buf []byte
	var b [1]byte
	for {
		n, err := os.Stdin.Read(b[:])
		if err != nil {
			return 0, 0, err
		}
		if n == 0 {
			continue
		}
		buf = append(buf, b[0])
		if b[0] == 't' {
			break
		}
	}
	if !(len(buf) > 2 && buf[0] == '\x1b' && buf[1] == '[' && buf[len(buf)-1] == 't') {
		return 0, 0, fmt.Errorf("无法解析的终端响应格式: %q", buf)
	}
	content := buf[2 : len(buf)-1]
	parts := bytes.Split(content, []byte(";"))
	if len(parts) != 3 {
		return 0, 0, fmt.Errorf("预期的响应分段为3, 实际为 %d: %q", len(parts), buf)
	}
	if string(parts[0]) != "6" {
		return 0, 0, fmt.Errorf("预期的响应代码为 6, 实际为 %s", parts[0])
	}
	h, err := strconv.Atoi(string(parts[1]))
	if err != nil {
		return 0, 0, err
	}
	w, err := strconv.Atoi(string(parts[2]))
	if err != nil {
		return 0, 0, err
	}
	return w, h, nil
}

func getSongMetadata(flacPath string) (title, artist, album string) {
	// This function remains unchanged.
	f, err := os.Open(flacPath)
	if err != nil {
		return "未知", "未知", "未知"
	}
	defer f.Close()
	m, err := tag.ReadFrom(f)
	if err != nil {
		return "未知", "未知", "未知"
	}
	title, artist, album = m.Title(), m.Artist(), m.Album()
	if title == "" {
		title = "未知"
	}
	if artist == "" {
		artist = "未知"
	}
	if album == "" {
		album = "未知"
	}
	return title, artist, album
}

func analyzeCoverColor(img image.Image) (r, g, b int) {
	// This function remains unchanged.
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pr, pg, pb, _ := img.At(x, y).RGBA()
			r8, g8, b8 := int(pr>>8), int(pg>>8), int(pb>>8)
			brightness := 0.2126*float64(r8) + 0.7152*float64(g8) + 0.0722*float64(b8)
			isBright := brightness > 160
			isNotGray := math.Abs(float64(r8)-float64(g8)) > 25 || math.Abs(float64(g8)-float64(b8)) > 25
			isNotWhite := !(r8 > 220 && g8 > 220 && b8 > 220)
			if isBright && isNotGray && isNotWhite {
				return r8, g8, b8
			}
		}
	}
	return 255, 255, 255
}

func max[T ~int | ~float64](a, b T) T {
	if a > b {
		return a
	}
	return b
}

func min[T ~int | ~float64](a, b T) T {
	if a < b {
		return a
	}
	return b
}
