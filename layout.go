package main

import (
	"fmt"
	"image"
	"image/draw"
	"os"

	"github.com/mattn/go-runewidth"
	"github.com/nfnt/resize"
	"golang.org/x/term"
)

// LayoutType represents the type of player layout.
//
// LayoutType 表示播放器布局类型。
type LayoutType int

const (
	LayoutNothing       LayoutType = iota // No content displayed
	LayoutTextOnly                        // Only text (title/artist/album)
	LayoutInfoOnly                        // Centered image without text
	LayoutWideRightText                   // Wide terminal: image left, text right
	_
	LayoutWideImageOnly // Wide terminal: centered image only
	LayoutNarrow        // Normal narrow terminal: image top, text bottom
	LayoutSwitchText    // Switchable: centered text + progress
	LayoutSwitchImage   // Switchable: centered image only
	LayoutSwitchNarrow  // Switchable: image top, text bottom (centered)
)

// Thresholds shared by the layout choice and the drawing steps.
//
// 布局判定与绘制步骤共用的阈值。
const (
	minLayoutWidth        = 23
	minLayoutHeight       = 5
	minImageLayoutHeight  = 13
	minSwitchTextHeight   = 10
	wideTerminalMinWidth  = 100
	wideTerminalAspect    = 2.2
	wideTerminalMaxHeight = 20
	wideTextPanelWidth    = 30
	wideTextMinGap        = 10
	narrowVirtualWidth    = 80
	narrowImageTextGap    = 5
	narrowMinTextRows     = 5
	progressBarPad         = 5
	minProgressBarWidth   = 10
	minImagePixels        = 10
	textOnlyBlockHeight   = 5
	switchTextBlockHeight = 7
	rightPanelMinHeight   = 5
	layoutIndicatorTicks  = 2
)

// isWideTerminal checks if the current terminal is considered wide.
// A terminal is wide if its width reaches wideTerminalMinWidth and its aspect
// ratio exceeds wideTerminalAspect, or when it is shorter than wideTerminalMaxHeight.
//
// isWideTerminal 检查当前终端是否被认为是宽终端。
// 宽终端的条件是宽度达到 wideTerminalMinWidth 且宽高比超过 wideTerminalAspect，
// 或者高度低于 wideTerminalMaxHeight。
func isWideTerminal(w, h int) bool {
	return w >= wideTerminalMinWidth && (float64(w)/float64(h) > wideTerminalAspect || h < wideTerminalMaxHeight)
}

// configLayoutToOverride converts a config layout value to an overrideLayout value.
// 0 = auto (-1), 1 = narrow (LayoutSwitchNarrow), 2 = text (LayoutSwitchText),
// 3 = image (LayoutSwitchImage).
//
// configLayoutToOverride 将配置布局值转换为 overrideLayout 值。
// 0 = 自动(-1), 1 = 窄屏样式(LayoutSwitchNarrow), 2 = 文本(LayoutSwitchText),
// 3 = 封面(LayoutSwitchImage)。
func configLayoutToOverride(configValue int) int {
	switch configValue {
	case 1:
		return int(LayoutSwitchNarrow)
	case 2:
		return int(LayoutSwitchText)
	case 3:
		return int(LayoutSwitchImage)
	default:
		return -1
	}
}

// resolveInitialLayout resolves the initial overrideLayout value from the
// configuration. For the "memory" mode it loads the saved layout instead.
//
// resolveInitialLayout 从配置解析初始 overrideLayout 值。
// “记忆”模式下改为加载已保存的布局。
func resolveInitialLayout() int {
	if GlobalConfig.App.DefaultLayout == 4 {
		savedLayout, err := LoadOverrideLayout()
		if err != nil {
			l.Warnf("Could not load saved layout: %v\n\n无法加载已保存的布局: %v", err, err)
			return -1
		}
		return savedLayout
	}

	return configLayoutToOverride(GlobalConfig.App.DefaultLayout)
}

// LayoutMetrics holds the metrics used to determine layout.
//
// LayoutMetrics 存储用于判断布局的指标。
type LayoutMetrics struct {
	// Terminal dimensions / 终端尺寸
	W int
	H int

	// Text metrics / 文本指标
	Title         string
	Artist        string
	Album         string
	MaxTextLength int

	// Layout flags / 布局标志
	IsWideTerminal     bool
	TextTooLongForWide bool
}

