package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"math"
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

// --- Page Management ---

type PageType int

const (
	PageMain   PageType = iota // 主播放页面
	PageLyrics                 // 歌词页面
	PageInfo                   // 歌曲信息页面
	PageStats                  // 统计信息页面
)

// pageState 管理页面状态
type pageState struct {
	currentPage PageType
	pages       map[PageType]func() // 页面渲染函数
}

// newPageState 创建新的页面状态管理器
func newPageState() *pageState {
	return &pageState{
		currentPage: PageMain,
		pages:       make(map[PageType]func()),
	}
}

// nextPage 切换到下一个页面
func (ps *pageState) nextPage() {
	ps.currentPage = (ps.currentPage + 1) % 4 // 循环切换4个页面
}

// renderCurrentPage 渲染当前页面
func (ps *pageState) renderCurrentPage() {
	if renderFunc, exists := ps.pages[ps.currentPage]; exists {
		renderFunc()
	}
}

// renderMainPage 渲染主播放页面
func renderMainPage(imageTop, imageHeight int, player *audioPlayer, flacPath string, imageRightEdge, coverColorR, coverColorG, coverColorB int, useCoverColor bool) {
	updateStatus(imageTop, imageHeight, player, flacPath, imageRightEdge, coverColorR, coverColorG, coverColorB, useCoverColor)
}

// renderLyricsPage 渲染歌词页面
func renderLyricsPage(player *audioPlayer, flacPath string, coverColorR, coverColorG, coverColorB int, useCoverColor bool) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return
	}

	// 清屏
	fmt.Print("\x1b[2J\x1b[H")

	// 显示页面标题
	var colorCode string
	if useCoverColor {
		colorCode = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", coverColorR, coverColorG, coverColorB)
	} else {
		colorCode = "\x1b[37m" // 白色
	}

	// 页面标题
	title := "歌词页面"
	titleCol := w/2 - len(title)/2
	if titleCol < 1 {
		titleCol = 1
	}
	fmt.Printf("\x1b[2;%dH%s\x1b[1m%s\x1b[0m", titleCol, colorCode, title)

	// 显示歌曲信息
	songTitle, artist, album := getSongMetadata(flacPath)
	infoLine := fmt.Sprintf("%s - %s - %s", songTitle, artist, album)
	infoCol := w/2 - len(infoLine)/2
	if infoCol < 1 {
		infoCol = 1
	}
	fmt.Printf("\x1b[4;%dH%s%s\x1b[0m", infoCol, colorCode, infoLine)

	// 显示歌词占位符
	lyricsText := "暂无歌词"
	lyricsCol := w/2 - len(lyricsText)/2
	lyricsRow := h / 2
	if lyricsCol < 1 {
		lyricsCol = 1
	}
	fmt.Printf("\x1b[%d;%dH%s%s\x1b[0m", lyricsRow, lyricsCol, colorCode, lyricsText)

	// 显示播放状态
	statusText := "播放中"
	if player.ctrl.Paused {
		statusText = "已暂停"
	}
	statusCol := w/2 - len(statusText)/2
	statusRow := h - 3
	if statusCol < 1 {
		statusCol = 1
	}
	fmt.Printf("\x1b[%d;%dH%s%s\x1b[0m", statusRow, statusCol, colorCode, statusText)

	// 显示页面指示器
	pageIndicator := "[歌词页面] 按Tab切换页面"
	indicatorCol := w/2 - len(pageIndicator)/2
	indicatorRow := h - 1
	if indicatorCol < 1 {
		indicatorCol = 1
	}
	fmt.Printf("\x1b[%d;%dH%s\x1b[0m", indicatorRow, indicatorCol, pageIndicator)
}

