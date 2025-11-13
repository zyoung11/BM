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
	"syscall"

	"github.com/dhowden/tag"
	"github.com/mattn/go-sixel"
	"github.com/nfnt/resize"
	"golang.org/x/term"
)

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

	// 1. 进入交替缓冲区，退出时还原
	fmt.Print("\x1b[?1049h")
	defer fmt.Print("\x1b[?1049l")

	// 2. 原始模式
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		log.Fatalf("failed to set raw mode: %v", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// 3. 主循环
	for {
		redraw(flacPath)

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
func redraw(flacPath string) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		fmt.Print("\x1b[2J\x1b[H")
		fmt.Println("无法获取终端尺寸")
		return
	}

	// 清屏 + 光标归位
	fmt.Print("\x1b[2J\x1b[H")

	// 调试信息
	fmt.Printf("Terminal: %d×%d   按任意键退出\n\n", w, h)

	// 提取并绘制封面
	if err := drawCover(flacPath, w, h); err != nil {
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

	// 3. 等比缩放（以终端宽度为基准，高度留两行文字）
	newW := uint(termW)
	newH := uint(float64(img.Bounds().Dy()) * float64(newW) / float64(img.Bounds().Dx()))
	if newH > uint(termH)-2 {
		newH = uint(termH) - 2
		newW = uint(float64(img.Bounds().Dx()) * float64(newH) / float64(img.Bounds().Dy()))
	}
	img = resize.Resize(newW, newH, img, resize.Lanczos3)

	// 4. 输出 sixel（光标当前就在第三行）
	return sixel.NewEncoder(os.Stdout).Encode(img)
}