// LayoutPosition holds the calculated position for image rendering.
//
// LayoutPosition 存储计算后的图片渲染位置。
type LayoutPosition struct {
	StartCol int
	StartRow int
	Width    int
	Height   int
}

// collectMetrics gathers all metrics needed for layout determination.
//
// collectMetrics 收集布局判断所需的所有指标。
func (p *PlayerPage) collectMetrics(w, h int) LayoutMetrics {
	title, artist, album := getSongMetadata(p.flacPath)
	maxTextLength := max(max(runewidth.StringWidth(title), runewidth.StringWidth(artist)), runewidth.StringWidth(album))

	showNothing := w < minLayoutWidth || h < minLayoutHeight
	showTextOnly := h < minImageLayoutHeight
	wide := isWideTerminal(w, h) && !showNothing && !showTextOnly

	metrics := LayoutMetrics{
		W:              w,
		H:              h,
		Title:          title,
		Artist:         artist,
		Album:          album,
		MaxTextLength:  maxTextLength,
		IsWideTerminal: wide,
	}

	if wide {
		availableWidth := w - wideTextPanelWidth
		if availableWidth < maxTextLength+wideTextMinGap {
			metrics.TextTooLongForWide = true
		}
	}

	return metrics
}

// determineLayout determines the layout type based on metrics. The user
// override picks the display mode; tiny terminals fall back to the protective
// layouts of the auto mode, while text and image modes blank out instead.
// Inside a terminal multiplexer images cannot be displayed reliably, so the
// text layout the O key cycles to is forced regardless of the saved or
// configured override.
//
// determineLayout 根据指标判断布局类型。用户覆盖值决定显示模式；
// 过小的终端回退到自动模式的保护布局，文本或封面模式则显示为空白。
// 终端复用器内无法可靠显示图像，无论保存或配置的覆盖值如何都强制为
// O 键循环到的文本布局。
func (p *PlayerPage) determineLayout(metrics *LayoutMetrics) LayoutType {
	w, h := metrics.W, metrics.H

	if w < minLayoutWidth || h < minLayoutHeight {
		return LayoutNothing
	}

	if p.app.forcedTextMode {
		if h < minSwitchTextHeight {
			return LayoutNothing
		}
		return LayoutSwitchText
	}

	switch p.overrideLayout {
	case LayoutSwitchText:
		if h < minSwitchTextHeight {
			return LayoutNothing
		}
		return LayoutSwitchText
	case LayoutSwitchImage:
		if h < minImageLayoutHeight {
			return LayoutNothing
		}
		return LayoutSwitchImage
	case LayoutSwitchNarrow:
		if h < minImageLayoutHeight {
			return LayoutTextOnly
		}
		if w < metrics.MaxTextLength {
			return LayoutInfoOnly
		}
		if metrics.IsWideTerminal {
			return LayoutSwitchNarrow
		}
		return LayoutNarrow
	}

	if h < minImageLayoutHeight {
		return LayoutTextOnly
	}

	if w < metrics.MaxTextLength {
		return LayoutInfoOnly
	}

	if metrics.IsWideTerminal {
		if !metrics.TextTooLongForWide {
			return LayoutWideRightText
		}
		return LayoutWideImageOnly
	}

	return LayoutNarrow
}

