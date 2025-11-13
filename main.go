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

	// 3. 查询终端字符尺寸
	cellW, cellH, err := getCellSize()
	if err != nil {
		log.Fatalf("终端不支持尺寸查询: %v", err)
	}

	// 4. 设置事件通道
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	keyCh := make(chan struct{}, 1)
	go func() {
		// 持续读取键盘输入，任意输入都会发送信号
		buf := make([]byte, 1024)
		for {
			_, err := os.Stdin.Read(buf)
			if err != nil {
				return // 发生错误（如程序退出），goroutine结束
			}
			keyCh <- struct{}{}
		}
	}()

	// 5. 主循环
	// 初始绘制
	redraw(flacPath, cellW, cellH)

	for {
		select {
		case <-keyCh:
			// 收到键盘输入，退出程序
			return
		case <-sigCh:
			// 收到窗口尺寸变化信号，重新绘制
			redraw(flacPath, cellW, cellH)
		}
	}
}

// ---------- 绘制 ----------
func redraw(flacPath string, cellW, cellH int) {
	// A short delay to allow the terminal to settle after a resize event.
	time.Sleep(50 * time.Millisecond)

	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		// 此时无法获取尺寸，仅清屏并显示错误
		fmt.Print("\x1b[2J\x1b[H")
		fmt.Println("无法获取终端尺寸")
		return
	}

	// 清屏(带滚动历史) + 光标归位
	fmt.Print("\x1b[2J\x1b[3J\x1b[H")

	// --- 加载并解码封面 ---
	f, err := os.Open(flacPath)
	if err != nil {
		fmt.Printf("打开文件失败: %v", err)
		return
	}
	defer f.Close()

	m, err := tag.ReadFrom(f)
	if err != nil {
		fmt.Printf("读取元数据失败: %v", err)
		return
	}
	pic := m.Picture()
	if pic == nil {
		fmt.Print("未找到内嵌封面")
		return
	}

	img, _, err := image.Decode(bytes.NewReader(pic.Data))
	if err != nil {
		fmt.Printf("解码封面失败: %v", err)
		return
	}

	// --- 计算尺寸和位置 ---
	// 为终端的边框或内边距增加一个安全边距 (99%宽度)
	pixelW := w * cellW
	safePixelW := int(float64(pixelW) * 0.99)
	// 为底部的提示文字留一行空间
	pixelH := (h - 1) * cellH
	if pixelH < 0 {
		pixelH = 0
	}

	// 缩放图片
	scaledImg := resize.Thumbnail(uint(safePixelW), uint(pixelH), img, resize.Lanczos3)

	// 计算居中所需的起始列
	finalImgW := scaledImg.Bounds().Dx()
	startCol := (w - (finalImgW / cellW)) / 2
	if startCol < 1 {
		startCol = 1
	}

	// --- 绘制 ---
	// 移动光标到起始位置 (行1, 列startCol)
	fmt.Printf("\x1b[%dG", startCol)

	// 编码并打印Sixel数据
	if err := sixel.NewEncoder(os.Stdout).Encode(scaledImg); err != nil {
		// 如果Sixel编码失败，在屏幕上打印错误
		// 需要先清屏，因为Sixel可能已部分输出
		fmt.Print("\x1b[2J\x1b[H")
		fmt.Printf("Sixel编码失败: %v", err)
	}

	// 在底部绘制提示
	fmt.Printf("\x1b[%d;1H", h)
	fmt.Print("Press any key to quit.")
}