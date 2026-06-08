package main

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Protocol int

const (
	ProtocolAuto Protocol = iota
	ProtocolSixel
	ProtocolKitty
	ProtocolITerm2
)

var kittyImageID uint32 = uint32(os.Getpid()<<16) + uint32(time.Now().UnixMicro()&0xFFFF)

var kittyZlibPool = sync.Pool{
	New: func() any {
		w, _ := zlib.NewWriterLevel(nil, zlib.BestSpeed)
		return w
	},
}

var kittyCompressPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

var kittyBase64Pool = sync.Pool{
	New: func() any {
		buf := make([]byte, 128*1024)
		return &buf
	},
}

func DetectTerminalProtocol() Protocol {
	if GlobalConfig != nil && GlobalConfig.App.ImageProtocol != "" {
		switch strings.ToLower(GlobalConfig.App.ImageProtocol) {
		case "auto":
		case "kitty":
			return ProtocolKitty
		case "sixel":
			return ProtocolSixel
		case "iterm2":
			return ProtocolITerm2
		}
	}

	termProgram := os.Getenv("TERM_PROGRAM")
	termName := strings.ToLower(os.Getenv("TERM"))

	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return ProtocolKitty
	}
	if strings.Contains(termName, "kitty") {
		return ProtocolKitty
	}
	if termProgram == "WezTerm" || termProgram == "ghostty" || termProgram == "rio" {
		return ProtocolKitty
	}
	if os.Getenv("WEZTERM_EXECUTABLE") != "" {
		return ProtocolKitty
	}

	if termProgram == "iTerm.app" {
		return ProtocolITerm2
	}

	sixelTerms := []string{"sixel", "mlterm", "foot", "contour", "xterm-sixel", "yaft-256color"}
	for _, t := range sixelTerms {
		if strings.Contains(termName, t) {
			return ProtocolSixel
		}
	}
	if termProgram == "foot" || termProgram == "contour" || termProgram == "mintty" || termProgram == "RLogin" {
		return ProtocolSixel
	}

	return ProtocolSixel
}

func RenderImage(img image.Image, widthChars, heightChars int) error {
	protocol := DetectTerminalProtocol()

	switch protocol {
	case ProtocolKitty:
		return renderKittyImage(img, widthChars, heightChars)
	case ProtocolITerm2:
		return renderITerm2Image(img, widthChars, heightChars)
	case ProtocolSixel:
		return renderSixelImage(img)
	default:
		return renderSixelImage(img)
	}
}

func renderKittyImage(img image.Image, widthChars, heightChars int) error {
	bounds := img.Bounds()
	pixelWidth := bounds.Dx()
	pixelHeight := bounds.Dy()

	imageID := atomic.AddUint32(&kittyImageID, 1)

	rgbaImg := image.NewRGBA(bounds)
	draw.Draw(rgbaImg, rgbaImg.Bounds(), img, bounds.Min, draw.Src)

	data := rgbaImg.Pix

	var compressed bool
	var compressedData []byte
	if len(data) > 1024 {
		buf := kittyCompressPool.Get().(*bytes.Buffer)
		buf.Reset()
		w := kittyZlibPool.Get().(*zlib.Writer)
		w.Reset(buf)
		w.Write(data)
		w.Close()
		compressedData = make([]byte, buf.Len())
		copy(compressedData, buf.Bytes())
		kittyZlibPool.Put(w)
		kittyCompressPool.Put(buf)
		compressed = true
	} else {
		compressedData = data
	}

	encLen := base64.StdEncoding.EncodedLen(len(compressedData))
	base64RawPtr := kittyBase64Pool.Get().(*[]byte)
	base64Raw := *base64RawPtr
	if cap(base64Raw) < encLen {
		base64Raw = make([]byte, encLen)
		*base64RawPtr = base64Raw
	}
	base64Buf := base64Raw[:encLen]
	base64.StdEncoding.Encode(base64Buf, compressedData)

	var control string
	if compressed {
		control = fmt.Sprintf("a=T,f=32,i=%d,s=%d,v=%d,c=%d,r=%d,q=2,o=z",
			imageID, pixelWidth, pixelHeight, widthChars, heightChars)
	} else {
		control = fmt.Sprintf("a=T,f=32,i=%d,s=%d,v=%d,c=%d,r=%d,q=2",
			imageID, pixelWidth, pixelHeight, widthChars, heightChars)
	}

	inTmux := os.Getenv("TMUX") != ""

	chunkSize := 4096
	first := true

	for i := 0; i < encLen; i += chunkSize {
		end := i + chunkSize
		if end > encLen {
			end = encLen
		}

		chunk := base64Buf[i:end]
		var chunkSequence string

		if first {
			first = false
			if end < encLen {
				chunkSequence = fmt.Sprintf("\x1b_G%s,m=1;%s\x1b\\", control, chunk)
			} else {
				chunkSequence = fmt.Sprintf("\x1b_G%s;%s\x1b\\", control, chunk)
			}
		} else {
			if end < encLen {
				chunkSequence = fmt.Sprintf("\x1b_Gm=1,q=2;%s\x1b\\", chunk)
			} else {
				chunkSequence = fmt.Sprintf("\x1b_Gm=0,q=2;%s\x1b\\", chunk)
			}
		}

		if inTmux {
			chunkSequence = "\x1bPtmux;" + strings.ReplaceAll(chunkSequence, "\x1b", "\x1b\x1b") + "\x1b\\"
		}

		if _, err := io.WriteString(os.Stdout, chunkSequence); err != nil {
			kittyBase64Pool.Put(base64RawPtr)
			return fmt.Errorf("failed to write kitty image data: %v\n\n无法写入 kitty 图像数据: %v", err, err)
		}
	}

	kittyBase64Pool.Put(base64RawPtr)
	return nil
}

