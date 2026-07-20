package main

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"time"

	"github.com/nfnt/resize"
	"golang.org/x/term"
)

// LayoutType represents the type of player layout.
//
// LayoutType 表示播放器布局类型。
type LayoutType int

const (
	LayoutNothing        LayoutType = iota // No content displayed
	LayoutTextOnly                         // Only text (title/artist/album)
	LayoutInfoOnly                         // Centered image without text
	LayoutWideRightText                    // Wide terminal: image left, text right
	LayoutWideBottomText                   // Wide terminal: image top, text bottom
	LayoutWideImageOnly                    // Wide terminal: centered image only
	LayoutNarrow                           // Normal narrow terminal: image top, text bottom
	LayoutSwitchText                       // Switchable: centered text + progress
	LayoutSwitchImage                      // Switchable: centered image only
	LayoutSwitchNarrow                     // Switchable: image top, text bottom (centered)
)

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

	// Image metrics / 图片指标
	ImageWidthInChars  int
	ImageHeightInChars int

	// Layout flags / 布局标志
	IsWideTerminal    bool
	TextTooLongForWide bool
	ShowTextInWideMode bool
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
	maxTextLength := max(max(len(title), len(artist)), len(album))

	showNothing := w < 23 || h < 5
	showTextOnly := h < 13
	isWideTerminal := w >= 100 && (float64(w)/float64(h) > 2.0 || h < 20) && !showNothing && !showTextOnly

	metrics := LayoutMetrics{
		W:              w,
		H:              h,
		Title:          title,
		Artist:         artist,
		Album:          album,
		MaxTextLength:  maxTextLength,
		IsWideTerminal: isWideTerminal,
	}

	if isWideTerminal {
		availableWidth := w - 30
		if availableWidth < maxTextLength+10 {
			metrics.TextTooLongForWide = true
		}
	}

	return metrics
}

