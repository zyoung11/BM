// main.go
package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // 注册 JPEG 解码
	_ "image/png"  // 注册 PNG 解码
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/dhowden/tag"
	"github.com/mattn/go-sixel"
	"github.com/nfnt/resize"
	"golang.org/x/term"
)

// getCellSize 向终端查询单个字符单元的像素尺寸
func getCellSize() (width, height int, err error) {
	// 打印查询指令: CSI 16 t
	fmt.Print("\x1b[16t")

	// 读取终端响应, 格式为: CSI 6 ; height ; width t
	var buf []byte
	var b [1]byte
	for {
		n, err := os.Stdin.Read(b[:])
		if err != nil {
			return 0, 0, err
		}
		if n == 0 {
			continue
		}
		buf = append(buf, b[0])
		if b[0] == 't' {
			break
		}
	}

	// 健壮地解析响应: CSI P1;P2;...t
	// 我们期望的响应是: \x1b[6;H;Wt
	if !(len(buf) > 2 && buf[0] == '\x1b' && buf[1] == '[' && buf[len(buf)-1] == 't') {
		return 0, 0, fmt.Errorf("无法解析的终端响应格式: %q", buf)
	}

	// 提取 `[` 和 `t` 之间的内容
	content := buf[2 : len(buf)-1]
	parts := bytes.Split(content, []byte(";"))

	// 对于 \x1b[16t, 响应是 \x1b[6;H;W t，应该有3个部分
	if len(parts) != 3 {
		return 0, 0, fmt.Errorf("预期的响应分段为3, 实际为 %d: %q", len(parts), buf)
	}

	// 第一个分段应该是 "6"
	if string(parts[0]) != "6" {
		return 0, 0, fmt.Errorf("预期的响应代码为 6, 实际为 %s", parts[0])
	}

	h, err := strconv.Atoi(string(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("无法解析高度: %w", err)
	}

	w, err := strconv.Atoi(string(parts[2]))
	if err != nil {
		return 0, 0, fmt.Errorf("无法解析宽度: %w", err)
	}

	return w, h, nil
}

// ---------- 事件体系 ----------
type Event interface{}

type KeyPressEvent struct{}

type SignalEvent struct {
	Signal os.Signal
}

// ---------- 入口 ----------
func main() {
	if len(os.Args) != 2 {
		log.Fatalf("用法: %s <xxx.flac>", os.Args[0])
	}
	flacPath := os.Args[1]

	// 1. 进入交替缓冲区，隐藏光标；退出时还原
	fmt.Print("\x1b[?1049h\x1b[?25l")
	defer fmt.Print("\x1b[?1049l\x1b[?25h")

	// 2. 原始模式
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		log.Fatalf("failed to set raw mode: %v", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// 查询终端字符尺寸
	cellW, cellH, err := getCellSize()
	if err != nil {
		log.Fatalf("终端不支持尺寸查询: %v", err)
	}

	// 3. 主循环
	for {
		redraw(flacPath, cellW, cellH)

		switch pollOneEvent().(type) {
		case KeyPressEvent:
			return // 任意键退出
		case SignalEvent:
			continue // 窗口大小改变，继续循环
		}
	}
}

// ---------- 事件监听 ----------
func pollOneEvent() Event {
	keyCh := make(chan struct{})
	go func() {
		var b [1]byte
		os.Stdin.Read(b[:])
		close(keyCh)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	select {
	case <-keyCh:
		return KeyPressEvent{}
	case sig := <-sigCh:
		return SignalEvent{Signal: sig}
	}
}

// ---------- 绘制 ----------
func redraw(flacPath string, cellW, cellH int) {
	// A short delay to allow the terminal to settle after a resize event,
	// which can help ensure we get the correct new dimensions.
	time.Sleep(50 * time.Millisecond)

	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		fmt.Print("\x1b[2J\x1b[H")
		fmt.Println("无法获取终端尺寸")
		return
	}

		// 清屏(带滚动历史) + 光标归位

		fmt.Print("\x1b[2J\x1b[3J\x1b[H")

	

			// 图片将从顶部开始绘制，只在底部为提示文字留一行空间

	

			pixelW := w * cellW

	

			pixelH := (h - 1) * cellH

	

			if pixelH < 0 {

	

				pixelH = 0

	

			}

	

		

	

			// 为终端的边框或内边距增加一个安全边距 (99%宽度)

	

			safePixelW := int(float64(pixelW) * 0.99)

	

		

	

			// 提取并绘制封面

	

			if err := drawCover(flacPath, safePixelW, pixelH); err != nil {

	

				fmt.Printf("绘制封面失败: %v\n", err)

	

			}

	// 底部提示
	fmt.Printf("\x1b[%d;1H", h)
	fmt.Print("Press any key to quit.")
}

// ---------- 封面 → Sixel ----------
func drawCover(path string, termW, termH int) error {
	// 1. 读 tag
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("打开文件: %w", err)
	}
	defer f.Close()

	m, err := tag.ReadFrom(f)
	if err != nil {
		return fmt.Errorf("读取元数据: %w", err)
	}
	pic := m.Picture()
	if pic == nil {
		return fmt.Errorf("未找到内嵌封面")
	}

	// 2. 解码成 image.Image
	img, _, err := image.Decode(bytes.NewReader(pic.Data))
	if err != nil {
		return fmt.Errorf("解码封面: %w", err)
	}

	// 3. 等比缩放以适应终端可视区域
	img = resize.Thumbnail(uint(termW), uint(termH), img, resize.Lanczos3)

	// 4. 输出 sixel（光标当前就在第三行）
	return sixel.NewEncoder(os.Stdout).Encode(img)
}
