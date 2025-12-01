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
	"sync/atomic"
	"time"
)

// 协议类型
type Protocol int

const (
	ProtocolAuto Protocol = iota
	ProtocolSixel
	ProtocolKitty
	ProtocolITerm2
	ProtocolHalfblocks
)

// 全局Kitty图像ID计数器
var kittyImageID uint32 = uint32(os.Getpid()<<16) + uint32(time.Now().UnixMicro()&0xFFFF)

// 检测终端支持的协议
func DetectTerminalProtocol() Protocol {
	// 首先检查配置中是否指定了协议
	if GlobalConfig != nil && GlobalConfig.App.ImageProtocol != "" {
		switch strings.ToLower(GlobalConfig.App.ImageProtocol) {
		case "auto":
			// 继续自动检测
		case "kitty":
			return ProtocolKitty
		case "sixel":
			return ProtocolSixel
		case "iterm2":
			return ProtocolITerm2
		case "halfblocks":
			return ProtocolHalfblocks
		}
	}

	// 检查环境变量
	termProgram := os.Getenv("TERM_PROGRAM")
	termName := strings.ToLower(os.Getenv("TERM"))

	// 1. 检查Kitty协议支持
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

	// 2. 检查iTerm2协议支持
	if termProgram == "iTerm.app" {
		return ProtocolITerm2
	}

	// 3. 检查Sixel协议支持（通过环境变量）
	if strings.Contains(termName, "sixel") || strings.Contains(termName, "mlterm") {
		return ProtocolSixel
	}

	// 4. 默认使用Sixel（保持向后兼容）
	return ProtocolSixel
}

// 渲染图像到终端
func RenderImage(img image.Image, widthChars, heightChars int) error {
	protocol := DetectTerminalProtocol()

	switch protocol {
	case ProtocolKitty:
		return renderKittyImage(img, widthChars, heightChars)
	case ProtocolITerm2:
		return renderITerm2Image(img, widthChars, heightChars)
	case ProtocolSixel:
		return renderSixelImage(img, widthChars, heightChars)
	case ProtocolHalfblocks:
		return renderHalfblocksImage(img, widthChars, heightChars)
	default:
		// 回退到Sixel
		return renderSixelImage(img, widthChars, heightChars)
	}
}

// Kitty协议渲染
func renderKittyImage(img image.Image, widthChars, heightChars int) error {
	bounds := img.Bounds()
	pixelWidth := bounds.Dx()
	pixelHeight := bounds.Dy()

	// 生成唯一的图像ID
	imageID := atomic.AddUint32(&kittyImageID, 1)

	// 将图像转换为RGBA
	rgbaImg := image.NewRGBA(bounds)
	draw.Draw(rgbaImg, rgbaImg.Bounds(), img, bounds.Min, draw.Src)

	// 获取原始RGBA数据
	data := rgbaImg.Pix

	// 使用zlib压缩（可选，但推荐）
	var compressed bool
	var compressedData []byte
	if len(data) > 1024 { // 只对大图像压缩
		var buf bytes.Buffer
		w, err := zlib.NewWriterLevel(&buf, zlib.BestSpeed)
		if err != nil {
			return fmt.Errorf("failed to create zlib writer: %v", err)
		}
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("failed to write to zlib writer: %v", err)
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("failed to close zlib writer: %v", err)
		}
		compressedData = buf.Bytes()
		compressed = true
	} else {
		compressedData = data
	}

	// Base64编码
	encoded := base64.StdEncoding.EncodeToString(compressedData)

	// 构建控制序列
	var control string
	if compressed {
		control = fmt.Sprintf("a=T,f=32,i=%d,s=%d,v=%d,c=%d,r=%d,q=2,o=z",
			imageID, pixelWidth, pixelHeight, widthChars, heightChars)
	} else {
		control = fmt.Sprintf("a=T,f=32,i=%d,s=%d,v=%d,c=%d,r=%d,q=2",
			imageID, pixelWidth, pixelHeight, widthChars, heightChars)
	}

	// 检查是否在tmux中
	inTmux := os.Getenv("TMUX") != ""

	// 发送图像数据（分块发送以避免缓冲区溢出）
	chunkSize := 4096 // 4KB chunks
	first := true

	for i := 0; i < len(encoded); i += chunkSize {
		end := i + chunkSize
		if end > len(encoded) {
			end = len(encoded)
		}

		chunk := encoded[i:end]
		var chunkSequence string

		if first {
			first = false
			if end < len(encoded) {
				// 更多块要发送
				chunkSequence = fmt.Sprintf("\x1b_G%s,m=1;%s\x1b\\", control, chunk)
			} else {
				// 单一块
				chunkSequence = fmt.Sprintf("\x1b_G%s;%s\x1b\\", control, chunk)
			}
		} else {
			if end < len(encoded) {
				// 继续块
				chunkSequence = fmt.Sprintf("\x1b_Gm=1,q=2;%s\x1b\\", chunk)
			} else {
				// 最后一块
				chunkSequence = fmt.Sprintf("\x1b_Gm=0,q=2;%s\x1b\\", chunk)
			}
		}

		// 包装tmux穿透
		if inTmux {
			chunkSequence = "\x1bPtmux;" + strings.ReplaceAll(chunkSequence, "\x1b", "\x1b\x1b") + "\x1b\\"
		}

		// 发送到终端
		if _, err := io.WriteString(os.Stdout, chunkSequence); err != nil {
			return fmt.Errorf("failed to write kitty image data: %v", err)
		}
	}

	return nil
}

