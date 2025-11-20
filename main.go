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
	loopStreamer := beep.Loop(-1, streamer) // -1 表示无限循环
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

// drawLayout 清空屏幕并绘制静态部分 (封面)
// 返回播放器状态UI应该开始绘制的行号
func drawLayout(flacPath string, cellW, cellH int) (statusRow int) {
	time.Sleep(50 * time.Millisecond)
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		fmt.Print("\x1b[2J\x1b[H")
		fmt.Println("无法获取终端尺寸")
		return h - 5 // 返回一个默认位置
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H")

	var finalImgH int
	f, err := os.Open(flacPath)
	if err == nil {
		defer f.Close()
		m, err := tag.ReadFrom(f)
		if err == nil {
			if pic := m.Picture(); pic != nil {
				if img, _, err := image.Decode(bytes.NewReader(pic.Data)); err == nil {
					pixelW := w * cellW
					safePixelW := int(float64(pixelW) * 0.99)
					pixelH := (h - 1) * cellH
					if pixelH < 0 {
						pixelH = 0
					}
					scaledImg := resize.Thumbnail(uint(safePixelW), uint(pixelH), img, resize.Lanczos3)
					finalImgW := scaledImg.Bounds().Dx()
					finalImgH = scaledImg.Bounds().Dy()
					imageWidthInChars := finalImgW / cellW
					imageHeightInChars := finalImgH / cellH
					startCol, startRow := 1, 1
					if w < 2*imageWidthInChars {
						// 确保绝对居中：如果宽度差是奇数，微调图片宽度
						if (w-imageWidthInChars)%2 != 0 {
							imageWidthInChars--
						}
						startCol = (w - imageWidthInChars) / 2
					} else {
						availableRows := h - 1
						startRow = (availableRows - imageHeightInChars) / 2
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

	// 计算并返回状态UI的起始行
	// 由于不显示文字，返回一个不影响的值
	return h
}

// updateStatus 只更新屏幕上动态的部分 (播放器状态)
func updateStatus(startRow int, player *audioPlayer) {
	// 不显示任何文字，只保留函数结构用于维持程序逻辑
}

// --- Main Entrypoint ---

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("用法: %s <music.flac>", os.Args[0])
	}
	flacPath := os.Args[1]

	f, err := os.Open(flacPath)
	if err != nil {
		log.Fatalf("打开文件失败: %v", err)
	}
	streamer, format, err := flac.Decode(f)
	if err != nil {
		log.Fatalf("解码FLAC失败: %v", err)
	}
	speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/30))
	player, err := newAudioPlayer(streamer, format)
	if err != nil {
		log.Fatalf("创建播放器失败: %v", err)
	}

	fmt.Print("\x1b[?1049h\x1b[?25l")
	defer fmt.Print("\x1b[?1049l\x1b[?25h")

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		log.Fatalf("设置原始模式失败: %v", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	cellW, cellH, err := getCellSize()
	if err != nil {
		log.Fatalf("终端不支持尺寸查询: %v", err)
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
	var statusRow int
	statusRow = drawLayout(flacPath, cellW, cellH)
	updateStatus(statusRow, player)

	for {
		select {
		case key := <-keyCh:
			needsUpdate := true
			switch key {
			case '\x1b': // ESC
				return
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
				updateStatus(statusRow, player)
			}

		case sig := <-sigCh:
			if sig == syscall.SIGINT {
				return
			}
			if sig == syscall.SIGWINCH {
				statusRow = drawLayout(flacPath, cellW, cellH)
				updateStatus(statusRow, player)
			}

		case <-ticker.C:
			updateStatus(statusRow, player)
		}
	}
}
