package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"io"
	"runtime"
	"sync"
)

var sixelRLEBufPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

var stripStatePool = sync.Pool{
	New: func() any { return &stripState{} },
}

type stripState struct {
	buf   []byte
	seen  []uint16
	epoch uint16
	dirty []int
}

type stripJob struct {
	sixelRow int
	yStart   int
	yEnd     int
	data     []byte
	width    int
	pal      *sixelPalette
}

type stripResult struct {
	sixelRow int
	rleData  []byte
}

type sixelPalette struct {
	nc     int
	colors []color.Color
	lutR   [16][256]uint8
	lutG   [16][256]uint8
	lutB   [16][256]uint8
}

var bayer4x4 = [16]int8{0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5}

func makeDither(levels int) [16]int8 {
	step := 256 / (levels - 1)
	var d [16]int8
	for i, v := range bayer4x4 {
		d[i] = int8((int(v) - 8) * step / 16)
	}
	return d
}

func newSixelPalette(rBits, gBits, bBits int, dither bool) *sixelPalette {
	rLevels := 1 << rBits
	gLevels := 1 << gBits
	bLevels := 1 << bBits
	nc := rLevels * gLevels * bLevels
	gShift := bBits
	rShift := gBits + bBits

	p := &sixelPalette{nc: nc, colors: make([]color.Color, nc)}
	for ri := range rLevels {
		for gi := range gLevels {
			for bi := range bLevels {
				p.colors[ri<<rShift|gi<<gShift|bi] = color.RGBA{
					uint8(ri * 255 / (rLevels - 1)),
					uint8(gi * 255 / (gLevels - 1)),
					uint8(bi * 255 / (bLevels - 1)),
					255,
				}
			}
		}
	}

	var dR, dG, dB [16]int8
	if dither {
		dR = makeDither(rLevels)
		dG = makeDither(gLevels)
		dB = makeDither(bLevels)
	}

	for b := range 16 {
		for v := range 256 {
			s := v + int(dR[b])
			if s < 0 {
				s = 0
			} else if s > 255 {
				s = 255
			}
			p.lutR[b][v] = uint8(s>>(8-rBits)) << rShift

			s = v + int(dG[b])
			if s < 0 {
				s = 0
			} else if s > 255 {
				s = 255
			}
			p.lutG[b][v] = uint8(s>>(8-gBits)) << gShift

			s = v + int(dB[b])
			if s < 0 {
				s = 0
			} else if s > 255 {
				s = 255
			}
			p.lutB[b][v] = uint8(s >> (8 - bBits))
		}
	}
	return p
}

var paletteLevels = []int{2, 8, 16, 32, 64, 128, 256}
var paletteBits = [][3]int{{0, 0, 0}, {1, 1, 1}, {2, 1, 1}, {2, 2, 1}, {2, 2, 2}, {3, 2, 2}, {3, 3, 2}}
var paletteCache sync.Map

func getSixelPalette(nc int, dither bool) *sixelPalette {
	type cacheKey struct {
		nc     int
		dither bool
	}
	key := cacheKey{nc, dither}
	if v, ok := paletteCache.Load(key); ok {
		return v.(*sixelPalette)
	}
	for i, lvl := range paletteLevels {
		if lvl == nc {
			bits := paletteBits[i]
			p := newSixelPalette(bits[0], bits[1], bits[2], dither)
			paletteCache.Store(key, p)
			return p
		}
	}
	return nil
}

func nearestPaletteLevel(nc int) int {
	for _, lvl := range paletteLevels {
		if lvl >= nc {
			return lvl
		}
	}
	return paletteLevels[len(paletteLevels)-1]
}

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
	return &Encoder{w: w, Workers: runtime.NumCPU(), Dither: true}
}