// iTerm2协议渲染
func renderITerm2Image(img image.Image, widthChars, heightChars int) error {
	// iTerm2使用Base64编码的PNG数据
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return fmt.Errorf("failed to encode PNG: %v", err)
	}

	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())

	// iTerm2图像序列
	sequence := fmt.Sprintf("\x1b]1337;File=inline=1;width=%d;height=%d:%s\x07",
		widthChars, heightChars, encoded)

	// 检查是否在tmux中
	if os.Getenv("TMUX") != "" {
		sequence = "\x1bPtmux;" + strings.ReplaceAll(sequence, "\x1b", "\x1b\x1b") + "\x1b\\"
	}

	// 发送到终端
	if _, err := io.WriteString(os.Stdout, sequence); err != nil {
		return fmt.Errorf("failed to write iTerm2 image data: %v", err)
	}

	return nil
}

// Sixel协议渲染（使用现有的编码器）
func renderSixelImage(img image.Image, widthChars, heightChars int) error {
	// 使用现有的sixel编码器
	return NewEncoder(os.Stdout).Encode(img)
}

// Halfblocks协议渲染（Unicode半块字符）
func renderHalfblocksImage(img image.Image, widthChars, heightChars int) error {
	bounds := img.Bounds()
	imgWidth := bounds.Dx()
	imgHeight := bounds.Dy()

	// 计算缩放比例
	scaleX := float64(imgWidth) / float64(widthChars*2) // 每个字符2个像素宽
	scaleY := float64(imgHeight) / float64(heightChars)

	var output strings.Builder

	for y := 0; y < heightChars; y++ {
		for x := 0; x < widthChars; x++ {
			// 获取上下两个像素
			topY := int(float64(y) * scaleY)
			bottomY := int(float64(y+1)*scaleY) - 1
			if bottomY < topY {
				bottomY = topY
			}

			leftX := int(float64(x*2) * scaleX)

			// 获取像素颜色
			topColor := img.At(leftX, topY)
			bottomColor := img.At(leftX, bottomY)

			// 转换为灰度
			topR, topG, topB, _ := topColor.RGBA()
			bottomR, bottomG, bottomB, _ := bottomColor.RGBA()

			topGray := (topR*299 + topG*587 + topB*114) / 1000
			bottomGray := (bottomR*299 + bottomG*587 + bottomB*114) / 1000

			// 选择Unicode半块字符
			var blockChar rune
			if topGray > 0x7FFF && bottomGray > 0x7FFF {
				blockChar = ' ' // 全白
			} else if topGray > 0x7FFF && bottomGray <= 0x7FFF {
				blockChar = '▀' // 上白下黑
			} else if topGray <= 0x7FFF && bottomGray > 0x7FFF {
				blockChar = '▄' // 上黑下白
			} else {
				blockChar = '█' // 全黑
			}

			output.WriteRune(blockChar)
		}
		output.WriteString("\n")
	}

	// 输出到终端
	_, err := io.WriteString(os.Stdout, output.String())
	return err
}

// 清除Kitty图像
func ClearKittyImages() error {
	// Kitty清除所有图像命令
	sequence := "\x1b_Ga=d\x1b\\"

	// 检查是否在tmux中
	if os.Getenv("TMUX") != "" {
		sequence = "\x1bPtmux;" + strings.ReplaceAll(sequence, "\x1b", "\x1b\x1b") + "\x1b\\"
	}

	// 发送到终端
	if _, err := io.WriteString(os.Stdout, sequence); err != nil {
		return fmt.Errorf("failed to clear kitty images: %v", err)
	}

	return nil
}

// 获取终端字体大小（用于像素到字符的转换）
func GetTerminalFontSize() (width, height int, err error) {
	// 尝试CSI 16t查询字符单元格大小
	fmt.Print("\x1b[16t")

	var buf [32]byte
	n, err := os.Stdin.Read(buf[:])
	if err != nil {
		// 回退到默认值
		return 8, 16, nil
	}

	response := string(buf[:n])
	// 解析响应格式: \x1b[8;高度;宽度t
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

	// 回退到默认值
	return 8, 16, nil
}

// 检查终端是否支持真彩色
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
