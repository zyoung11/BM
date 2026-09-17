package main

import (
	"os"
	"os/exec"
	"strings"
)

var notifySendAvailable bool

func init() {
	if _, err := exec.LookPath("notify-send"); err == nil {
		notifySendAvailable = true
	}
}

// sendNotification sends a desktop notification when song-change
// notifications are enabled at runtime. The runtime toggle lives on App and
// is never persisted.
//
// sendNotification 在运行时通知开关开启时发送歌曲变更的桌面通知。运行时
// 开关保存在 App 上，不会持久化。
func (a *App) sendNotification(artist, title, coverPath string) {
	if !notifySendAvailable {
		return
	}

	if a == nil || !a.notificationsEnabled {
		return
	}

	go func() {
		safeArtist := sanitizeString(artist)
		safeTitle := sanitizeString(title)

		if safeArtist == "" {
			safeArtist = "Unknown Artist"
		}
		if safeTitle == "" {
			safeTitle = "Unknown Title"
		}

		icon := ""
		if isValidIconPath(coverPath) {
			icon = coverPath
		}

		cmd := exec.Command(
			"notify-send",
			"-a", "BM",
			"-i", icon,
			safeArtist,
			safeTitle,
		)

		cmd.Run()
	}()
}

func sanitizeString(s string) string {
	replacer := strings.NewReplacer(
		"&", "",
		";", "",
		"|", "",
		"*", "",
		"~", "",
		"<", "",
		">", "",
		"^", "",
		"(", "",
		")", "",
		"[", "",
		"]", "",
		"{", "",
		"}", "",
		"$", "",
		"\"", "",
	)
	return replacer.Replace(s)
}

func isValidIconPath(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}