// calculateImagePosition calculates the image position based on layout type.
//
// calculateImagePosition 根据布局类型计算图片位置。
func (p *PlayerPage) calculateImagePosition(layout LayoutType, metrics *LayoutMetrics, imageWidth, imageHeight int) LayoutPosition {
	w, h := metrics.W, metrics.H

	switch layout {
	case LayoutNothing, LayoutTextOnly:
		return LayoutPosition{0, 0, 0, 0}

	case LayoutInfoOnly:
		return LayoutPosition{
			StartCol: (w - imageWidth) / 2,
			StartRow: (h - imageHeight) / 2,
			Width:    imageWidth,
			Height:   imageHeight,
		}

	case LayoutWideRightText:
		return LayoutPosition{
			StartCol: 1,
			StartRow: (h - imageHeight + 1) / 2,
			Width:    imageWidth,
			Height:   imageHeight,
		}

	case LayoutNarrow, LayoutSwitchNarrow:
		return p.calculateNarrowImagePosition(w, imageWidth, imageHeight, h)

	case LayoutWideImageOnly:
		return LayoutPosition{
			StartCol: (w - imageWidth) / 2,
			StartRow: (h - imageHeight + 1) / 2,
			Width:    imageWidth,
			Height:   imageHeight,
		}

	case LayoutSwitchImage:
		return LayoutPosition{
			StartCol: (w - imageWidth) / 2,
			StartRow: (h - imageHeight + 1) / 2,
			Width:    imageWidth,
			Height:   imageHeight,
		}

	default:
		return LayoutPosition{
			StartCol: (w - imageWidth) / 2,
			StartRow: 2,
			Width:    imageWidth,
			Height:   imageHeight,
		}
	}
}

// narrowBaseRows computes the info row and the progress row for the narrow
// layouts from the row just below the image and the terminal height, before
// the centering shift is applied.
//
// narrowBaseRows 根据图片下方首行与终端高度，计算窄屏布局的信息行与进度条行
// （尚未应用居中偏移）。
func narrowBaseRows(imageBottomRow, h int) (int, int) {
	availableRows := h - imageBottomRow
	infoRow := imageBottomRow + availableRows/3
	progressRow := imageBottomRow + 2*availableRows/3 + (h-(imageBottomRow+2*availableRows/3))/2
	return infoRow, progressRow
}

// calculateNarrowImagePosition places the image for the narrow layouts. The
// text block sits one third down the rows left below the image; when that gap
// grows past narrowImageTextGap rows the image is pulled up against the text,
// then the whole composition shifts so the whitespace above and below it stays
// even. The applied shift is stored in layoutShift so the text drawing steps
// offset their rows by exactly the same amount.
//
// calculateNarrowImagePosition 为窄屏布局计算图片位置。文字块位于图片下方剩余
// 行数的三分之一处；当间隔超过 narrowImageTextGap 行时图片上移贴近文字，随后
// 整体偏移使上下留白均匀。实际偏移量存入 layoutShift，供文字绘制步骤按完全
// 相同的量偏移各自的行。
func (p *PlayerPage) calculateNarrowImagePosition(w, imageWidth, imageHeight, h int) LayoutPosition {
	p.layoutShift = 0
	startRow := 2
	imageBottomRow := startRow + imageHeight
	infoRow, progressRow := narrowBaseRows(imageBottomRow, h)
	if infoRow-imageBottomRow > narrowImageTextGap {
		startRow = max(infoRow-1-imageHeight, 2)
		imageBottomRow = startRow + imageHeight
		_, progressRow = narrowBaseRows(imageBottomRow, h)
		shift := (startRow - (h - progressRow)) / 2
		if shift > 0 {
			if startRow-shift < 2 {
				shift = startRow - 2
			}
			p.layoutShift = shift
			startRow -= shift
		}
	}
	return LayoutPosition{
		StartCol: (w - imageWidth) / 2,
		StartRow: startRow,
		Width:    imageWidth,
		Height:   imageHeight,
	}
}

// updateWideTextFlag recomputes the TextTooLongForWide verdict from the actual
// image width so the decision flips in both directions as the cover size
// changes.
//
// updateWideTextFlag 根据实际图片宽度重新计算 TextTooLongForWide 判定，
// 使结论随封面尺寸变化可以双向翻转。
func (p *PlayerPage) updateWideTextFlag(metrics *LayoutMetrics, imageWidth int) {
	if metrics.IsWideTerminal {
		availableWidth := metrics.W - imageWidth
		metrics.TextTooLongForWide = availableWidth < metrics.MaxTextLength+wideTextMinGap
	}
}

// calculatePixelSize calculates the pixel size for image rendering.
//
// calculatePixelSize 计算图片渲染的像素尺寸。
func (p *PlayerPage) calculatePixelSize(metrics *LayoutMetrics, layout LayoutType) (int, int) {
	w, h := metrics.W, metrics.H

	if layout == LayoutNothing || layout == LayoutTextOnly {
		return 0, 0
	}

	if layout == LayoutWideRightText {
		return (w - wideTextPanelWidth) * p.cellW, (h - 1) * p.cellH
	}

	return w * p.cellW, (h - 2) * p.cellH
}

