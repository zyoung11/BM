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
	palRGB [][3]uint8
	cube   [32768]uint16
}

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
	for sy := range sampleH {
		for sx := range sampleW {
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

	p.palRGB = make([][3]uint8, nc)
	for i, c := range p.colors {
		cr, cg, cb, _ := c.RGBA()
		p.palRGB[i] = [3]uint8{uint8(cr >> 8), uint8(cg >> 8), uint8(cb >> 8)}
	}

	for ri := range 32 {
		rCenter := ri*8 + 4
		for gi := range 32 {
			gCenter := gi*8 + 4
			for bi := range 32 {
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
	p := job.pal

	errRows := make([][][3]int, nRows+1)
	for i := range nRows + 1 {
		errRows[i] = make([][3]int, imgWidth+2)
	}

	rowBytes := imgWidth * 4

	for dy := range nRows {
		y := job.yStart + dy
		if y >= imgHeight {
			continue
		}
		bit := byte(1 << dy)
		curErr := errRows[dy]
		nextErr := errRows[dy+1]
		pi := y * rowBytes

		for x := range imgWidth {
			r := clampByteInt(int(data[pi]) + curErr[x][0])
			g := clampByteInt(int(data[pi+1]) + curErr[x][1])
			b := clampByteInt(int(data[pi+2]) + curErr[x][2])

			ci := int(p.cube[(r>>3)*1024+(g>>3)*32+(b>>3)])
			pr := p.palRGB[ci][0]
			pg := p.palRGB[ci][1]
			pb := p.palRGB[ci][2]

			errR := int(data[pi]) - int(pr)
			errG := int(data[pi+1]) - int(pg)
			errB := int(data[pi+2]) - int(pb)

			curErr[x+1][0] += errR * 7 / 16
			curErr[x+1][1] += errG * 7 / 16
			curErr[x+1][2] += errB * 7 / 16

			if x > 0 {
				nextErr[x-1][0] += errR * 3 / 16
				nextErr[x-1][1] += errG * 3 / 16
				nextErr[x-1][2] += errB * 3 / 16
			}
			nextErr[x][0] += errR * 5 / 16
			nextErr[x][1] += errG * 5 / 16
			nextErr[x][2] += errB * 5 / 16
			nextErr[x+1][0] += errR * 1 / 16
			nextErr[x+1][1] += errG * 1 / 16
			nextErr[x+1][2] += errB * 1 / 16

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

func clampByteInt(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
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
