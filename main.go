// main.go
package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // 注册 JPEG 解码
	_ "image/png"  // 注册 PNG 解码
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/dhowden/tag"
	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/effects"
	"github.com/gopxl/beep/v2/flac"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/mattn/go-sixel"
	"github.com/nfnt/resize"
	"golang.org/x/term"
)

// --- Helper Functions ---

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

// --- Audio Player ---

type audioPlayer struct {
	sampleRate beep.SampleRate
	streamer   beep.StreamSeeker
	ctrl       *beep.Ctrl
	resampler  *beep.Resampler
	volume     *effects.Volume
}

func newAudioPlayer(streamer beep.StreamSeeker, format beep.Format) (*audioPlayer, error) {
	loopStreamer, _ := beep.Loop2(streamer) // 无限循环
	ctrl := &beep.Ctrl{Streamer: loopStreamer}
	resampler := beep.ResampleRatio(4, 1, ctrl)
	volume := &effects.Volume{Streamer: resampler, Base: 2}
	return &audioPlayer{format.SampleRate, streamer, ctrl, resampler, volume}, nil
}

// --- TUI / Drawing ---

func getCellSize() (width, height int, err error) {
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
		return 0, 0, fmt.Errorf("无法解析高度: %w", err)
	}
	w, err := strconv.Atoi(string(parts[2]))
	if err != nil {
		return 0, 0, fmt.Errorf("无法解析宽度: %w", err)
	}
	return w, h, nil
}

// displayAlbumArt 显示专辑封面
// 输入: flacPath - 歌曲路径, cellW, cellH - 终端单元格宽高
// 输出: statusRow - 状态行位置, imageRightEdge - 图片右边界位置
func displayAlbumArt(flacPath string, cellW, cellH int) (statusRow int, imageRightEdge int) {
	time.Sleep(50 * time.Millisecond)
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		fmt.Print("\x1b[2J\x1b[H")
		fmt.Println("无法获取终端尺寸")
		return h - 5, 0 // 返回一个默认位置
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H")

	var finalImgH int
	var imageWidthInChars, imageHeightInChars int
	var startCol, startRow int

	// 判断布局模式
	isWideTerminal := w >= 80 // 假设80字符以上为宽终端

	f, err := os.Open(flacPath)
	if err == nil {
		defer f.Close()
		m, err := tag.ReadFrom(f)
		if err == nil {
			if pic := m.Picture(); pic != nil {
				if img, _, err := image.Decode(bytes.NewReader(pic.Data)); err == nil {
					// 根据布局模式调整图片尺寸
					var pixelW, pixelH int
					if isWideTerminal {
						// 宽终端：左侧图片，右侧信息栏
						pixelW = (w - 30) * cellW // 预留30字符给信息栏
						pixelH = (h - 1) * cellH
					} else {
						// 窄终端：顶部图片，底部状态栏
						pixelW = w * cellW
						pixelH = (h - 2) * cellH // 预留2行给状态栏
					}

					safePixelW := int(float64(pixelW) * 0.99)
					if pixelH < 0 {
						pixelH = 0
					}

					scaledImg := resize.Thumbnail(uint(safePixelW), uint(pixelH), img, resize.Lanczos3)
					finalImgW := scaledImg.Bounds().Dx()
					finalImgH = scaledImg.Bounds().Dy()
					imageWidthInChars = finalImgW / cellW
					imageHeightInChars = finalImgH / cellH

					// 计算图片位置
					if isWideTerminal {
						// 宽终端：图片在左侧
						startCol = 1
						startRow = (h - imageHeightInChars) / 2
					} else {
						// 窄终端：图片在顶部居中
						startCol = (w - imageWidthInChars) / 2
						// 确保绝对居中
						if (w-imageWidthInChars)%2 != 0 {
							imageWidthInChars--
							startCol = (w - imageWidthInChars) / 2
						}
						startRow = 1
					}

					if startCol < 1 {
						startCol = 1
					}
					if startRow < 1 {
						startRow = 1
					}

					fmt.Printf("\x1b[%d;%dH", startRow, startCol)
					_ = sixel.NewEncoder(os.Stdout).Encode(scaledImg)
				}
			}
		}
	}

	// 返回状态信息显示位置和图片右边界
	if isWideTerminal {
		// 宽终端：返回图片右边界位置
		imageRightEdge := startCol + imageWidthInChars
		return imageRightEdge, imageRightEdge
	} else {
		// 窄终端：返回图片底部位置
		imageBottomRow := startRow + imageHeightInChars
		return imageBottomRow, 0
	}
}

