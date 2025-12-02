package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StorageData holds the data stored in the storage.json file.
//
// StorageData 保存存储在 storage.json 文件中的数据。
type StorageData struct {
	LibraryPath  string   `json:"library_path"`
	Playlist     []string `json:"playlist"`
	PlayHistory  []string `json:"play_history"`
	CurrentSong  *string  `json:"current_song,omitempty"`
	Volume       *float64 `json:"volume,omitempty"`
	PlaybackRate *float64 `json:"playback_rate,omitempty"`
	PlayMode     *int     `json:"play_mode,omitempty"`
}

// getStoragePath returns the absolute path to the storage file.
//
// getStoragePath 返回存储文件的绝对路径。
func getStoragePath() (string, error) {
	storagePath := GlobalConfig.App.Storage
	if strings.HasPrefix(storagePath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("could not get user home directory: %v\n\n无法获取用户主目录: %v", err, err)
		}
		storagePath = filepath.Join(home, storagePath[2:])
	}
	return storagePath, nil
}

// loadStorageData loads data from the storage.json file.
//
// loadStorageData 从 storage.json 文件加载数据。
func loadStorageData() (*StorageData, error) {
	storagePath, err := getStoragePath()
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(storagePath); os.IsNotExist(err) {
		return &StorageData{}, nil
	}

	data, err := os.ReadFile(storagePath)
	if err != nil {
		return nil, fmt.Errorf("could not read storage file: %v\n\n无法读取存储文件: %v", err, err)
	}

	var storageData StorageData
	if err := json.Unmarshal(data, &storageData); err != nil {
		if len(data) == 0 {
			return &StorageData{}, nil
		}
		return nil, fmt.Errorf("could not decode storage file: %v\n\n无法解析存储文件: %v", err, err)
	}

	return &storageData, nil
}

// saveStorageData saves data to the storage.json file.
//
// saveStorageData 将数据保存到 storage.json 文件。
func saveStorageData(data *StorageData) error {
	storagePath, err := getStoragePath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(storagePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("could not create storage directory: %v\n\n无法创建存储目录: %v", err, err)
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode storage data: %v\n\n无法编码存储数据: %v", err, err)
	}

	if err := os.WriteFile(storagePath, jsonData, 0644); err != nil {
		return fmt.Errorf("could not write storage file: %v\n\n无法写入存储文件: %v", err, err)
	}

	return nil
}

// SaveLibraryPath saves the library path to the storage.json file.
//
// SaveLibraryPath 将音乐库路径保存到 storage.json 文件。
func SaveLibraryPath(path string) error {
	if !GlobalConfig.App.RememberLibraryPath {
		return nil
	}
	storageData, err := loadStorageData()
	if err != nil {
		return fmt.Errorf("could not load storage data: %v\n\n无法加载存储数据: %v", err, err)
	}

	storageData.LibraryPath = path

	if err := saveStorageData(storageData); err != nil {
		return fmt.Errorf("could not save storage data: %v\n\n无法保存存储数据: %v", err, err)
	}
	return nil
}

// SavePlaylist saves the current playlist to the storage.json file.
// It converts absolute paths to paths relative to the library root.
//
// SavePlaylist 将当前播放列表保存到 storage.json 文件。
// 它将绝对路径转换为相对于音乐库根目录的相对路径。
func SavePlaylist(playlist []string, libraryPath string) error {
	if !GlobalConfig.App.PlaylistHistory {
		return nil
	}

	storageData, err := loadStorageData()
	if err != nil {
		return fmt.Errorf("could not load storage data for playlist: %v\n\n无法加载播放列表的存储数据: %v", err, err)
	}

	relativePlaylist := make([]string, len(playlist))
	for i, songPath := range playlist {
		relPath, err := filepath.Rel(libraryPath, songPath)
		if err != nil {
			relativePlaylist[i] = songPath
		} else {
			relativePlaylist[i] = relPath
		}
	}

	storageData.Playlist = relativePlaylist

	if err := saveStorageData(storageData); err != nil {
		return fmt.Errorf("could not save playlist data: %v\n\n无法保存播放列表数据: %v", err, err)
	}
	return nil
}

