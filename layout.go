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
	narrowSongTextRows    = 3
	narrowBlockFixedRows  = 4
	narrowMinTextRows     = 5
	narrowMinGap          = 5
	narrowMaxGap          = 8
	progressBarPad        = 5
	textProgressBarPad    = 7
	minProgressBarWidth   = 10
	progressBarTextScale  = 4
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

// songTextWidth returns the display width of the widest song metadata line.
//
// songTextWidth 返回歌曲元数据中最宽一行的显示宽度。
func songTextWidth(flacPath string) int {
	title, artist, album := getSongMetadata(flacPath)
	return max(max(runewidth.StringWidth(title), runewidth.StringWidth(artist)), runewidth.StringWidth(album))
}

// cappedProgressBarWidth applies the metadata based cap to a progress bar
// width: the bar never outgrows progressBarTextScale times the widest song
// text and never drops below the minimum width, so it keeps its proportion
// on terminals of any size.
//
// cappedProgressBarWidth 将基于元数据的封顶应用到进度条宽度：进度条最宽不超过
// 歌曲文字最大宽度的 progressBarTextScale 倍，且不小于最小进度条宽度，
// 从而在任意尺寸的终端上保持比例。
func cappedProgressBarWidth(base, maxTextWidth int) int {
	width := min(base, max(progressBarTextScale*maxTextWidth, minProgressBarWidth))
	return max(width, minProgressBarWidth)
}

// progressBarWidth returns the shared progress bar width for the narrow
// layouts and the wide right-text layout: the virtual column band minus its
// side padding, capped by the song metadata.
//
// progressBarWidth 返回窄屏布局与宽屏右文布局共用的进度条宽度，
// 即虚拟列宽减去两侧留白，并受歌曲元数据封顶。
func progressBarWidth(w, maxTextWidth int) int {
	base := max(min(narrowVirtualWidth, w)-2*progressBarPad, minProgressBarWidth)
	return cappedProgressBarWidth(base, maxTextWidth)
}

// collectMetrics gathers all metrics needed for layout determination.
//
// collectMetrics 收集布局判断所需的所有指标。
func (p *PlayerPage) collectMetrics(w, h int) LayoutMetrics {
	showNothing := w < minLayoutWidth || h < minLayoutHeight
	showTextOnly := h < minImageLayoutHeight
	wide := isWideTerminal(w, h) && !showNothing && !showTextOnly

	maxTextLength := songTextWidth(p.flacPath)
	metrics := LayoutMetrics{
		W:              w,
		H:              h,
		MaxTextLength:  maxTextLength,
		IsWideTerminal: wide,
	}

	if wide {
		panelWidth := progressBarWidth(w, maxTextLength) + 2*progressBarPad
		if panelWidth < maxTextLength+wideTextMinGap {
			metrics.TextTooLongForWide = true
		}
	}

	return metrics
}

