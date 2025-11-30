package main

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"io"
	"runtime"
	"sync"

	"github.com/soniakeys/quant/median"
)

// Encoder encodes an image to the sixel format.
//
// Encoder 将图像编码为sixel格式。
type Encoder struct {
	w io.Writer

	Dither bool
	Width  int
	Height int
	Colors int

	// Workers is the number of parallel workers to use (0 means use number of CPU cores).
	//
	// Workers 是要使用的并行工作线程数（0表示使用CPU核心数）。
	Workers int
}

// NewEncoder returns a new instance of Encoder.
//
// NewEncoder 返回一个新的 Encoder 实例。
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w, Workers: runtime.NumCPU()}
}

// stripResult stores the processing result of each slice.
//
// stripResult 存储每个分片的处理结果。
type stripResult struct {
	startRow int
	sixelMap [][][]byte
}

// Encode encodes an image to the sixel format in parallel.
//
// Encode 并行地将图像编码为sixel格式。
func (e *Encoder) Encode(img image.Image) error {
	nc := e.Colors
	if nc < 2 {
		nc = 255
	}

	origWidth, origHeight := img.Bounds().Dx(), img.Bounds().Dy()
	if origWidth == 0 || origHeight == 0 {
		return nil
	}

	width, height := origWidth, origHeight
	if e.Width > 0 && e.Width < width {
		width = e.Width
	}
	if e.Height > 0 && e.Height < height {
		height = e.Height
	}

	outBuf := bytes.NewBuffer(make([]byte, 0, 1024*64))
	outBuf.Write([]byte{0x1b, 0x50, 0x30, 0x3b, 0x30, 0x3b, 0x38, 0x71, 0x22, 0x31, 0x3b, 0x31})

	var paletted *image.Paletted
	if p, ok := img.(*image.Paletted); ok && len(p.Palette) <= int(nc) {
		paletted = p
	} else {
		q := median.Quantizer(nc - 1)
		paletted = q.Paletted(img)
		if e.Dither {
			draw.FloydSteinberg.Draw(paletted, img.Bounds(), img, image.Point{})
		}
	}

	for i, c := range paletted.Palette {
		r, g, b, _ := c.RGBA()
		if i >= int(nc) {
			break
		}
		fmt.Fprintf(outBuf, "#%d;2;%d;%d;%d", i+1, r*100/0xFFFF, g*100/0xFFFF, b*100/0xFFFF)
	}

	sixelRows := (height + 5) / 6
	workers := e.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > sixelRows {
		workers = sixelRows
	}
	rowsPerWorker := (sixelRows + workers - 1) / workers

	var wg sync.WaitGroup
	resultChan := make(chan stripResult, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			startRow := workerID * rowsPerWorker
			endRow := startRow + rowsPerWorker
			if endRow > sixelRows {
				endRow = sixelRows
			}
			if startRow >= endRow {
				return
			}
			e.processStrip(img, paletted, startRow, endRow, width, height, nc, resultChan)
		}(i)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	e.encodeSixelRows(outBuf, resultChan, sixelRows, width, len(paletted.Palette))

	outBuf.Write([]byte{0x1b, 0x5c})
	_, err := outBuf.WriteTo(e.w)
	return err
}

