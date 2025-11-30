package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StorageData holds the data stored in the storage.json file.
type StorageData struct {
	LibraryPath string   `json:"library_path"` // 保存的完整音乐库路径
	Playlist    []string `json:"playlist"`     // 保存的播放列表（相对路径）
	PlayHistory []string `json:"play_history"` // 保存的播放历史记录（相对路径）
}

// getStoragePath returns the absolute path to the storage file.
func getStoragePath() (string, error) {
	storagePath := GlobalConfig.App.Storage
	if strings.HasPrefix(storagePath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("could not get user home directory: %v", err)
		}
		storagePath = filepath.Join(home, storagePath[2:])
	}
	return storagePath, nil
}

// loadStorageData loads data from the storage.json file.
func loadStorageData() (*StorageData, error) {
	storagePath, err := getStoragePath()
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(storagePath); os.IsNotExist(err) {
		// File doesn't exist, return default data
		return &StorageData{}, nil
	}

	data, err := os.ReadFile(storagePath)
	if err != nil {
		return nil, fmt.Errorf("could not read storage file: %v", err)
	}

	var storageData StorageData
	if err := json.Unmarshal(data, &storageData); err != nil {
		// If the file is empty or corrupted, return a default struct
		if len(data) == 0 {
			return &StorageData{}, nil
		}
		return nil, fmt.Errorf("could not decode storage file: %v", err)
	}

	return &storageData, nil
}

// saveStorageData saves data to the storage.json file.
func saveStorageData(data *StorageData) error {
	storagePath, err := getStoragePath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(storagePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("could not create storage directory: %v", err)
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode storage data: %v", err)
	}

	if err := os.WriteFile(storagePath, jsonData, 0644); err != nil {
		return fmt.Errorf("could not write storage file: %v", err)
	}

	return nil
}

// SaveLibraryPath saves the library path to the storage.json file
func SaveLibraryPath(path string) error {
	if !GlobalConfig.App.RememberLibraryPath {
		return nil
	}
	storageData, err := loadStorageData()
	if err != nil {
		return fmt.Errorf("could not load storage data: %v", err)
	}

	storageData.LibraryPath = path

	if err := saveStorageData(storageData); err != nil {
		return fmt.Errorf("could not save storage data: %v", err)
	}
	return nil
}

// SavePlaylist saves the current playlist to the storage.json file.
// It converts absolute paths to paths relative to the library root.
func SavePlaylist(playlist []string, libraryPath string) error {
	if !GlobalConfig.App.PlaylistHistory {
		return nil
	}

	storageData, err := loadStorageData()
	if err != nil {
		return fmt.Errorf("could not load storage data for playlist: %v", err)
	}

	relativePlaylist := make([]string, len(playlist))
	for i, songPath := range playlist {
		relPath, err := filepath.Rel(libraryPath, songPath)
		if err != nil {
			// If we can't make it relative, store the absolute path as a fallback
			relativePlaylist[i] = songPath
		} else {
			relativePlaylist[i] = relPath
		}
	}

	storageData.Playlist = relativePlaylist

	if err := saveStorageData(storageData); err != nil {
		return fmt.Errorf("could not save playlist data: %v", err)
	}
	return nil
}

// LoadPlaylist loads the playlist from the storage.json file.
// It converts relative paths back to absolute paths based on the library root.
func LoadPlaylist(libraryPath string) ([]string, error) {
	if !GlobalConfig.App.PlaylistHistory {
		return []string{}, nil
	}

	storageData, err := loadStorageData()
	if err != nil {
		return nil, fmt.Errorf("could not load storage data for playlist: %v", err)
	}

	if storageData.Playlist == nil {
		return []string{}, nil
	}

	absolutePlaylist := make([]string, len(storageData.Playlist))
	for i, relPath := range storageData.Playlist {
		// Check if the path is already absolute (fallback case)
		if filepath.IsAbs(relPath) {
			absolutePlaylist[i] = relPath
		} else {
			absolutePlaylist[i] = filepath.Join(libraryPath, relPath)
		}
	}

	return absolutePlaylist, nil
}

// SavePlayHistory saves the current play history to the storage.json file.
// It converts absolute paths to paths relative to the library root.
func SavePlayHistory(playHistory []string, libraryPath string) error {
	if !GlobalConfig.App.PlaybackHistoryPersistence {
		return nil
	}

	storageData, err := loadStorageData()
	if err != nil {
		return fmt.Errorf("could not load storage data for play history: %v", err)
	}

	relativePlayHistory := make([]string, len(playHistory))
	for i, songPath := range playHistory {
		relPath, err := filepath.Rel(libraryPath, songPath)
		if err != nil {
			// If we can't make it relative, store the absolute path as a fallback
			relativePlayHistory[i] = songPath
		} else {
			relativePlayHistory[i] = relPath
		}
	}

	storageData.PlayHistory = relativePlayHistory

	if err := saveStorageData(storageData); err != nil {
		return fmt.Errorf("could not save play history data: %v", err)
	}
	return nil
}

// LoadPlayHistory loads the play history from the storage.json file.
// It converts relative paths back to absolute paths based on the library root.
func LoadPlayHistory(libraryPath string) ([]string, error) {
	if !GlobalConfig.App.PlaybackHistoryPersistence {
		return []string{}, nil
	}

	storageData, err := loadStorageData()
	if err != nil {
		return nil, fmt.Errorf("could not load storage data for play history: %v", err)
	}

	if storageData.PlayHistory == nil {
		return []string{}, nil
	}

	absolutePlayHistory := make([]string, len(storageData.PlayHistory))
	for i, relPath := range storageData.PlayHistory {
		// Check if the path is already absolute (fallback case)
		if filepath.IsAbs(relPath) {
			absolutePlayHistory[i] = relPath
		} else {
			absolutePlayHistory[i] = filepath.Join(libraryPath, relPath)
		}
	}

	return absolutePlayHistory, nil
}
