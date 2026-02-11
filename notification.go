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

func sendNotification(artist, title, coverPath string) {
	if !notifySendAvailable {
		return
	}

	if GlobalConfig != nil && !GlobalConfig.App.EnableNotifications {
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