// LoadPlaylist loads the playlist from the storage.json file.
// It converts relative paths back to absolute paths based on the library root.
//
// LoadPlaylist 从 storage.json 文件加载播放列表。
// 它将相对路径转换回基于音乐库根目录的绝对路径。
func LoadPlaylist(libraryPath string) ([]string, error) {
	if !GlobalConfig.App.PlaylistHistory {
		return []string{}, nil
	}

	storageData, err := loadStorageData()
	if err != nil {
		return nil, fmt.Errorf("could not load storage data for playlist: %v\n\n无法加载播放列表的存储数据: %v", err, err)
	}

	if storageData.Playlist == nil {
		return []string{}, nil
	}

	absolutePlaylist := make([]string, len(storageData.Playlist))
	for i, relPath := range storageData.Playlist {
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
//
// SavePlayHistory 将当前播放历史记录保存到 storage.json 文件。
// 它将绝对路径转换为相对于音乐库根目录的相对路径。
func SavePlayHistory(playHistory []string, libraryPath string) error {
	if !GlobalConfig.App.AutostartLastPlayed {
		return nil
	}

	storageData, err := loadStorageData()
	if err != nil {
		return fmt.Errorf("could not load storage data for play history: %v\n\n无法加载播放历史的存储数据: %v", err, err)
	}

	relativePlayHistory := make([]string, len(playHistory))
	for i, songPath := range playHistory {
		relPath, err := filepath.Rel(libraryPath, songPath)
		if err != nil {
			relativePlayHistory[i] = songPath
		} else {
			relativePlayHistory[i] = relPath
		}
	}

	storageData.PlayHistory = relativePlayHistory

	if err := saveStorageData(storageData); err != nil {
		return fmt.Errorf("could not save play history data: %v\n\n无法保存播放历史数据: %v", err, err)
	}
	return nil
}

// SaveCurrentSong saves the current playing song to the storage.json file.
// It converts absolute paths to paths relative to the library root.
//
// SaveCurrentSong 将当前播放的歌曲保存到 storage.json 文件。
// 它将绝对路径转换为相对于音乐库根目录的相对路径。
func SaveCurrentSong(songPath string, libraryPath string) error {
	if !GlobalConfig.App.AutostartLastPlayed {
		return nil
	}

	storageData, err := loadStorageData()
	if err != nil {
		return fmt.Errorf("could not load storage data for current song: %v\n\n无法加载当前歌曲的存储数据: %v", err, err)
	}

	var relativePath string
	if songPath != "" {
		relPath, err := filepath.Rel(libraryPath, songPath)
		if err != nil {
			relativePath = songPath
		} else {
			relativePath = relPath
		}
		storageData.CurrentSong = &relativePath
	} else {
		storageData.CurrentSong = nil
	}

	if err := saveStorageData(storageData); err != nil {
		return fmt.Errorf("could not save current song data: %v\n\n无法保存当前歌曲数据: %v", err, err)
	}
	return nil
}

// LoadCurrentSong loads the current playing song from the storage.json file.
// It converts relative paths back to absolute paths based on the library root.
//
// LoadCurrentSong 从 storage.json 文件加载当前播放的歌曲。
// 它将相对路径转换回绝对路径。
func LoadCurrentSong(libraryPath string) (string, error) {
	if !GlobalConfig.App.AutostartLastPlayed {
		return "", nil
	}

	storageData, err := loadStorageData()
	if err != nil {
		return "", fmt.Errorf("could not load storage data for current song: %v\n\n无法加载当前歌曲的存储数据: %v", err, err)
	}

	if storageData.CurrentSong == nil {
		return "", nil
	}

	relPath := *storageData.CurrentSong
	if filepath.IsAbs(relPath) {
		return relPath, nil
	} else {
		return filepath.Join(libraryPath, relPath), nil
	}
}

// LoadPlayHistory loads the play history from the storage.json file.
// It converts relative paths back to absolute paths based on the library root.
//
// LoadPlayHistory 从 storage.json 文件加载播放历史记录。
// 它将相对路径转换回基于音乐库根目录的绝对路径。
func LoadPlayHistory(libraryPath string) ([]string, error) {
	if !GlobalConfig.App.AutostartLastPlayed {
		return []string{}, nil
	}

	storageData, err := loadStorageData()
	if err != nil {
		return nil, fmt.Errorf("could not load storage data for play history: %v\n\n无法加载播放历史的存储数据: %v", err, err)
	}

	if storageData.PlayHistory == nil {
		return []string{}, nil
	}

	absolutePlayHistory := make([]string, len(storageData.PlayHistory))
	for i, relPath := range storageData.PlayHistory {
		if filepath.IsAbs(relPath) {
			absolutePlayHistory[i] = relPath
		} else {
			absolutePlayHistory[i] = filepath.Join(libraryPath, relPath)
		}
	}

	return absolutePlayHistory, nil
}

// SavePlayMode saves the current play mode to the storage.json file.
//
// SavePlayMode 将当前播放模式保存到 storage.json 文件。
func SavePlayMode(playMode int) error {
	// Don't save mode 3 (memory) - it's not a real play mode
	if playMode == 3 {
		return nil
	}

	storageData, err := loadStorageData()
	if err != nil {
		return fmt.Errorf("could not load storage data for play mode: %v\n\n无法加载播放模式的存储数据: %v", err, err)
	}

	storageData.PlayMode = &playMode

	if err := saveStorageData(storageData); err != nil {
		return fmt.Errorf("could not save play mode data: %v\n\n无法保存播放模式数据: %v", err, err)
	}
	return nil
}

// LoadPlayMode loads the play mode from the storage.json file.
// If no play mode is saved, it returns the default play mode from config.
//
// LoadPlayMode 从 storage.json 文件加载播放模式。
// 如果没有保存播放模式，则返回配置中的默认播放模式。
func LoadPlayMode() (int, error) {
	storageData, err := loadStorageData()
	if err != nil {
		return GlobalConfig.App.DefaultPlayMode, fmt.Errorf("could not load storage data for play mode: %v\n\n无法加载播放模式的存储数据: %v", err, err)
	}

	if storageData.PlayMode == nil {
		return GlobalConfig.App.DefaultPlayMode, nil
	}

	// If saved mode is 3 (should not happen, but handle it), return default
	if *storageData.PlayMode == 3 {
		return GlobalConfig.App.DefaultPlayMode, nil
	}

	return *storageData.PlayMode, nil
}
