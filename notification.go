
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// sendNotification sends a desktop notification using the freedesktop.org notification standard.
// It sends the notification asynchronously to avoid blocking the main thread.
func sendNotification(artist, title, coverPath string) {
	go func() {
		// Use a debounce mechanism if needed, though this is already in a goroutine
		// If you want to prevent spamming notifications, you might add a time check here.

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

		// Construct the command for notify-send
		cmd := exec.Command(
			"notify-send",
			"-a", appName,
			"-i", icon,
			safeArtist, // Summary
			safeTitle,  // Body
		)

		// Run the command
		err := cmd.Run()
		if err != nil {
			// Log the error, but don't crash the application
			// You might want to use a more sophisticated logging mechanism
			fmt.Fprintf(os.Stderr, "Error sending notification: %v\n", err)
		}
	}()
}

// sanitizeString removes special characters that might cause issues in shell commands or D-Bus.
func sanitizeString(s string) string {
	// Replacer for problematic characters
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

// isValidIconPath checks if the provided path is a valid and existing file.
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