// narrowCoverTooSmall reports whether the cover would shrink below three
// fifths of the progress bar width in the narrow layouts, where the metadata
// block squeezes it. Callers then fall back to the cover-only layout so a
// tiny cover with text never reaches the screen.
//
// narrowCoverTooSmall 报告窄屏布局下封面是否会缩到进度条宽度的五分之三以下，
// 即被元数据块挤小时的情况。调用方随即回退到只显示封面的布局，
// 避免小封面配文字的版面上屏。
func (p *PlayerPage) narrowCoverTooSmall(w, h, maxTextWidth int) bool {
	barWidth := progressBarWidth(w, maxTextWidth)
	heightCols := narrowImageHeightBudget(h) * p.cellH / p.cellW
	coverCols := min(barWidth, heightCols)
	return coverCols*5 < barWidth*3
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
		if p.narrowCoverTooSmall(w, h, metrics.MaxTextLength) {
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

	if p.narrowCoverTooSmall(w, h, metrics.MaxTextLength) {
		return LayoutInfoOnly
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
		panelWidth := progressBarWidth(w, metrics.MaxTextLength) + 2*progressBarPad
		return LayoutPosition{
			StartCol: (w - imageWidth - panelWidth) / 2,
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

// narrowImageHeightBudget returns the tallest the cover may grow in the
// narrow layouts so the gaps around the song text can still reach their
// minimum.
//
// narrowImageHeightBudget 返回窄屏布局封面允许的最大高度，
// 保证歌曲文字上下的间隔仍能达到最小值。
func narrowImageHeightBudget(h int) int {
	return h - narrowBlockFixedRows - 2*narrowMinGap - 1
}

// narrowRows computes the first row of the cover and the rows of the song
// text and the progress bar for the narrow layouts. Cover, text and progress
// bar form one block that sits centered, with the extra row on top when the
// whitespace count is odd; the whitespace around the song text stays equal so
// the text reads as the middle of the composition, and it grows with the
// terminal only between narrowMinGap and narrowMaxGap, so tall terminals pile
// the extra rows into the surrounding whitespace instead of stretching the
// block. When the terminal cannot spare the minimum the gap gives way down to
// what fits.
//
// narrowRows 计算窄屏布局封面起始行以及歌曲文字与进度条的行号。封面、文字与
// 进度条组成一个整体垂直居中，留白总数为奇数时上面多一行；歌曲文字上下的空白
// 保持相等，使文字成为版面的中心，且只在 narrowMinGap 到 narrowMaxGap 之间随
// 终端增大，高终端的多余行数进入上下留白而不是拉伸版面。终端空间不足时，
// 间隔向下让步到刚好放得下的程度。
func narrowRows(imageHeight, h int) (int, int, int) {
	free := max(h-imageHeight-narrowBlockFixedRows, 0)
	gapPair := 5 * free / 7
	gapUp := max(min((gapPair+1)/2, narrowMaxGap), min(narrowMinGap, (free-1)/2))
	gapLow := gapUp + 1
	blank := free - gapUp - gapLow
	startRow := blank - blank/2 + 1
	infoRow := startRow + imageHeight + gapUp
	progressRow := infoRow + narrowSongTextRows + gapLow
	return startRow, infoRow, progressRow
}

// calculateNarrowImagePosition places the image for the narrow layouts. The
// rows come straight from the centered composition, so the cover never needs
// a corrective shift afterwards.
//
// calculateNarrowImagePosition 为窄屏布局计算图片位置。各行直接取自居中的版面，
// 封面事后不再需要修正偏移。
func (p *PlayerPage) calculateNarrowImagePosition(w, imageWidth, imageHeight, h int) LayoutPosition {
	startRow := 2
	if h-(startRow+imageHeight) >= narrowMinTextRows {
		startRow, _, _ = narrowRows(imageHeight, h)
	}
	return LayoutPosition{
		StartCol: (w - imageWidth) / 2,
		StartRow: startRow,
		Width:    imageWidth,
		Height:   imageHeight,
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
		bar := progressBarWidth(w, metrics.MaxTextLength)
		coverCols := min(bar, w-bar-2*progressBarPad)
		return coverCols * p.cellW, (h - 1) * p.cellH
	}

	if layout == LayoutNarrow || layout == LayoutSwitchNarrow {
		return progressBarWidth(w, metrics.MaxTextLength) * p.cellW, narrowImageHeightBudget(h) * p.cellH
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
			p.updateNarrowStatus(w, h)
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
		scaledImg, imageWidthInChars, imageHeightInChars := p.scaleCoverForLayout(normImg, &metrics, layout)

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
// given layout and converts the result back to character cells.
//
// scaleCoverForLayout 将归一化封面缩放到给定布局的像素预算并换算回字符单元格。
func (p *PlayerPage) scaleCoverForLayout(normImg image.Image, metrics *LayoutMetrics, layout LayoutType) (image.Image, int, int) {
	if p.cellW == 0 {
		p.cellW = 1
	}
	if p.cellH == 0 {
		p.cellH = 1
	}

	pixelW, pixelH := p.calculatePixelSize(metrics, layout)
	if pixelW < minImagePixels {
		pixelW = minImagePixels
	}
	if pixelH < minImagePixels {
		pixelH = minImagePixels
	}

	scaledImg := resize.Thumbnail(uint(pixelW), uint(pixelH), normImg, resize.Lanczos3)
	finalImgW, finalImgH := scaledImg.Bounds().Dx(), scaledImg.Bounds().Dy()

	imageWidthInChars := max(finalImgW/p.cellW, 1)
	imageHeightInChars := max(finalImgH/p.cellH, 1)
	if imageWidthInChars > metrics.W {
		imageWidthInChars = metrics.W
	}
	if imageHeightInChars > metrics.H {
		imageHeightInChars = metrics.H
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
