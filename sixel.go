package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"io"
	"runtime"
	"sync"

	"github.com/soniakeys/quant/median"
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
	cube   [32768]uint16
	dR     [64]int16
	dG     [64]int16
	dB     [64]int16
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

const ditherScale = 0.35

func buildAdaptivePalette(img image.Image) *sixelPalette {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w == 0 || h == 0 {
		return nil
	}

	stepX := max(w/64, 1)
	stepY := max(h/64, 1)
	sampleW := (w + stepX - 1) / stepX
	sampleH := (h + stepY - 1) / stepY
	sample := image.NewRGBA(image.Rect(0, 0, sampleW, sampleH))
	for sy := 0; sy < sampleH; sy++ {
		for sx := 0; sx < sampleW; sx++ {
			sample.Set(sx, sy, img.At(sx*stepX+bounds.Min.X, sy*stepY+bounds.Min.Y))
		}
	}

	q := median.Quantizer(1023)
	paletted := q.Paletted(sample)
	nc := len(paletted.Palette)
	if nc > 1024 {
		nc = 1024
	}

	p := &sixelPalette{
		nc:     nc,
		colors: paletted.Palette[:nc],
	}

	for ri := 0; ri < 32; ri++ {
		rCenter := ri*8 + 4
		for gi := 0; gi < 32; gi++ {
			gCenter := gi*8 + 4
			for bi := 0; bi < 32; bi++ {
				bCenter := bi*8 + 4
				bestIdx := 0
				bestDist := int(^uint(0) >> 1)
				for i, c := range p.colors {
					cr, cg, cb, _ := c.RGBA()
					dr := int(cr>>8) - rCenter
					dg := int(cg>>8) - gCenter
					db := int(cb>>8) - bCenter
					dist := dr*dr + dg*dg + db*db
					if dist < bestDist {
						bestDist = dist
						bestIdx = i
					}
				}
				p.cube[ri*1024+gi*32+bi] = uint16(bestIdx)
			}
		}
	}

	step := 8.0
	for i := range 64 {
		v := (float64(bayer8x8[i]) - 31.5) / 64.0 * step * ditherScale
		p.dR[i] = int16(v)
		p.dG[i] = int16(v)
		p.dB[i] = int16(v)
	}

	return p
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

// Encode encodes an image to the sixel format using adaptive median-cut
// palette generation combined with a 16³ spatial lookup cube for fast
// per-pixel color mapping.
//
// Encode 使用自适应median-cut调色板生成结合16³空间查找立方体来快速映射像素颜色。
func (e *Encoder) Encode(img image.Image) error {
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

	pal := buildAdaptivePalette(img)
	if pal == nil {
		return nil
	}

	estSize := width * height / 2
	if estSize < 65536 {
		estSize = 65536
	}
	outBuf := bytes.NewBuffer(make([]byte, 0, estSize))

	outBuf.Write([]byte{0x1b, 0x50, 0x30, 0x3b, 0x30, 0x3b, 0x38, 0x71, 0x22, 0x31, 0x3b, 0x31})

	for i := range pal.nc {
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
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				processStrip(job, resultCh)
			}
		}()
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
	p := job.pal

	rowBytes := imgWidth * 4

	for dy := range nRows {
		y := job.yStart + dy
		if y >= imgHeight {
			continue
		}
		yb8 := (y & 7) << 3
		bit := byte(1 << dy)

		d0, d1, d2, d3 := p.dR[yb8|0], p.dR[yb8|1], p.dR[yb8|2], p.dR[yb8|3]
		dg0, dg1, dg2, dg3 := p.dG[yb8|0], p.dG[yb8|1], p.dG[yb8|2], p.dG[yb8|3]
		db0, db1, db2, db3 := p.dB[yb8|0], p.dB[yb8|1], p.dB[yb8|2], p.dB[yb8|3]
		d4, d5, d6, d7 := p.dR[yb8|4], p.dR[yb8|5], p.dR[yb8|6], p.dR[yb8|7]
		dg4, dg5, dg6, dg7 := p.dG[yb8|4], p.dG[yb8|5], p.dG[yb8|6], p.dG[yb8|7]
		db4, db5, db6, db7 := p.dB[yb8|4], p.dB[yb8|5], p.dB[yb8|6], p.dB[yb8|7]

		pi := y * rowBytes
		lim := imgWidth &^ 7
		for x := 0; x < lim; x += 8 {
			ci := cubeLookup(p, data[pi], data[pi+1], data[pi+2], d0, dg0, db0)
			st.seenAndSet(int(ci), nc, x, bit)
			ci = cubeLookup(p, data[pi+4], data[pi+5], data[pi+6], d1, dg1, db1)
			st.seenAndSet(int(ci), nc, x+1, bit)
			ci = cubeLookup(p, data[pi+8], data[pi+9], data[pi+10], d2, dg2, db2)
			st.seenAndSet(int(ci), nc, x+2, bit)
			ci = cubeLookup(p, data[pi+12], data[pi+13], data[pi+14], d3, dg3, db3)
			st.seenAndSet(int(ci), nc, x+3, bit)
			ci = cubeLookup(p, data[pi+16], data[pi+17], data[pi+18], d4, dg4, db4)
			st.seenAndSet(int(ci), nc, x+4, bit)
			ci = cubeLookup(p, data[pi+20], data[pi+21], data[pi+22], d5, dg5, db5)
			st.seenAndSet(int(ci), nc, x+5, bit)
			ci = cubeLookup(p, data[pi+24], data[pi+25], data[pi+26], d6, dg6, db6)
			st.seenAndSet(int(ci), nc, x+6, bit)
			ci = cubeLookup(p, data[pi+28], data[pi+29], data[pi+30], d7, dg7, db7)
			st.seenAndSet(int(ci), nc, x+7, bit)
			pi += 32
		}
		for x := lim; x < imgWidth; x++ {
			b := yb8 | (x & 7)
			ci := cubeLookup(p, data[pi], data[pi+1], data[pi+2], p.dR[b], p.dG[b], p.dB[b])
			st.seenAndSet(int(ci), nc, x, bit)
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

func cubeLookup(p *sixelPalette, r, g, b byte, dr, dg, db int16) uint16 {
	sr := int(r) + int(dr)
	if sr < 0 {
		sr = 0
	} else if sr > 255 {
		sr = 255
	}
	sg := int(g) + int(dg)
	if sg < 0 {
		sg = 0
	} else if sg > 255 {
		sg = 255
	}
	sb := int(b) + int(db)
	if sb < 0 {
		sb = 0
	} else if sb > 255 {
		sb = 255
	}
	return p.cube[(sr>>3)*1024+(sg>>3)*32+(sb>>3)]
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
	if n >= 1000 {
		b.Write([]byte{
			byte(0x30 + n/1000),
			byte(0x30 + (n%1000)/100),
			byte(0x30 + (n%100)/10),
			byte(0x30 + n%10),
		})
	} else if n >= 100 {
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
