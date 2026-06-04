package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"
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
	lutR   [64][256]uint16
	lutG   [64][256]uint16
	lutB   [64][256]uint16
}

var bayer8x8 = [64]int8{
	0, 32, 8, 40, 2, 34, 10, 42,
	48, 16, 56, 24, 50, 18, 58, 26,
	12, 44, 4, 36, 14, 46, 6, 38,
	60, 28, 52, 20, 62, 30, 54, 22,
	3, 35, 11, 43, 1, 33, 9, 41,
	51, 19, 59, 27, 49, 17, 57, 25,
	15, 47, 7, 39, 13, 45, 5, 37,
	63, 31, 55, 23, 61, 29, 53, 21,
}

func linearToSRGB(v float64) uint8 {
	if v <= 0.0031308 {
		return uint8(math.Round(v * 12.92 * 255))
	}
	return uint8(math.Round((1.055*math.Pow(v, 1.0/2.4) - 0.055) * 255))
}

func srgbToLinear(v uint8) float64 {
	s := float64(v) / 255.0
	if s <= 0.04045 {
		return s / 12.92
	}
	return math.Pow((s+0.055)/1.055, 2.4)
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
				tR := float64(ri) / float64(rLevels-1)
				tG := float64(gi) / float64(gLevels-1)
				tB := float64(bi) / float64(bLevels-1)
				p.colors[ri<<rShift|gi<<gShift|bi] = color.RGBA{
					linearToSRGB(tR),
					linearToSRGB(tG),
					linearToSRGB(tB),
					255,
				}
			}
		}
	}

	dR, dG, dB := makeGammaDither(rLevels, dither), makeGammaDither(gLevels, dither), makeGammaDither(bLevels, dither)

	for b := range 64 {
		for v := range 256 {
			lin := srgbToLinear(uint8(v))

			q := lin + dR[b]
			if q < 0 {
				q = 0
			} else if q > 1 {
				q = 1
			}
			idx := int(math.Round(q * float64(rLevels-1)))
			p.lutR[b][v] = uint16(idx << rShift)

			q = lin + dG[b]
			if q < 0 {
				q = 0
			} else if q > 1 {
				q = 1
			}
			idx = int(math.Round(q * float64(gLevels-1)))
			p.lutG[b][v] = uint16(idx << gShift)

			q = lin + dB[b]
			if q < 0 {
				q = 0
			} else if q > 1 {
				q = 1
			}
			idx = int(math.Round(q * float64(bLevels-1)))
			p.lutB[b][v] = uint16(idx)
		}
	}
	return p
}

func makeGammaDither(levels int, enabled bool) [64]float64 {
	var d [64]float64
	if !enabled {
		return d
	}
	step := 1.0 / float64(levels-1)
	for i, v := range bayer8x8 {
		d[i] = (float64(v) - 31.5) / 64.0 * step * 0.35
	}
	return d
}

var paletteLevels = []int{2, 8, 16, 32, 64, 128, 256, 512}
var paletteBits = [][3]int{{0, 0, 0}, {1, 1, 1}, {2, 1, 1}, {2, 2, 1}, {2, 2, 2}, {3, 2, 2}, {3, 3, 2}, {3, 3, 3}}
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

	Workers int
}

// NewEncoder returns a new instance of Encoder.
//
// NewEncoder 返回一个新的 Encoder 实例。
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w, Workers: runtime.NumCPU(), Dither: true}
}

// Encode encodes an image to the sixel format in parallel, using
// gamma-aware uniform quantization with optional Bayer 8×8 ordered dithering.
//
// Encode 并行地将图像编码为sixel格式，使用gamma感知的均匀量化和可选的Bayer 8×8有序抖动。
func (e *Encoder) Encode(img image.Image) error {
	nc := e.Colors
	if nc < 2 {
		nc = 512
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
		yb8 := (y & 7) << 3
		bit := byte(1 << dy)

		r := [4]*[256]uint16{
			&job.pal.lutR[yb8|0], &job.pal.lutR[yb8|1], &job.pal.lutR[yb8|2], &job.pal.lutR[yb8|3],
		}
		g := [4]*[256]uint16{
			&job.pal.lutG[yb8|0], &job.pal.lutG[yb8|1], &job.pal.lutG[yb8|2], &job.pal.lutG[yb8|3],
		}
		b := [4]*[256]uint16{
			&job.pal.lutB[yb8|0], &job.pal.lutB[yb8|1], &job.pal.lutB[yb8|2], &job.pal.lutB[yb8|3],
		}
		r4, r5, r6, r7 := &job.pal.lutR[yb8|4], &job.pal.lutR[yb8|5], &job.pal.lutR[yb8|6], &job.pal.lutR[yb8|7]
		g4, g5, g6, g7 := &job.pal.lutG[yb8|4], &job.pal.lutG[yb8|5], &job.pal.lutG[yb8|6], &job.pal.lutG[yb8|7]
		b4, b5, b6, b7 := &job.pal.lutB[yb8|4], &job.pal.lutB[yb8|5], &job.pal.lutB[yb8|6], &job.pal.lutB[yb8|7]

		pi := y * rowBytes
		lim := imgWidth &^ 7
		for x := 0; x < lim; x += 8 {
			ci := int(r[0][data[pi]]) | int(g[0][data[pi+1]]) | int(b[0][data[pi+2]])
			st.seenAndSet(ci, nc, x, bit)
			ci = int(r[1][data[pi+4]]) | int(g[1][data[pi+5]]) | int(b[1][data[pi+6]])
			st.seenAndSet(ci, nc, x+1, bit)
			ci = int(r[2][data[pi+8]]) | int(g[2][data[pi+9]]) | int(b[2][data[pi+10]])
			st.seenAndSet(ci, nc, x+2, bit)
			ci = int(r[3][data[pi+12]]) | int(g[3][data[pi+13]]) | int(b[3][data[pi+14]])
			st.seenAndSet(ci, nc, x+3, bit)
			ci = int(r4[data[pi+16]]) | int(g4[data[pi+17]]) | int(b4[data[pi+18]])
			st.seenAndSet(ci, nc, x+4, bit)
			ci = int(r5[data[pi+20]]) | int(g5[data[pi+21]]) | int(b5[data[pi+22]])
			st.seenAndSet(ci, nc, x+5, bit)
			ci = int(r6[data[pi+24]]) | int(g6[data[pi+25]]) | int(b6[data[pi+26]])
			st.seenAndSet(ci, nc, x+6, bit)
			ci = int(r7[data[pi+28]]) | int(g7[data[pi+29]]) | int(b7[data[pi+30]])
			st.seenAndSet(ci, nc, x+7, bit)
			pi += 32
		}
		for x := lim; x < imgWidth; x++ {
			bayer := yb8 | (x & 7)
			ci := int(job.pal.lutR[bayer][data[pi]]) | int(job.pal.lutG[bayer][data[pi+1]]) | int(job.pal.lutB[bayer][data[pi+2]])
			st.seenAndSet(ci, nc, x, bit)
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

func (st *stripState) seenAndSet(ci, nc, x int, bit byte) {
	if ci >= nc {
		return
	}
	if st.seen[ci] != st.epoch {
		st.seen[ci] = st.epoch
		st.dirty = append(st.dirty, ci)
	}
	st.buf[ci*len(st.buf)/nc+x] |= bit
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