// updateStatus 更新屏幕上动态的部分 (播放器状态和信息)
func updateStatus(startRow int, player *audioPlayer, flacPath string, imageRightEdge int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return
	}

	// 判断布局模式
	isWideTerminal := w >= 80

	if isWideTerminal {
		// 宽终端：右侧信息栏
		updateRightPanel(imageRightEdge, player, w, h, flacPath)
	} else {
		// 窄终端：底部状态栏
		updateBottomStatus(startRow, player, w, h, flacPath)
	}
}

// updateRightPanel 更新右侧信息面板
func updateRightPanel(imageRightEdge int, _ *audioPlayer, w, h int, flacPath string) {
	// 获取歌曲元数据
	title, artist, album := getSongMetadata(flacPath)

	// 计算文本的平均长度
	texts := []string{title, artist, album}
	var totalLength int
	for _, text := range texts {
		totalLength += len(text)
	}
	avgLength := totalLength / len(texts)

	// 计算信息显示的中心位置（图片右边界到终端右边界的中间）
	availableWidth := w - imageRightEdge
	centerCol := imageRightEdge + availableWidth/2

	// 向左偏移一半的平均长度，实现视觉居中
	visualCenterCol := centerCol - avgLength/2
	if visualCenterCol < imageRightEdge {
		visualCenterCol = imageRightEdge
	}

	// 计算垂直居中位置
	infoHeight := 3 // 只显示3行信息
	startRow := (h - infoHeight) / 2
	if startRow < 1 {
		startRow = 1
	}

	// 显示简约的歌曲信息
	fmt.Printf("\x1b[%d;%dH\x1b[1m%s\x1b[0m", startRow, visualCenterCol, title)
	fmt.Printf("\x1b[%d;%dH%s", startRow+1, visualCenterCol, artist)
	fmt.Printf("\x1b[%d;%dH%s", startRow+2, visualCenterCol, album)
}

// updateBottomStatus 更新底部状态栏
func updateBottomStatus(startRow int, _ *audioPlayer, w, h int, flacPath string) {
	// 获取歌曲元数据
	title, artist, album := getSongMetadata(flacPath)

	// 计算垂直居中位置（在图片下方的空间中居中）
	// startRow 是图片底部位置，我们在这个空间内垂直居中显示
	availableRows := h - startRow
	infoHeight := 3 // 只显示3行信息
	startDisplayRow := startRow + (availableRows-infoHeight)/2
	if startDisplayRow < startRow {
		startDisplayRow = startRow
	}
	if startDisplayRow+infoHeight > h {
		startDisplayRow = h - infoHeight
	}

	// 每行文字各自居中对齐
	centerCol := w / 2

	// 显示简约的歌曲信息（每行单独计算居中位置）
	titleCol := centerCol - len(title)/2
	if titleCol < 1 {
		titleCol = 1
	}
	fmt.Printf("\x1b[%d;%dH\x1b[1m%s\x1b[0m", startDisplayRow, titleCol, title)

	artistCol := centerCol - len(artist)/2
	if artistCol < 1 {
		artistCol = 1
	}
	fmt.Printf("\x1b[%d;%dH%s", startDisplayRow+1, artistCol, artist)

	albumCol := centerCol - len(album)/2
	if albumCol < 1 {
		albumCol = 1
	}
	fmt.Printf("\x1b[%d;%dH%s", startDisplayRow+2, albumCol, album)
}