// renderInfoPage 渲染歌曲信息页面
func renderInfoPage(flacPath string, coverColorR, coverColorG, coverColorB int, useCoverColor bool) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return
	}

	// 清屏
	fmt.Print("\x1b[2J\x1b[H")

	// 显示页面标题
	var colorCode string
	if useCoverColor {
		colorCode = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", coverColorR, coverColorG, coverColorB)
	} else {
		colorCode = "\x1b[37m" // 白色
	}

	// 页面标题
	title := "歌曲信息"
	titleCol := w/2 - len(title)/2
	if titleCol < 1 {
		titleCol = 1
	}
	fmt.Printf("\x1b[2;%dH%s\x1b[1m%s\x1b[0m", titleCol, colorCode, title)

	// 获取并显示详细的歌曲信息
	songTitle, artist, album := getSongMetadata(flacPath)

	// 显示详细信息
	infoStartRow := h/2 - 2

	infoLines := []string{
		fmt.Sprintf("标题: %s", songTitle),
		fmt.Sprintf("艺术家: %s", artist),
		fmt.Sprintf("专辑: %s", album),
		fmt.Sprintf("文件: %s", flacPath),
	}

	for i, line := range infoLines {
		lineCol := w/2 - len(line)/2
		if lineCol < 1 {
			lineCol = 1
		}
		fmt.Printf("\x1b[%d;%dH%s%s\x1b[0m", infoStartRow+i, lineCol, colorCode, line)
	}

	// 显示页面指示器
	pageIndicator := "[信息页面] 按Tab切换页面"
	indicatorCol := w/2 - len(pageIndicator)/2
	indicatorRow := h - 1
	if indicatorCol < 1 {
		indicatorCol = 1
	}
	fmt.Printf("\x1b[%d;%dH%s\x1b[0m", indicatorRow, indicatorCol, pageIndicator)
}

// renderStatsPage 渲染统计信息页面
func renderStatsPage(player *audioPlayer, flacPath string, coverColorR, coverColorG, coverColorB int, useCoverColor bool) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return
	}

	// 清屏
	fmt.Print("\x1b[2J\x1b[H")

	// 显示页面标题
	var colorCode string
	if useCoverColor {
		colorCode = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", coverColorR, coverColorG, coverColorB)
	} else {
		colorCode = "\x1b[37m" // 白色
	}

	// 页面标题
	title := "统计信息"
	titleCol := w/2 - len(title)/2
	if titleCol < 1 {
		titleCol = 1
	}
	fmt.Printf("\x1b[2;%dH%s\x1b[1m%s\x1b[0m", titleCol, colorCode, title)

	// 计算统计信息
	currentPos := player.streamer.Position()
	totalLen := player.streamer.Len()
	progress := 0.0
	if totalLen > 0 {
		progress = float64(currentPos) / float64(totalLen)
	}

	// 显示统计信息
	infoStartRow := h/2 - 2

	infoLines := []string{
		fmt.Sprintf("播放进度: %.1f%%", progress*100),
		fmt.Sprintf("当前位置: %d 样本", currentPos),
		fmt.Sprintf("总长度: %d 样本", totalLen),
		fmt.Sprintf("采样率: %d Hz", player.sampleRate),
		fmt.Sprintf("音量: %.1f", player.volume.Volume),
		fmt.Sprintf("播放速率: %.2f", player.resampler.Ratio()),
	}

	for i, line := range infoLines {
		lineCol := w/2 - len(line)/2
		if lineCol < 1 {
			lineCol = 1
		}
		fmt.Printf("\x1b[%d;%dH%s%s\x1b[0m", infoStartRow+i, lineCol, colorCode, line)
	}

	// 显示页面指示器
	pageIndicator := "[统计页面] 按Tab切换页面"
	indicatorCol := w/2 - len(pageIndicator)/2
	indicatorRow := h - 1
	if indicatorCol < 1 {
		indicatorCol = 1
	}
	fmt.Printf("\x1b[%d;%dH%s\x1b[0m", indicatorRow, indicatorCol, pageIndicator)
}

// --- Audio Player ---

type audioPlayer struct {
	sampleRate beep.SampleRate
	streamer   beep.StreamSeeker // 原始 streamer，用于时长计算
	ctrl       *beep.Ctrl
	resampler  *beep.Resampler
	volume     *effects.Volume
	position   int // 当前播放位置（样本数）
}

