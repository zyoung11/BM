package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// 清屏 + 光标回顶
func clear() { fmt.Print("\033[H\033[2J") }

// 字符行列
func ttySize() (rows, cols int) {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err == nil {
		rows = int(ws.Row)
		cols = int(ws.Col)
	}
	return
}

// 窗口像素（foot 专用）
func footPixels() (w, h int) {
	// 发查询
	fmt.Print("\033[14t")

	// 原始模式，只改必要位
	old, _ := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TCGETS)
	raw := *old
	raw.Lflag &^= unix.ECHO | unix.ICANON
	raw.Cc[unix.VMIN], raw.Cc[unix.VTIME] = 0, 1
	unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TCSETS, &raw)
	defer unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TCSETS, old)

	buf := make([]byte, 64)
	n, _ := os.Stdin.Read(buf)
	s := string(buf[:n])
	if strings.HasPrefix(s, "\033[4;") && strings.HasSuffix(s, "t") {
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(s, "\033[4;"), "t"), ";")
		if len(parts) == 2 {
			w, _ = strconv.Atoi(parts[0])
			h, _ = strconv.Atoi(parts[1])
		}
	}
	return
}

func main() {
	clear()
	for {
		rows, cols := ttySize()
		pxW, pxH := footPixels()
		// 覆盖显示
		fmt.Printf("\033[H") // 光标回顶即可
		fmt.Printf("终端：%d 列 × %d 行\n", cols, rows)
		fmt.Printf("窗口：%d px × %d px\n", pxW, pxH)
		fmt.Printf("单字符：%d px × %d px\n", pxW/cols, pxH/rows)
		fmt.Println("\n按 Ctrl-C 退出")
		time.Sleep(200 * time.Millisecond) // 5 次/秒，可调
	}
}