func renderITerm2Image(img image.Image, widthChars, heightChars int) error {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return fmt.Errorf("failed to encode PNG: %v\n\n无法编码 PNG: %v", err, err)
	}

	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())

	sequence := fmt.Sprintf("\x1b]1337;File=inline=1;width=%d;height=%d:%s\x07",
		widthChars, heightChars, encoded)

	if os.Getenv("TMUX") != "" {
		sequence = "\x1bPtmux;" + strings.ReplaceAll(sequence, "\x1b", "\x1b\x1b") + "\x1b\\"
	}

	if _, err := io.WriteString(os.Stdout, sequence); err != nil {
		return fmt.Errorf("failed to write iTerm2 image data: %v\n\n无法写入 iTerm2 图像数据: %v", err, err)
	}

	return nil
}

func renderSixelImage(img image.Image) error {
	return NewEncoder(os.Stdout).Encode(img)
}

func ClearKittyImages() error {
	sequence := "\x1b_Ga=d\x1b\\"

	if os.Getenv("TMUX") != "" {
		sequence = "\x1bPtmux;" + strings.ReplaceAll(sequence, "\x1b", "\x1b\x1b") + "\x1b\\"
	}

	if _, err := io.WriteString(os.Stdout, sequence); err != nil {
		return fmt.Errorf("failed to clear kitty images: %v\n\n无法清除 kitty 图像: %v", err, err)
	}

	return nil
}

func GetTerminalFontSize() (width, height int, err error) {
	fmt.Print("\x1b[16t")

	var buf [32]byte
	n, err := os.Stdin.Read(buf[:])
	if err != nil {
		return 8, 16, nil
	}

	response := string(buf[:n])
	if strings.HasPrefix(response, "\x1b[8;") && strings.HasSuffix(response, "t") {
		parts := strings.Split(response[4:len(response)-1], ";")
		if len(parts) >= 2 {
			var h, w int
			if _, err := fmt.Sscanf(parts[0], "%d", &h); err == nil {
				if _, err := fmt.Sscanf(parts[1], "%d", &w); err == nil {
					return w, h, nil
				}
			}
		}
	}

	return 8, 16, nil
}

func SupportsTrueColor() bool {
	colorterm := os.Getenv("COLORTERM")
	if colorterm == "truecolor" || colorterm == "24bit" {
		return true
	}

	termProgram := os.Getenv("TERM_PROGRAM")
	if termProgram == "iTerm.app" || termProgram == "WezTerm" ||
		termProgram == "ghostty" || termProgram == "rio" {
		return true
	}

	termName := strings.ToLower(os.Getenv("TERM"))
	if strings.Contains(termName, "kitty") || strings.Contains(termName, "truecolor") {
		return true
	}

	return false
}