func newAudioPlayer(streamer beep.StreamSeeker, format beep.Format) (*audioPlayer, error) {
	loopStreamer, _ := beep.Loop2(streamer) // 无限循环
	ctrl := &beep.Ctrl{Streamer: loopStreamer}
	resampler := beep.ResampleRatio(4, 1, ctrl)
	volume := &effects.Volume{Streamer: resampler, Base: 2}
	return &audioPlayer{format.SampleRate, streamer, ctrl, resampler, volume, 0}, nil
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
// 输出: imageTop, imageHeight, imageRightEdge, coverColorR, coverColorG, coverColorB
func displayAlbumArt(flacPath string, cellW, cellH int) (imageTop, imageHeight, imageRightEdge, coverColorR, coverColorG, coverColorB int) {
	time.Sleep(50 * time.Millisecond)
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		fmt.Print("\x1b[2J\x1b[H")
		fmt.Println("无法获取终端尺寸")
		return 0, 0, 0, 255, 255, 255 // 返回默认值
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H")

	var finalImgH int
	var imageWidthInChars, imageHeightInChars int
	var startCol, startRow int
	var coverImg image.Image

	// 判断布局模式
	// 首先检查是否应该什么都不显示
	showNothing := w < 23 || h < 5
	// 然后检查是否应该只显示文本和进度条
	showTextOnly := h < 13
	// 最后判断宽终端模式
	isWideTerminal := w >= 100 && (float64(w)/float64(h) > 2.0 || h < 20) && !showNothing && !showTextOnly

	f, err := os.Open(flacPath)
	if err == nil {
		defer f.Close()
		m, err := tag.ReadFrom(f)
		if err == nil {
			if pic := m.Picture(); pic != nil {
				if img, _, err := image.Decode(bytes.NewReader(pic.Data)); err == nil {
					// 保存原始图片用于颜色分析
					coverImg = img

					// 根据布局模式调整图片尺寸
					var pixelW, pixelH int
					if showNothing || showTextOnly {
						// 什么都不显示或只显示文本模式：不计算图片尺寸
						pixelW = 0
						pixelH = 0
					} else if isWideTerminal {
						// 宽终端：左侧图片，右侧信息栏
						pixelW = (w - 30) * cellW // 预留30字符给信息栏
						pixelH = (h - 1) * cellH
					} else {
						// 窄终端或只显示照片模式：顶部图片，底部状态栏
						pixelW = w * cellW
						pixelH = (h - 2) * cellH // 预留2行给状态栏
					}

					// 确保目标尺寸合理
					if pixelW < 10 {
						pixelW = 10
					}
					if pixelH < 10 {
						pixelH = 10
					}

					// 先将所有图片统一转换为960x960分辨率
					originalBounds := img.Bounds()
					originalWidth := originalBounds.Dx()
					originalHeight := originalBounds.Dy()

					// 如果图片不是960x960，先转换为960x960
					var normalizedImg image.Image
					if originalWidth != 960 || originalHeight != 960 {
						// 使用Lanczos3算法保持高质量缩放
						normalizedImg = resize.Resize(960, 960, img, resize.Lanczos3)
					} else {
						normalizedImg = img
					}

					// 然后根据终端尺寸进行最终缩放
					scaledImg := resize.Thumbnail(uint(pixelW), uint(pixelH), normalizedImg, resize.Lanczos3)
					finalImgW := scaledImg.Bounds().Dx()
					finalImgH = scaledImg.Bounds().Dy()

					// 防止除以零错误
					if cellW == 0 {
						cellW = 1
					}
					if cellH == 0 {
						cellH = 1
					}

					// 精确计算字符尺寸，考虑可能的余数
					imageWidthInChars = (finalImgW + cellW - 1) / cellW
					imageHeightInChars = (finalImgH + cellH - 1) / cellH

					// 确保尺寸不会超出终端
					if imageWidthInChars > w {
						imageWidthInChars = w
					}
					if imageHeightInChars > h {
						imageHeightInChars = h
					}

					// 获取歌曲信息来计算最长文本长度
					title, artist, album := getSongMetadata(flacPath)
					maxTextLength := max(max(len(title), len(artist)), len(album))

					// 判断显示模式 - 按优先级顺序判断
					// 1. 什么都不显示模式（最高优先级）
					showNothing := w < 23 || h < 5
					// 2. 只显示文本模式
					showTextOnly := h < 13 && !showNothing
					// 3. 只显示照片模式
					showInfoOnly := (w < maxTextLength || h < 10) && !showNothing && !showTextOnly

					// 计算图片位置
					if showNothing {
						// 什么都不显示模式：不显示图片
						startCol = 0
						startRow = 0
						imageWidthInChars = 0
						imageHeightInChars = 0
					} else if showTextOnly {
						// 只显示文本模式：不显示图片
						startCol = 0
						startRow = 0
						imageWidthInChars = 0
						imageHeightInChars = 0
					} else if showInfoOnly {
						// 只显示照片模式：完全居中
						startCol = (w - imageWidthInChars) / 2
						startRow = (h - imageHeightInChars) / 2
						// 确保绝对居中
						if (w-imageWidthInChars)%2 != 0 {
							imageWidthInChars--
							startCol = (w - imageWidthInChars) / 2
						}
						if (h-imageHeightInChars)%2 != 0 {
							imageHeightInChars--
							startRow = (h - imageHeightInChars) / 2
						}
					} else if isWideTerminal {
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
						// 照片距离终端上边界两行间距
						startRow = 2
					}

					if startCol < 1 {
						startCol = 1
					}
					if startRow < 1 {
						startRow = 1
					}

					// 确保图片位置不会超出终端边界
					if startCol+imageWidthInChars > w {
						imageWidthInChars = w - startCol
					}
					if startRow+imageHeightInChars > h {
						imageHeightInChars = h - startRow
					}

					// 只有在应该显示图片的模式下才显示图片
					if !showNothing && !showTextOnly {
						fmt.Printf("\x1b[%d;%dH", startRow, startCol)
						encoder := sixel.NewEncoder(os.Stdout)
						// 确保sixel编码器使用正确的图片尺寸
						_ = encoder.Encode(scaledImg)

						// 在图片右侧填充空格来覆盖黑色区域
						if imageWidthInChars > 0 && startCol+imageWidthInChars <= w {
							// 从图片右侧开始到终端右侧填充空格
							fillStartCol := startCol + imageWidthInChars
							fillEndCol := w
							if fillStartCol <= fillEndCol {
								// 在图片的每一行右侧填充空格
								for row := startRow; row < startRow+imageHeightInChars; row++ {
									fmt.Printf("\x1b[%d;%dH", row, fillStartCol)
									// 使用清除到行尾命令确保完全覆盖
									fmt.Print("\x1b[K")
								}
							}
						}
					}
				}
			}
		}
	}

	// 分析封面颜色
	if coverImg != nil {
		coverColorR, coverColorG, coverColorB = analyzeCoverColor(coverImg)
	} else {
		coverColorR, coverColorG, coverColorB = 255, 255, 255
	}

	// 返回图片位置和尺寸
	imageRightEdgeVal := 0
	// 只有在宽终端模式时才设置imageRightEdge
	if isWideTerminal {
		imageRightEdgeVal = startCol + imageWidthInChars
	}

	// 确保返回的图片高度不为零，避免后续除以零
	if imageHeightInChars == 0 && finalImgH > 0 && cellH > 0 {
		imageHeightInChars = 1
	}

	return startRow, imageHeightInChars, imageRightEdgeVal, coverColorR, coverColorG, coverColorB
}

// updateStatus 更新屏幕上动态的部分 (播放器状态和信息)
func updateStatus(imageTop, imageHeight int, player *audioPlayer, flacPath string, imageRightEdge, coverColorR, coverColorG, coverColorB int, useCoverColor bool) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return
	}

	// 获取歌曲信息来计算最长文本长度
	title, artist, album := getSongMetadata(flacPath)
	maxTextLength := max(max(len(title), len(artist)), len(album))

	// 如果终端宽度小于最长文本长度，只显示封面
	if w < maxTextLength {
		// 空间不足，只显示照片（已在displayAlbumArt中处理）
		return
	}

	// 判断布局模式 - 必须与displayAlbumArt中的判断完全一致
	// 按优先级顺序判断
	// 1. 什么都不显示模式（最高优先级）
	showNothing := w < 23 || h < 5
	if showNothing {
		// 什么都不显示模式
		return
	}

	// 2. 只显示文本和进度条模式
	showTextOnly := h < 13 && !showNothing
	if showTextOnly {
		// 只显示文本和进度条模式
		updateTextOnlyMode(player, w, h, flacPath, coverColorR, coverColorG, coverColorB, useCoverColor)
		return
	}

	// 3. 只显示照片模式
	showInfoOnly := (w < maxTextLength || h < 10) && !showNothing && !showTextOnly
	if showInfoOnly {
		// 只显示照片模式，不显示任何信息
		return
	}

	// 判断宽终端模式
	isWideTerminal := w >= 100 && (float64(w)/float64(h) > 2.0 || h < 20)

	if isWideTerminal {
		// 宽终端：右侧信息栏 - 但需要检查是否有足够的空间显示信息
		if imageRightEdge > 0 && w-imageRightEdge >= 30 {
			updateRightPanel(imageRightEdge, player, w, flacPath, imageTop, imageHeight, coverColorR, coverColorG, coverColorB, useCoverColor)
		}
	} else {
		// 窄终端：检查照片下方是否有足够空间
		imageBottomRow := imageTop + imageHeight
		availableRows := h - imageBottomRow
		if availableRows < 5 {
			// 空间不足，只显示照片
			return
		}
		// 底部状态栏
		updateBottomStatus(imageBottomRow, player, w, h, flacPath, coverColorR, coverColorG, coverColorB, useCoverColor)
	}
}