// renderTextByLayout renders text content based on layout type. The layouts
// that show only a cover draw no text at all.
//
// renderTextByLayout 根据布局类型渲染文本内容。只显示封面的布局不画任何文字。
func (p *PlayerPage) renderTextByLayout(layout LayoutType, w, h int) {
	if layout == LayoutNothing {
		return
	}

	switch layout {
	case LayoutTextOnly:
		p.updateTextOnlyMode(w, h)

	case LayoutInfoOnly, LayoutWideImageOnly, LayoutSwitchImage:

	case LayoutWideRightText:
		if p.imageRightEdge > 0 && w-p.imageRightEdge >= wideTextPanelWidth {
			p.updateRightPanel(w)
		}

	case LayoutNarrow, LayoutSwitchNarrow:
		imageBottomRow := p.imageTop + p.imageHeight
		if h-imageBottomRow >= narrowMinTextRows {
			p.updateNarrowStatus(imageBottomRow, w, h)
		}

	case LayoutSwitchText:
		p.updateSwitchTextMode(w, h)
	}
}

// renderWithLayout orchestrates the complete rendering process.
//
// renderWithLayout 协调完整的渲染流程。
func (p *PlayerPage) renderWithLayout() {
	p.refreshCellSize()

	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		fmt.Print("\x1b[2J\x1b[H")
		l.Warnf("Unable to get terminal size\n\n无法获取终端尺寸")
		return
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H")

	coverImg, normImg, coverColorR, coverColorG, coverColorB := p.getCoverData()
	metrics := p.collectMetrics(w, h)
	layout := p.determineLayout(&metrics)

	if coverImg != nil && layoutUsesImage(layout) {
		scaledImg, imageWidthInChars, imageHeightInChars := p.scaleCoverForLayout(normImg, &metrics, &layout)

		pos := p.calculateImagePosition(layout, &metrics, imageWidthInChars, imageHeightInChars)
		startCol, startRow := pos.StartCol, pos.StartRow

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

		targetPixelW := imageWidthInChars * p.cellW
		targetPixelH := imageHeightInChars * p.cellH
		if targetPixelW > 0 && targetPixelH > 0 {
			sb := scaledImg.Bounds()
			if sb.Dx() >= targetPixelW && sb.Dy() >= targetPixelH &&
				(sb.Dx() != targetPixelW || sb.Dy() != targetPixelH) {
				offsetX := (sb.Dx() - targetPixelW) / 2
				offsetY := (sb.Dy() - targetPixelH) / 2
				aligned := image.NewRGBA(image.Rect(0, 0, targetPixelW, targetPixelH))
				draw.Draw(aligned, aligned.Bounds(), scaledImg, image.Point{X: offsetX, Y: offsetY}, draw.Src)
				scaledImg = aligned
			}
		}

		fmt.Printf("\x1b[%d;%dH", startRow, startCol)
		if err := RenderImage(scaledImg, imageWidthInChars, imageHeightInChars); err != nil {
			_ = NewEncoder(os.Stdout).Encode(scaledImg)
		}

		if imageWidthInChars > 0 && startCol+imageWidthInChars <= w {
			fillStartCol := startCol + imageWidthInChars
			for row := startRow; row < startRow+imageHeightInChars; row++ {
				fmt.Printf("\x1b[%d;%dH\x1b[K", row, fillStartCol)
			}
		}
		if startRow+imageHeightInChars <= h {
			fmt.Printf("\x1b[%d;%dH\x1b[J", startRow+imageHeightInChars, startCol)
		}

		p.imageTop = startRow
		p.imageHeight = imageHeightInChars
		p.imageRightEdge = startCol + imageWidthInChars
	} else {
		p.imageTop = 0
		p.imageHeight = 0
		p.imageRightEdge = 0
	}

	p.currentLayout = layout
	p.renderTextByLayout(layout, w, h)

	p.coverColorR = coverColorR
	p.coverColorG = coverColorG
	p.coverColorB = coverColorB
}

