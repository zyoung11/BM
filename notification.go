package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func sendNotification(artist, title, coverPath string) {
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

		appName := "kew"
		icon := ""
		if isValidIconPath(coverPath) {
			icon = coverPath
		}

		cmd := exec.Command(
			"notify-send",
			"-a", appName,
			"-i", icon,
			safeArtist,
			safeTitle,
		)

		err := cmd.Run()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error sending notification: %v\n", err)
		}
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