// updateRightPanel 更新右侧信息面板
func updateRightPanel(imageRightEdge int, player *audioPlayer, w int, flacPath string, imageTop, imageHeight, coverColorR, coverColorG, coverColorB int, useCoverColor bool) {
	// 如果图片高度小于5行，则空间太小，不显示任何信息，实现“只显示照片”模式
	if imageHeight < 5 {
		return
	}

	// 获取歌曲元数据
	title, artist, album := getSongMetadata(flacPath)

	// --- 水平位置计算 (与之前类似) ---
	texts := []string{title, artist, album}
	var totalLength int
	for _, text := range texts {
		totalLength += len(text)
	}
	avgLength := 0
	if len(texts) > 0 {
		avgLength = totalLength / len(texts)
	}

	availableWidth := w - imageRightEdge
	centerCol := imageRightEdge + availableWidth/2
	visualCenterCol := centerCol - avgLength/2
	if visualCenterCol < imageRightEdge+1 {
		visualCenterCol = imageRightEdge + 1
	}

	// --- 垂直位置计算 (新逻辑) ---
	// 在图片高度范围内，分成相同高度的三份
	partHeight := imageHeight / 3

	// 信息显示的第二行在第二部分的中间位置
	artistRow := imageTop + partHeight + partHeight/2
	titleRow := artistRow - 1
	albumRow := artistRow + 1

	// 进度条在第三部分的中间部分
	progressRow := imageTop + (2 * partHeight) + partHeight/2

	// 当高度比较低导致信息显示的第三行和进度条之间的间距不足一行的时候切换到只显示照片模式
	// (albumRow 是第三行信息的位置, progressRow 是进度条位置)
	if progressRow-albumRow < 1 {
		return // 间距不足，不显示信息和进度条
	}

	// 确保信息不会超出图片顶部
	if titleRow < imageTop {
		titleRow = imageTop
		artistRow = titleRow + 1
		albumRow = artistRow + 1
	}

	// 确保进度条不会超出图片底部
	if progressRow >= imageTop+imageHeight {
		progressRow = imageTop + imageHeight - 1
	}

	// --- 绘制 ---
	// 显示简约的歌曲信息
	var colorCode string
	if useCoverColor {
		colorCode = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", coverColorR, coverColorG, coverColorB)
	} else {
		colorCode = "\x1b[37m" // 白色
	}
	fmt.Printf("\x1b[%d;%dH\x1b[K%s\x1b[1m%s\x1b[0m", titleRow, visualCenterCol, colorCode, title)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", artistRow, visualCenterCol, colorCode, artist)
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", albumRow, visualCenterCol, colorCode, album)

	// 计算进度条位置和长度
	progressBarStartCol := imageRightEdge + 5
	progressBarWidth := w - progressBarStartCol - 1
	if progressBarWidth < 10 {
		return // 空间不足以绘制有意义的进度条
	}

	// 计算播放进度
	currentPos := player.streamer.Position()
	totalLen := player.streamer.Len()
	var progress float64
	if totalLen > 0 {
		progress = float64(currentPos) / float64(totalLen)
	}

	playedChars := int(float64(progressBarWidth) * progress)

	// 显示播放/暂停图标和进度条
	fmt.Printf("\x1b[%d;%dH\x1b[K%s", progressRow, progressBarStartCol-2, colorCode)
	if player.ctrl.Paused {
		fmt.Printf("▶")
	} else {
		fmt.Printf("⏸")
	}
	fmt.Printf("\x1b[0m\x1b[%d;%dH", progressRow, progressBarStartCol)
	if playedChars > 0 {
		fmt.Printf("\x1b[2m%s", colorCode) // 调暗
		for range playedChars {
			fmt.Printf("━")
		}
		fmt.Printf("\x1b[0m") // 恢复正常亮度
	}
	fmt.Printf("%s", colorCode) // 未播放部分
	for i := playedChars; i < progressBarWidth; i++ {
		fmt.Printf("━")
	}
	fmt.Printf("\x1b[0m")
	// 显示循环图标
	fmt.Printf("\x1b[%d;%dH\x1b[K%s⟳\x1b[0m", progressRow, progressBarStartCol+progressBarWidth+1, colorCode)
}