// getSongMetadata 获取歌曲元数据
func getSongMetadata(flacPath string) (title, artist, album string) {
	f, err := os.Open(flacPath)
	if err != nil {
		return "未知", "未知", "未知"
	}
	defer f.Close()

	m, err := tag.ReadFrom(f)
	if err != nil {
		return "未知", "未知", "未知"
	}

	title = m.Title()
	if title == "" {
		title = "未知"
	}
	artist = m.Artist()
	if artist == "" {
		artist = "未知"
	}
	album = m.Album()
	if album == "" {
		album = "未知"
	}

	return title, artist, album
}

// playMusic 播放音乐并处理用户交互
// 输入: flacPath - 歌曲路径
// 输出: error - 错误信息
func playMusic(flacPath string) error {
	f, err := os.Open(flacPath)
	if err != nil {
		return fmt.Errorf("打开文件失败: %v", err)
	}
	streamer, format, err := flac.Decode(f)
	if err != nil {
		return fmt.Errorf("解码FLAC失败: %v", err)
	}
	speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/30))
	player, err := newAudioPlayer(streamer, format)
	if err != nil {
		return fmt.Errorf("创建播放器失败: %v", err)
	}

	fmt.Print("\x1b[?1049h\x1b[?25l")
	defer fmt.Print("\x1b[?1049l\x1b[?25h")

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("设置原始模式失败: %v", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	cellW, cellH, err := getCellSize()
	if err != nil {
		return fmt.Errorf("终端不支持尺寸查询: %v", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH, syscall.SIGINT)
	defer signal.Stop(sigCh)

	keyCh := make(chan byte, 1)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			if n > 0 {
				keyCh <- buf[0]
			}
		}
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	speaker.Play(player.volume)

	// --- 主循环 ---
	var statusRow, imageRightEdge int
	statusRow, imageRightEdge = displayAlbumArt(flacPath, cellW, cellH)
	updateStatus(statusRow, player, flacPath, imageRightEdge)

	for {
		select {
		case key := <-keyCh:
			needsUpdate := true
			switch key {
			case '\x1b': // ESC
				return nil
			case ' ':
				speaker.Lock()
				player.ctrl.Paused = !player.ctrl.Paused
				speaker.Unlock()
			case 'q', 'w':
				speaker.Lock()
				newPos := player.streamer.Position()
				if key == 'q' {
					newPos -= player.sampleRate.N(time.Second * 5)
				} else {
					newPos += player.sampleRate.N(time.Second * 5)
				}
				if newPos < 0 {
					newPos = 0
				}
				if newPos >= player.streamer.Len() {
					newPos = player.streamer.Len() - 1
				}
				if err := player.streamer.Seek(newPos); err != nil {
					// log error?
				}
				speaker.Unlock()
			case 'a', 's':
				speaker.Lock()
				if key == 'a' {
					player.volume.Volume -= 0.1
				} else {
					player.volume.Volume += 0.1
				}
				speaker.Unlock()
			case 'z', 'x':
				speaker.Lock()
				ratio := player.resampler.Ratio()
				if key == 'z' {
					ratio *= 15.0 / 16.0
				} else {
					ratio *= 16.0 / 15.0
				}
				player.resampler.SetRatio(min(max(ratio, 0.1), 4.0))
				speaker.Unlock()
			default:
				needsUpdate = false
			}
			if needsUpdate {
				updateStatus(statusRow, player, flacPath, imageRightEdge)
			}

		case sig := <-sigCh:
			if sig == syscall.SIGINT {
				return nil
			}
			if sig == syscall.SIGWINCH {
				statusRow, imageRightEdge = displayAlbumArt(flacPath, cellW, cellH)
				updateStatus(statusRow, player, flacPath, imageRightEdge)
			}

		case <-ticker.C:
			updateStatus(statusRow, player, flacPath, imageRightEdge)
		}
	}
}

// --- Main Entrypoint ---

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("用法: %s <music.flac>", os.Args[0])
	}
	flacPath := os.Args[1]

	if err := playMusic(flacPath); err != nil {
		log.Fatalf("播放失败: %v", err)
	}
}