// determineLayout determines the layout type based on metrics.
//
// determineLayout 根据指标判断布局类型。
func (p *PlayerPage) determineLayout(metrics *LayoutMetrics) LayoutType {
	w, h := metrics.W, metrics.H

	if w < 23 || h < 5 {
		return LayoutNothing
	}

	if h < 13 {
		return LayoutTextOnly
	}

	if p.overrideLayout >= LayoutSwitchText {
		return p.overrideLayout
	}

	if w < metrics.MaxTextLength || h < 10 {
		return LayoutInfoOnly
	}

	if metrics.IsWideTerminal {
		if !metrics.TextTooLongForWide {
			return LayoutWideRightText
		}
		if metrics.ShowTextInWideMode {
			return LayoutWideBottomText
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

	case LayoutWideBottomText, LayoutNarrow:
		startRow := 2
		imageBottomRow := startRow + imageHeight
		availableRows := h - imageBottomRow
		infoRow := imageBottomRow + availableRows/3
		if infoRow-imageBottomRow > 5 {
			startRow = infoRow - 1 - imageHeight
			if startRow < 2 {
				startRow = 2
			}
		}
		imageBottomRow = startRow + imageHeight
		availableRows = h - imageBottomRow
		progressRow := imageBottomRow + 2*availableRows/3 + (h-(imageBottomRow+2*availableRows/3))/2
		topGap := startRow
		bottomGap := h - progressRow
		shift := (topGap - bottomGap) / 2
		p.layoutShift = shift
		if shift > 0 {
			startRow -= shift
			if startRow < 2 {
				startRow = 2
			}
		} else {
			p.layoutShift = 0
		}
		return LayoutPosition{
			StartCol: (w - imageWidth) / 2,
			StartRow: startRow,
			Width:    imageWidth,
			Height:   imageHeight,
		}

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

	case LayoutSwitchNarrow:
		startRow := 2
		imageBottomRow := startRow + imageHeight
		availableRows := h - imageBottomRow
		infoRow := imageBottomRow + availableRows/3
		if infoRow-imageBottomRow > 5 {
			startRow = infoRow - 1 - imageHeight
			if startRow < 2 {
				startRow = 2
			}
		}
		imageBottomRow = startRow + imageHeight
		availableRows = h - imageBottomRow
		progressRow := imageBottomRow + 2*availableRows/3 + (h-(imageBottomRow+2*availableRows/3))/2
		topGap := startRow
		bottomGap := h - progressRow
		shift := (topGap - bottomGap) / 2
		p.layoutShift = shift
		if shift > 0 {
			startRow -= shift
			if startRow < 2 {
				startRow = 2
			}
		} else {
			p.layoutShift = 0
		}
		return LayoutPosition{
			StartCol: (w - imageWidth) / 2,
			StartRow: startRow,
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

// updateLayoutFlagsWithImage updates layout flags after image size is known.
//
// updateLayoutFlagsWithImage 在图片尺寸已知后更新布局标志。
func (p *PlayerPage) updateLayoutFlagsWithImage(metrics *LayoutMetrics, imageWidth, imageHeight int) {
	metrics.ImageWidthInChars = imageWidth
	metrics.ImageHeightInChars = imageHeight

	if metrics.IsWideTerminal {
		availableWidth := metrics.W - imageWidth
		if availableWidth < metrics.MaxTextLength+10 {
			metrics.TextTooLongForWide = true
		}
	}

	p.textTooLongForWide = metrics.TextTooLongForWide
	p.showTextInWideMode = false

	if metrics.IsWideTerminal && metrics.TextTooLongForWide {
		if metrics.H-imageHeight >= 5 {
			p.showTextInWideMode = true
		}
	}
}

// renderAlbumArt renders the album art and returns image dimensions.
//
// renderAlbumArt 渲染专辑封面并返回图片尺寸。
func (p *PlayerPage) renderAlbumArt(coverImg image.Image, layout LayoutType, pos *LayoutPosition) (int, int) {
	if coverImg == nil || layout == LayoutNothing || layout == LayoutTextOnly {
		return 0, 0
	}

	metrics := LayoutMetrics{W: pos.Width, H: pos.Height}
	pixelW, pixelH := p.calculatePixelSize(&metrics, layout)

	if pixelW < 10 {
		pixelW = 10
	}
	if pixelH < 10 {
		pixelH = 10
	}

	normalizedImg := resize.Resize(960, 960, coverImg, resize.Lanczos3)
	scaledImg := resize.Thumbnail(uint(pixelW), uint(pixelH), normalizedImg, resize.Lanczos3)

	imageWidthInChars := scaledImg.Bounds().Dx() / p.cellW
	if imageWidthInChars < 1 {
		imageWidthInChars = 1
	}
	imageHeightInChars := scaledImg.Bounds().Dy() / p.cellH
	if imageHeightInChars < 1 {
		imageHeightInChars = 1
	}

	w, h, _ := term.GetSize(int(os.Stdout.Fd()))
	if imageWidthInChars > w {
		imageWidthInChars = w
	}
	if imageHeightInChars > h {
		imageHeightInChars = h
	}

	pos.Width = imageWidthInChars
	pos.Height = imageHeightInChars

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

	fmt.Printf("\x1b[%d;%dH", pos.StartRow, pos.StartCol)
	if err := RenderImage(scaledImg, imageWidthInChars, imageHeightInChars); err != nil {
		_ = NewEncoder(os.Stdout).Encode(scaledImg)
	}

	return imageWidthInChars, imageHeightInChars
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
		return (w - 30) * p.cellW, (h - 1) * p.cellH
	}

	return w * p.cellW, (h - 2) * p.cellH
}

// clearImageArea clears the area around the rendered image.
//
// clearImageArea 清除渲染图片周围的区域。
func (p *PlayerPage) clearImageArea(pos *LayoutPosition, imageWidth, imageHeight int) {
	w, h, _ := term.GetSize(int(os.Stdout.Fd()))

	if imageWidth > 0 && pos.StartCol+imageWidth <= w {
		fillStartCol := pos.StartCol + imageWidth
		for row := pos.StartRow; row < pos.StartRow+imageHeight; row++ {
			fmt.Printf("\x1b[%d;%dH\x1b[K", row, fillStartCol)
		}
	}
	if pos.StartRow+imageHeight <= h {
		fmt.Printf("\x1b[%d;%dH\x1b[J", pos.StartRow+imageHeight, pos.StartCol)
	}
}

// renderTextByLayout renders text content based on layout type.
//
// renderTextByLayout 根据布局类型渲染文本内容。
func (p *PlayerPage) renderTextByLayout(layout LayoutType, metrics *LayoutMetrics) {
	if layout == LayoutNothing {
		return
	}

	w, h := metrics.W, metrics.H

	switch layout {
	case LayoutTextOnly:
		p.updateTextOnlyMode(w, h)

	case LayoutInfoOnly:
		// No text rendering for info-only layout

	case LayoutWideRightText:
		if p.imageRightEdge > 0 && w-p.imageRightEdge >= 30 {
			p.updateRightPanel(w)
		}

	case LayoutWideBottomText, LayoutNarrow:
		imageBottomRow := p.imageTop + p.imageHeight
		if h-imageBottomRow >= 5 {
			p.updateBottomStatus(imageBottomRow, w, h)
		}

	case LayoutWideImageOnly:
		// No text rendering for wide image-only layout

	case LayoutSwitchText:
		p.updateSwitchTextMode(w, h)

	case LayoutSwitchImage:

	case LayoutSwitchNarrow:
		imageBottomRow := p.imageTop + p.imageHeight
		p.updateSwitchNarrowMode(imageBottomRow, w, h)
	}
}

// renderWithLayout orchestrates the complete rendering process.
//
// renderWithLayout 协调完整的渲染流程。
func (p *PlayerPage) renderWithLayout() {
	time.Sleep(50 * time.Millisecond)
	p.refreshCellSize()

	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		fmt.Print("\x1b[2J\x1b[H")
		l.Warnf("Unable to get terminal size\n\n无法获取终端尺寸")
		return
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H")

	coverImg := p.loadCoverImage()
	metrics := p.collectMetrics(w, h)
	layout := p.determineLayout(&metrics)

	var coverColorR, coverColorG, coverColorB int

	if coverImg != nil {
		r, g, b := analyzeCoverColor(coverImg)
		coverColorR, coverColorG, coverColorB = r, g, b
	} else {
		coverColorR, coverColorG, coverColorB = 255, 255, 255
	}

	var imageWidthInChars, imageHeightInChars int
	var startCol, startRow int

	if coverImg != nil && layout != LayoutNothing && layout != LayoutTextOnly && layout != LayoutSwitchText {
		pixelW, pixelH := p.calculatePixelSize(&metrics, layout)
		if pixelW < 10 {
			pixelW = 10
		}
		if pixelH < 10 {
			pixelH = 10
		}

		normalizedImg := resize.Resize(960, 960, coverImg, resize.Lanczos3)
		scaledImg := resize.Thumbnail(uint(pixelW), uint(pixelH), normalizedImg, resize.Lanczos3)
		finalImgW, finalImgH := scaledImg.Bounds().Dx(), scaledImg.Bounds().Dy()

		if p.cellW == 0 {
			p.cellW = 1
		}
		if p.cellH == 0 {
			p.cellH = 1
		}

		imageWidthInChars = finalImgW / p.cellW
		if imageWidthInChars < 1 {
			imageWidthInChars = 1
		}
		imageHeightInChars = finalImgH / p.cellH
		if imageHeightInChars < 1 {
			imageHeightInChars = 1
		}

		if imageWidthInChars > w {
			imageWidthInChars = w
		}
		if imageHeightInChars > h {
			imageHeightInChars = h
		}

		p.updateLayoutFlagsWithImage(&metrics, imageWidthInChars, imageHeightInChars)
		layout = p.determineLayout(&metrics)

		pos := p.calculateImagePosition(layout, &metrics, imageWidthInChars, imageHeightInChars)
		startCol, startRow = pos.StartCol, pos.StartRow

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

	metrics.ImageWidthInChars = imageWidthInChars
	metrics.ImageHeightInChars = imageHeightInChars
	p.renderTextByLayout(layout, &metrics)

	p.coverColorR = coverColorR
	p.coverColorG = coverColorG
	p.coverColorB = coverColorB
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