// updateBottomStatus 更新底部状态栏
func updateBottomStatus(startRow int, player *audioPlayer, w, h int, flacPath string, coverColorR, coverColorG, coverColorB int, useCoverColor bool) {
	// 获取歌曲元数据
	title, artist, album := getSongMetadata(flacPath)

	// 计算三等分位置（在图片下方的空间中）
	// startRow 是图片底部位置，我们将可用空间分成三等分
	availableRows := h - startRow

	// 计算三等分的分界线
	firstThird := startRow + availableRows/3
	secondThird := startRow + 2*availableRows/3

	// 歌曲信息第一行显示在第一部分与第二部分的交界处
	infoRow := firstThird

	// 进度条显示在下三分之一的中间位置
	progressRow := secondThird + (h-secondThird)/2

	// 每行文字各自居中对齐
	centerCol := w / 2

	// 显示简约的歌曲信息（每行单独计算居中位置）
	var colorCode string
	if useCoverColor {
		colorCode = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", coverColorR, coverColorG, coverColorB)
	} else {
		colorCode = "\x1b[37m" // 白色
	}
	titleCol := centerCol - len(title)/2
	if titleCol < 1 {
		titleCol = 1
	}
	fmt.Printf("\x1b[%d;%dH\x1b[K%s\x1b[1m%s\x1b[0m", infoRow, titleCol, colorCode, title)

	artistCol := centerCol - len(artist)/2
	if artistCol < 1 {
		artistCol = 1
	}
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", infoRow+1, artistCol, colorCode, artist)

	albumCol := centerCol - len(album)/2
	if albumCol < 1 {
		albumCol = 1
	}
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", infoRow+2, albumCol, colorCode, album)

	// 计算进度条位置和长度
	// 进度条从左侧5个字符开始，到右侧5个字符结束
	progressBarStartCol := 5
	progressBarEndCol := w - 5
	progressBarWidth := progressBarEndCol - progressBarStartCol

	// 确保进度条宽度合理
	if progressBarWidth < 10 {
		progressBarWidth = 10
	}

	// 计算播放进度
	currentPos := player.streamer.Position()
	totalLen := player.streamer.Len()
	var progress float64
	if totalLen > 0 {
		progress = float64(currentPos) / float64(totalLen)
	} else {
		progress = 0
	}

	// 计算已播放和未播放的字符数
	playedChars := int(float64(progressBarWidth) * progress)

	// 显示播放/暂停图标和进度条
	fmt.Printf("\x1b[%d;%dH\x1b[K%s", progressRow, progressBarStartCol-2, colorCode)
	if player.ctrl.Paused {
		fmt.Printf("▶")
	} else {
		fmt.Printf("⏸")
	}
	fmt.Printf("\x1b[0m\x1b[%d;%dH", progressRow, progressBarStartCol)

	// 已播放部分（调暗显示）
	if playedChars > 0 {
		fmt.Printf("\x1b[2m%s", colorCode) // 调暗
		for range playedChars {
			fmt.Printf("━")
		}
		fmt.Printf("\x1b[0m") // 恢复正常亮度
	}

	// 未播放部分（正常亮度）
	fmt.Printf("%s", colorCode) // 未播放部分
	for i := playedChars; i < progressBarWidth; i++ {
		fmt.Printf("━")
	}
	fmt.Printf("\x1b[0m")
	// 显示循环图标
	fmt.Printf("\x1b[%d;%dH\x1b[K%s⟳\x1b[0m", progressRow, progressBarStartCol+progressBarWidth+1, colorCode)
}