// layoutUsesImage reports whether the given layout renders the album cover.
//
// layoutUsesImage 报告给定布局是否渲染专辑封面。
func layoutUsesImage(layout LayoutType) bool {
	switch layout {
	case LayoutNothing, LayoutTextOnly, LayoutSwitchText:
		return false
	}
	return true
}

// scaleCoverForLayout scales the normalized cover to the pixel budget of the
// current layout and converts the result back to character cells. The image
// width feeds the layout verdict, so whenever that verdict flips the cover is
// scaled again with the new budget until both agree.
//
// scaleCoverForLayout 将归一化封面缩放到当前布局的像素预算并换算回字符单元格。
// 图片宽度会参与布局判定，因此判定翻转时按新预算重新缩放，直到两者一致。
func (p *PlayerPage) scaleCoverForLayout(normImg image.Image, metrics *LayoutMetrics, layout *LayoutType) (image.Image, int, int) {
	if p.cellW == 0 {
		p.cellW = 1
	}
	if p.cellH == 0 {
		p.cellH = 1
	}

	var scaledImg image.Image
	var imageWidthInChars, imageHeightInChars int

	for range 3 {
		pixelW, pixelH := p.calculatePixelSize(metrics, *layout)
		if pixelW < minImagePixels {
			pixelW = minImagePixels
		}
		if pixelH < minImagePixels {
			pixelH = minImagePixels
		}

		scaledImg = resize.Thumbnail(uint(pixelW), uint(pixelH), normImg, resize.Lanczos3)
		finalImgW, finalImgH := scaledImg.Bounds().Dx(), scaledImg.Bounds().Dy()

		imageWidthInChars = max(finalImgW/p.cellW, 1)
		imageHeightInChars = max(finalImgH/p.cellH, 1)
		if imageWidthInChars > metrics.W {
			imageWidthInChars = metrics.W
		}
		if imageHeightInChars > metrics.H {
			imageHeightInChars = metrics.H
		}

		p.updateWideTextFlag(metrics, imageWidthInChars)
		next := p.determineLayout(metrics)
		if next == *layout {
			break
		}
		*layout = next
	}

	return scaledImg, imageWidthInChars, imageHeightInChars
}

// loadCoverImage loads the cover image from audio file or fallbacks.
//
// loadCoverImage 从音频文件加载封面图片或使用备用图片。
func (p *PlayerPage) loadCoverImage() image.Image {
	coverImg := getCoverFromAudioFile(p.flacPath)

	if coverImg == nil && GlobalConfig != nil && GlobalConfig.App.EnableFolderCovers {
		coverImg = getFolderCoverImage(p.flacPath)
	}

	if coverImg == nil {
		defaultCoverPath := getDefaultCoverPath()
		if defaultCoverPath != "" {
			if img, err := loadImageFile(defaultCoverPath); err == nil {
				coverImg = img
			}
		}
	}

	return coverImg
}

// getCoverData returns the cover image, its 960x960 normalized version and
// the dominant cover color. Results are cached per song path so redraws
// (terminal resize, layout switch) do not re-decode or re-analyze the cover.
//
// getCoverData 返回封面图片、其 960x960 归一化版本以及封面主色调。
// 结果按歌曲路径缓存，重绘（终端 resize、布局切换）时无需重新解码或分析封面。
func (p *PlayerPage) getCoverData() (image.Image, image.Image, int, int, int) {
	if p.coverCacheValid && p.coverCachePath == p.flacPath {
		return p.coverCacheImg, p.coverCacheNorm, p.coverCacheR, p.coverCacheG, p.coverCacheB
	}

	coverImg := p.loadCoverImage()
	var normImg image.Image
	var r, g, b int
	if coverImg != nil {
		r, g, b = analyzeCoverColor(coverImg)
		normImg = resize.Resize(960, 960, coverImg, resize.Lanczos3)
	} else {
		r, g, b = 255, 255, 255
	}

	p.coverCachePath = p.flacPath
	p.coverCacheImg = coverImg
	p.coverCacheNorm = normImg
	p.coverCacheR, p.coverCacheG, p.coverCacheB = r, g, b
	p.coverCacheValid = true

	return coverImg, normImg, r, g, b
}