// processStrip processes a strip of the image in parallel (by sixel rows).
//
// processStrip 并行处理图像的一个条带（按sixel行）。
func (e *Encoder) processStrip(img image.Image, paletted *image.Paletted, startRow, endRow, width, totalHeight, nc int, resultChan chan<- stripResult) {
	sixelRows := endRow - startRow

	sixelMap := make([][][]byte, sixelRows)
	for z := 0; z < sixelRows; z++ {
		sixelMap[z] = make([][]byte, nc)
		for c := 0; c < nc; c++ {
			sixelMap[z][c] = make([]byte, width)
		}
	}

	startY := startRow * 6
	endY := endRow * 6
	if endY > totalHeight {
		endY = totalHeight
	}

	switch src := img.(type) {
	case *image.RGBA:
		pix := src.Pix
		stride := src.Stride
		for y := startY; y < endY; y++ {
			rowStart := y * stride
			z := y/6 - startRow
			bit := byte(y % 6)
			if y >= paletted.Bounds().Dy() {
				continue
			}
			for x := 0; x < width; x++ {
				if x >= paletted.Bounds().Dx() {
					continue
				}
				offset := rowStart + x*4
				if pix[offset+3] != 255 {
					continue
				}
				idx := int(paletted.ColorIndexAt(x, y))
				if idx < 0 || idx >= nc {
					continue
				}
				sixelMap[z][idx][x] |= 1 << bit
			}
		}
	case *image.Paletted:
		pix := paletted.Pix
		stride := paletted.Stride
		for y := startY; y < endY; y++ {
			rowStart := y * stride
			z := y/6 - startRow
			bit := byte(y % 6)
			if y >= paletted.Bounds().Dy() {
				continue
			}
			for x := 0; x < width; x++ {
				if x >= paletted.Bounds().Dx() {
					continue
				}
				idx := int(pix[rowStart+x])
				if idx < 0 || idx >= nc {
					continue
				}
				sixelMap[z][idx][x] |= 1 << bit
			}
		}
	default:
		for y := startY; y < endY; y++ {
			z := y/6 - startRow
			bit := byte(y % 6)
			if y >= paletted.Bounds().Dy() {
				continue
			}
			for x := 0; x < width; x++ {
				if x >= paletted.Bounds().Dx() {
					continue
				}
				_, _, _, a := img.At(x, y).RGBA()
				if a != 0xFFFF {
					continue
				}
				idx := int(paletted.ColorIndexAt(x, y))
				if idx < 0 || idx >= nc {
					continue
				}
				sixelMap[z][idx][x] |= 1 << bit
			}
		}
	}

	resultChan <- stripResult{
		startRow: startRow,
		sixelMap: sixelMap,
	}
}

// encodeSixelRows encodes sixel rows in order.
//
// encodeSixelRows 按顺序编码sixel行。
func (e *Encoder) encodeSixelRows(outBuf *bytes.Buffer, resultChan <-chan stripResult, totalRows, width, paletteSize int) {
	orderedResults := make([][][]byte, totalRows)

	for res := range resultChan {
		for i, colorData := range res.sixelMap {
			rowIdx := res.startRow + i
			if rowIdx < totalRows {
				orderedResults[rowIdx] = colorData
			}
		}
	}

	tempBuf := make([]byte, 0, 256)
	for z := 0; z < totalRows; z++ {
		if z > 0 {
			outBuf.WriteByte(0x2d)
		}

		if z >= len(orderedResults) || orderedResults[z] == nil {
			continue
		}

		colorData := orderedResults[z]

		for colorIdx := 0; colorIdx < paletteSize; colorIdx++ {
			sixelRow := colorData[colorIdx]

			hasData := false
			for x := 0; x < width; x++ {
				if sixelRow[x] != 0 {
					hasData = true
					break
				}
			}
			if !hasData {
				continue
			}

			outBuf.WriteByte(0x24)
			outBuf.WriteByte(0x23)

			colorNum := colorIdx + 1
			if colorNum >= 100 {
				outBuf.Write([]byte{
					byte(0x30 + colorNum/100),
					byte(0x30 + (colorNum%100)/10),
					byte(0x30 + colorNum%10),
				})
			} else if colorNum >= 10 {
				outBuf.Write([]byte{
					byte(0x30 + colorNum/10),
					byte(0x30 + colorNum%10),
				})
			} else {
				outBuf.WriteByte(byte(0x30 + colorNum))
			}

			var lastCh byte
			runCount := 0
			for x := 0; x <= width; x++ {
				var ch byte
				if x < width {
					ch = sixelRow[x]
				} else {
					ch = 0xff
				}

				if ch != lastCh || runCount == 255 {
					if runCount > 0 {
						sixelChar := lastCh + 63
						tempBuf = tempBuf[:0]

						if runCount > 1 {
							tempBuf = append(tempBuf, 0x21)
							if runCount >= 100 {
								tempBuf = append(tempBuf,
									byte(0x30+runCount/100),
									byte(0x30+(runCount%100)/10),
									byte(0x30+runCount%10),
								)
							} else if runCount >= 10 {
								tempBuf = append(tempBuf,
									byte(0x30+runCount/10),
									byte(0x30+runCount%10),
								)
							} else {
								tempBuf = append(tempBuf, byte(0x30+runCount))
							}
						}
						tempBuf = append(tempBuf, sixelChar)
						outBuf.Write(tempBuf)
					}
					lastCh = ch
					runCount = 1
				} else {
					runCount++
				}
			}
		}
	}
}