// updateTextOnlyMode 只显示文本和进度条模式
func updateTextOnlyMode(player *audioPlayer, w, h int, flacPath string, coverColorR, coverColorG, coverColorB int, useCoverColor bool) {
	// 获取歌曲元数据
	title, artist, album := getSongMetadata(flacPath)

	// 计算文本显示位置 - 在终端中间显示
	centerRow := h / 2
	centerCol := w / 2

	// 显示简约的歌曲信息（每行单独计算居中位置）
	var colorCode string
	if useCoverColor {
		colorCode = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", coverColorR, coverColorG, coverColorB)
	} else {
		colorCode = "\x1b[37m" // 白色
	}

	// 显示标题（加粗）
	titleCol := centerCol - len(title)/2
	if titleCol < 1 {
		titleCol = 1
	}
	fmt.Printf("\x1b[%d;%dH\x1b[K%s\x1b[1m%s\x1b[0m", centerRow-1, titleCol, colorCode, title)

	// 显示艺术家
	artistCol := centerCol - len(artist)/2
	if artistCol < 1 {
		artistCol = 1
	}
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", centerRow, artistCol, colorCode, artist)

	// 显示专辑
	albumCol := centerCol - len(album)/2
	if albumCol < 1 {
		albumCol = 1
	}
	fmt.Printf("\x1b[%d;%dH\x1b[K%s%s\x1b[0m", centerRow+1, albumCol, colorCode, album)

	// 计算进度条位置和长度
	progressBarStartCol := 5
	progressBarEndCol := w - 5
	progressBarWidth := progressBarEndCol - progressBarStartCol

	// 确保进度条宽度合理
	if progressBarWidth < 10 {
		progressBarWidth = 10
	}

	// 计算播放进度
	currentPos := player.streamer.Position()
	totalLen := player.streamer.Len()
	var progress float64
	if totalLen > 0 {
		progress = float64(currentPos) / float64(totalLen)
	} else {
		progress = 0
	}

	// 计算已播放和未播放的字符数
	playedChars := int(float64(progressBarWidth) * progress)

	// 显示播放/暂停图标和进度条
	// 在三行信息和进度条之间添加一行间隔
	progressRow := centerRow + 3
	fmt.Printf("\x1b[%d;%dH\x1b[K%s", progressRow, progressBarStartCol-2, colorCode)
	if player.ctrl.Paused {
		fmt.Printf("▶")
	} else {
		fmt.Printf("⏸")
	}
	fmt.Printf("\x1b[0m\x1b[%d;%dH", progressRow, progressBarStartCol)

	// 已播放部分（调暗显示）
	if playedChars > 0 {
		fmt.Printf("\x1b[2m%s", colorCode) // 调暗
		for i := 0; i < playedChars; i++ {
			fmt.Printf("━")
		}
		fmt.Printf("\x1b[0m") // 恢复正常亮度
	}

	// 未播放部分（正常亮度）
	fmt.Printf("%s", colorCode) // 未播放部分
	for i := playedChars; i < progressBarWidth; i++ {
		fmt.Printf("━")
	}
	fmt.Printf("\x1b[0m")
	// 显示循环图标
	fmt.Printf("\x1b[%d;%dH\x1b[K%s⟳\x1b[0m", progressRow, progressBarStartCol+progressBarWidth+1, colorCode)
}