// Encode encodes an image to the sixel format in parallel, using uniform
// quantization with optional Bayer ordered dithering for high performance.
//
// Encode 并行地将图像编码为sixel格式，使用均匀量化和可选的Bayer有序抖动，性能极高。
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

	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
	data := rgba.Pix

	palLevel := nearestPaletteLevel(nc)
	pal := getSixelPalette(palLevel, e.Dither)

	estSize := width * height / 2
	if estSize < 65536 {
		estSize = 65536
	}
	outBuf := bytes.NewBuffer(make([]byte, 0, estSize))

	outBuf.Write([]byte{0x1b, 0x50, 0x30, 0x3b, 0x30, 0x3b, 0x38, 0x71, 0x22, 0x31, 0x3b, 0x31})

	for i := 0; i < pal.nc; i++ {
		r, g, b, _ := pal.colors[i].RGBA()
		outBuf.WriteByte('#')
		writeSixelNum(outBuf, i+1)
		outBuf.WriteString(";2;")
		writeSixelNum(outBuf, int(r*100/0xFFFF))
		outBuf.WriteByte(';')
		writeSixelNum(outBuf, int(g*100/0xFFFF))
		outBuf.WriteByte(';')
		writeSixelNum(outBuf, int(b*100/0xFFFF))
	}

	totalSixelRows := (height + 5) / 6
	workers := e.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > totalSixelRows {
		workers = totalSixelRows
	}

	jobCh := make(chan stripJob, workers)
	resultCh := make(chan stripResult, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Go(func() {
			for job := range jobCh {
				processStrip(job, resultCh)
			}
		})
	}

	makeJob := func(sixelRow int) stripJob {
		yStart := sixelRow * 6
		yEnd := yStart + 6
		if yEnd > height {
			yEnd = height
		}
		return stripJob{sixelRow: sixelRow, yStart: yStart, yEnd: yEnd, data: data, width: width, pal: pal}
	}

	for i := 0; i < totalSixelRows && i < workers; i++ {
		jobCh <- makeJob(i)
	}
	jobsSent := min(workers, totalSixelRows)

	pending := make(map[int][]byte)
	nextRow := 0
	received := 0

	for received < totalSixelRows {
		res := <-resultCh
		received++
		pending[res.sixelRow] = res.rleData

		for {
			d, ok := pending[nextRow]
			if !ok {
				break
			}
			outBuf.Write(d)
			delete(pending, nextRow)
			nextRow++
		}

		if jobsSent < totalSixelRows {
			jobCh <- makeJob(jobsSent)
			jobsSent++
		}
	}

	close(jobCh)
	wg.Wait()

	outBuf.Write([]byte{0x1b, 0x5c})
	_, err := outBuf.WriteTo(e.w)
	return err
}

