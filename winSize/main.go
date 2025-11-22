package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// 字符行列
func ttySize() (rows, cols int) {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err == nil {
		rows = int(ws.Row)
		cols = int(ws.Col)
	}
	return
}

// 窗口像素
func winPixel() (w, h int) {
	fmt.Print("\033[14t")

	// 先把 stdin 设成原始模式
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
	rows, cols := ttySize()
	pxW, pxH := winPixel()
	if cols == 0 || rows == 0 || pxW == 0 || pxH == 0 {
		fmt.Println("无法获取尺寸")
		return
	}
	fmt.Printf("窗口 %d×%d px\n", pxW, pxH)
}