// analyzeCoverColor 分析封面颜色
func analyzeCoverColor(img image.Image) (r, g, b int) {
	bounds := img.Bounds()

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := img.At(x, y)
			pr, pg, pb, _ := pixel.RGBA()

			// 转换为0-255范围
			r8 := int(pr >> 8)
			g8 := int(pg >> 8)
			b8 := int(pb >> 8)

			// 计算亮度
			brightness := 0.2126*float64(r8) + 0.7152*float64(g8) + 0.0722*float64(b8)

			// 筛选条件：寻找有色彩且明亮的像素
			isBright := brightness > 160 // 进一步提高亮度阈值
			isNotGray := math.Abs(float64(r8)-float64(g8)) > 25 || math.Abs(float64(g8)-float64(b8)) > 25 || math.Abs(float64(b8)-float64(r8)) > 25
			isNotWhite := !(r8 > 220 && g8 > 220 && b8 > 220) // 避免接近纯白色

			if isBright && isNotGray && isNotWhite {
				// 找到第一个符合条件的像素就返回
				return r8, g8, b8
			}
		}
	}

	// 如果没有找到合适的颜色，使用默认的亮色
	return 255, 255, 255
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

	// 启动 MPRIS 服务
	mprisServer, err := NewMPRISServer(player, flacPath)
	if err != nil {
		// log.Printf("MPRIS 服务启动失败: %v", err)
	} else {
		if err := mprisServer.Start(); err != nil {
			// log.Printf("MPRIS 服务注册失败: %v", err)
		} else {
			defer mprisServer.StopService()
			mprisServer.StartUpdateLoop()
			mprisServer.UpdatePlaybackStatus(true)
			// 立即更新一次元数据
			mprisServer.UpdateMetadata()
		}
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

	// --- 多页面管理 ---
	pageState := newPageState()
	var imageTop, imageHeight, imageRightEdge, coverColorR, coverColorG, coverColorB int
	useCoverColor := true // 默认使用封面颜色

	// 初始化主页面显示
	imageTop, imageHeight, imageRightEdge, coverColorR, coverColorG, coverColorB = displayAlbumArt(flacPath, cellW, cellH)

	// 注册页面渲染函数
	pageState.pages[PageMain] = func() {
		renderMainPage(imageTop, imageHeight, player, flacPath, imageRightEdge, coverColorR, coverColorG, coverColorB, useCoverColor)
	}
	pageState.pages[PageLyrics] = func() {
		renderLyricsPage(player, flacPath, coverColorR, coverColorG, coverColorB, useCoverColor)
	}
	pageState.pages[PageInfo] = func() {
		renderInfoPage(flacPath, coverColorR, coverColorG, coverColorB, useCoverColor)
	}
	pageState.pages[PageStats] = func() {
		renderStatsPage(player, flacPath, coverColorR, coverColorG, coverColorB, useCoverColor)
	}

	// --- 主循环 ---
	pageState.renderCurrentPage()

	for {
		select {
		case key := <-keyCh:
			needsUpdate := true
			switch key {
			case '\x1b': // ESC
				return nil
			case '\t': // Tab键切换页面
				pageState.nextPage()
				// 如果是主页面，需要重新显示专辑封面
				if pageState.currentPage == PageMain {
					imageTop, imageHeight, imageRightEdge, coverColorR, coverColorG, coverColorB = displayAlbumArt(flacPath, cellW, cellH)
				}
			case ' ':
				speaker.Lock()
				player.ctrl.Paused = !player.ctrl.Paused
				speaker.Unlock()
				// 更新 MPRIS 播放状态
				if mprisServer != nil {
					mprisServer.UpdatePlaybackStatus(!player.ctrl.Paused)
				}
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
				// 更新 MPRIS 播放位置
				if mprisServer != nil {
					// 将样本数转换为微秒
					positionInMicroseconds := int64(float64(newPos) / float64(player.sampleRate) * 1e6)
					mprisServer.position = positionInMicroseconds
					if mprisServer.isPlaying {
						mprisServer.startTime = time.Now().Add(-time.Duration(positionInMicroseconds) * time.Microsecond)
					}
					mprisServer.lastUpdate = time.Now()
					mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
						"Position": positionInMicroseconds,
					})
				}
			case 'a', 's':
				speaker.Lock()
				if key == 'a' {
					player.volume.Volume -= 0.1
				} else {
					player.volume.Volume += 0.1
				}
				speaker.Unlock()
				// 更新 MPRIS 音量
				if mprisServer != nil {
					volume, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Volume")
					mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
						"Volume": volume.Value(),
					})
				}
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
				// 更新 MPRIS 播放速率
				if mprisServer != nil {
					rate, _ := mprisServer.Get("org.mpris.MediaPlayer2.Player", "Rate")
					mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
						"Rate": rate.Value(),
					})
				}
			case 'e':
				useCoverColor = !useCoverColor
			default:
				needsUpdate = false
			}
			if needsUpdate {
				pageState.renderCurrentPage()
			}

		case sig := <-sigCh:
			if sig == syscall.SIGINT {
				return nil
			}
			if sig == syscall.SIGWINCH {
				// 窗口大小改变时，如果是主页面需要重新显示专辑封面
				if pageState.currentPage == PageMain {
					imageTop, imageHeight, imageRightEdge, coverColorR, coverColorG, coverColorB = displayAlbumArt(flacPath, cellW, cellH)
				}
				pageState.renderCurrentPage()
			}

		case <-ticker.C:
			// 定时更新当前页面
			pageState.renderCurrentPage()
			// 更新 MPRIS 播放位置
			if mprisServer != nil {
				// 让 MPRIS 服务器自己处理位置更新
				// 这里只需要触发位置检查
				currentPos := mprisServer.getCurrentPosition()
				// 发送属性变化信号
				mprisServer.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
					"Position": currentPos,
				})
			}
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