func processStrip(job stripJob, resultCh chan<- stripResult) {
	st := stripStatePool.Get().(*stripState)

	nc := job.pal.nc
	stripCap := nc * job.width
	if cap(st.buf) < stripCap {
		st.buf = make([]byte, stripCap)
	}
	st.buf = st.buf[:stripCap]

	if cap(st.seen) < nc {
		st.seen = make([]uint16, nc)
	}
	st.seen = st.seen[:nc]

	st.epoch++
	if st.epoch == 0 {
		clear(st.seen)
		st.epoch = 1
	}
	clear(st.buf)
	st.dirty = st.dirty[:0]

	data := job.data
	imgWidth := job.width
	imgHeight := (len(data) / 4) / imgWidth
	nRows := job.yEnd - job.yStart

	rowBytes := imgWidth * 4

	for dy := range nRows {
		y := job.yStart + dy
		if y >= imgHeight {
			continue
		}
		yb4 := (y & 3) << 2
		bit := byte(1 << dy)

		r0, r1, r2, r3 := &job.pal.lutR[yb4|0], &job.pal.lutR[yb4|1], &job.pal.lutR[yb4|2], &job.pal.lutR[yb4|3]
		g0, g1, g2, g3 := &job.pal.lutG[yb4|0], &job.pal.lutG[yb4|1], &job.pal.lutG[yb4|2], &job.pal.lutG[yb4|3]
		b0, b1, b2, b3 := &job.pal.lutB[yb4|0], &job.pal.lutB[yb4|1], &job.pal.lutB[yb4|2], &job.pal.lutB[yb4|3]

		pi := y * rowBytes
		lim := imgWidth &^ 3
		for x := 0; x < lim; x += 4 {
			ci := int(r0[data[pi]]) | int(g0[data[pi+1]]) | int(b0[data[pi+2]])
			if ci < nc {
				if st.seen[ci] != st.epoch {
					st.seen[ci] = st.epoch
					st.dirty = append(st.dirty, ci)
				}
				st.buf[ci*imgWidth+x] |= bit
			}
			ci = int(r1[data[pi+4]]) | int(g1[data[pi+5]]) | int(b1[data[pi+6]])
			if ci < nc {
				if st.seen[ci] != st.epoch {
					st.seen[ci] = st.epoch
					st.dirty = append(st.dirty, ci)
				}
				st.buf[ci*imgWidth+x+1] |= bit
			}
			ci = int(r2[data[pi+8]]) | int(g2[data[pi+9]]) | int(b2[data[pi+10]])
			if ci < nc {
				if st.seen[ci] != st.epoch {
					st.seen[ci] = st.epoch
					st.dirty = append(st.dirty, ci)
				}
				st.buf[ci*imgWidth+x+2] |= bit
			}
			ci = int(r3[data[pi+12]]) | int(g3[data[pi+13]]) | int(b3[data[pi+14]])
			if ci < nc {
				if st.seen[ci] != st.epoch {
					st.seen[ci] = st.epoch
					st.dirty = append(st.dirty, ci)
				}
				st.buf[ci*imgWidth+x+3] |= bit
			}
			pi += 16
		}
		for x := lim; x < imgWidth; x++ {
			b := yb4 | (x & 3)
			ci := int(job.pal.lutR[b][data[pi]]) | int(job.pal.lutG[b][data[pi+1]]) | int(job.pal.lutB[b][data[pi+2]])
			if ci < nc {
				if st.seen[ci] != st.epoch {
					st.seen[ci] = st.epoch
					st.dirty = append(st.dirty, ci)
				}
				st.buf[ci*imgWidth+x] |= bit
			}
			pi += 4
		}
	}

	localBuf := sixelRLEBufPool.Get().(*bytes.Buffer)
	localBuf.Reset()
	encodeStrip(localBuf, st, job.sixelRow, imgWidth)
	rleBytes := make([]byte, localBuf.Len())
	copy(rleBytes, localBuf.Bytes())
	sixelRLEBufPool.Put(localBuf)

	releaseStripState(st)
	resultCh <- stripResult{sixelRow: job.sixelRow, rleData: rleBytes}
}

func encodeStrip(buf *bytes.Buffer, st *stripState, sixelRow, width int) {
	if sixelRow > 0 {
		buf.WriteByte(0x2d)
	}

	for _, c := range st.dirty {
		base := c * width
		row := st.buf[base : base+width]

		buf.WriteByte(0x24)
		buf.WriteByte(0x23)
		writeSixelNum(buf, c+1)

		var lastCh byte
		runCount := 0
		for x := 0; x <= width; x++ {
			var ch byte
			if x < width {
				ch = row[x]
			} else {
				ch = 0xff
			}
			if ch != lastCh || runCount == 255 {
				if runCount > 0 {
					sixelChar := lastCh + 63
					if runCount > 1 {
						buf.WriteByte(0x21)
						writeSixelNum(buf, runCount)
					}
					buf.WriteByte(sixelChar)
				}
				lastCh = ch
				runCount = 1
			} else {
				runCount++
			}
		}
	}
}

func releaseStripState(st *stripState) {
	st.dirty = st.dirty[:0]
	stripStatePool.Put(st)
}

func writeSixelNum(b *bytes.Buffer, n int) {
	if n >= 100 {
		b.Write([]byte{
			byte(0x30 + n/100),
			byte(0x30 + (n%100)/10),
			byte(0x30 + n%10),
		})
	} else if n >= 10 {
		b.Write([]byte{
			byte(0x30 + n/10),
			byte(0x30 + n%10),
		})
	} else {
		b.WriteByte(byte(0x30 + n))
	}
}
