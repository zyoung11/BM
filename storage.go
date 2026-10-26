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
	LibraryPath         string   `json:"library_path"`
	Playlist            []string `json:"playlist"`
	PlayHistory         []string `json:"play_history"`
	CurrentSong         *string  `json:"current_song,omitempty"`
	Volume              *float64 `json:"volume,omitempty"`
	PlaybackRate        *float64 `json:"playback_rate,omitempty"`
	PlayMode            *int     `json:"play_mode,omitempty"`
	Sort                *int     `json:"sort,omitempty"`
	NameDisplayLibrary  *int     `json:"name_display_library,omitempty"`
	NameDisplayPlaylist *int     `json:"name_display_playlist,omitempty"`
	Page                *int     `json:"page,omitempty"`
	OverrideLayout      *int     `json:"override_layout,omitempty"`
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

	tmpPath := storagePath + ".tmp"
	if err := os.WriteFile(tmpPath, jsonData, 0644); err != nil {
		return fmt.Errorf("could not write storage temp file: %v\n\n无法写入存储临时文件: %v", err, err)
	}
	if err := os.Rename(tmpPath, storagePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("could not replace storage file: %v\n\n无法替换存储文件: %v", err, err)
	}

	return nil
}

// SaveLibraryPath saves the library path to the storage.json file.
//
// SaveLibraryPath 将音乐库路径保存到 storage.json 文件。
func SaveLibraryPath(path string) error {
	if GlobalConfig.App.SessionMemory < 1 {
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
	if GlobalConfig.App.SessionMemory < 2 {
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
	if GlobalConfig.App.SessionMemory < 2 {
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
	if GlobalConfig.App.SessionMemory < 3 {
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
	if GlobalConfig.App.SessionMemory < 3 {
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
	if GlobalConfig.App.SessionMemory < 3 {
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
	if GlobalConfig.App.SessionMemory < 3 {
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

// resolveSortMode maps a sort mode setting to a runtime sort mode. The
// value 3 (memory) and any other out-of-range value fall back to the default
// sort.
//
// resolveSortMode 将排序方式设置映射为运行时排序方式。取值 3（记忆）及
// 其他越界取值回退为默认排序。
func resolveSortMode(mode int) int {
	if mode < 0 || mode > 2 {
		return 0
	}
	return mode
}

// SaveSort saves the library sort mode to the storage.json file.
//
// SaveSort 将媒体库排序方式保存到 storage.json 文件。
func SaveSort(sort int) error {
	storageData, err := loadStorageData()
	if err != nil {
		return fmt.Errorf("could not load storage data for sort: %v\n\n无法加载排序方式的存储数据: %v", err, err)
	}
	sortMode := resolveSortMode(sort)
	storageData.Sort = &sortMode
	if err := saveStorageData(storageData); err != nil {
		return fmt.Errorf("could not save sort data: %v\n\n无法保存排序方式数据: %v", err, err)
	}
	return nil
}

// LoadSort returns the saved library sort mode, or the default sort when
// nothing has been saved yet.
//
// LoadSort 返回已保存的媒体库排序方式；尚未保存时返回默认排序。
func LoadSort() (int, error) {
	storageData, err := loadStorageData()
	if err != nil {
		return 0, fmt.Errorf("could not load storage data for sort: %v\n\n无法加载排序方式的存储数据: %v", err, err)
	}
	if storageData.Sort == nil {
		return 0, nil
	}
	return resolveSortMode(*storageData.Sort), nil
}

// SaveNameDisplay saves a song name display mode to the storage.json file.
// The library and the playlist keep separate settings.
//
// SaveNameDisplay 将歌曲名显示模式保存到 storage.json 文件。
// 媒体库与播放列表各自独立设置。
func SaveNameDisplay(library bool, mode int) error {
	storageData, err := loadStorageData()
	if err != nil {
		return fmt.Errorf("could not load storage data for name display: %v\n\n无法加载歌曲名显示模式的存储数据: %v", err, err)
	}
	value := resolveNameDisplayMode(mode)
	if library {
		storageData.NameDisplayLibrary = &value
	} else {
		storageData.NameDisplayPlaylist = &value
	}
	if err := saveStorageData(storageData); err != nil {
		return fmt.Errorf("could not save name display data: %v\n\n无法保存歌曲名显示模式数据: %v", err, err)
	}
	return nil
}

// LoadNameDisplay returns the saved song name display mode for a page, or the
// default display when nothing has been saved yet.
//
// LoadNameDisplay 返回页面已保存的歌曲名显示模式；尚未保存时返回默认显示。
func LoadNameDisplay(library bool) (int, error) {
	storageData, err := loadStorageData()
	if err != nil {
		return 0, fmt.Errorf("could not load storage data for name display: %v\n\n无法加载歌曲名显示模式的存储数据: %v", err, err)
	}
	if library {
		if storageData.NameDisplayLibrary != nil {
			return resolveNameDisplayMode(*storageData.NameDisplayLibrary), nil
		}
		return 0, nil
	}
	if storageData.NameDisplayPlaylist != nil {
		return resolveNameDisplayMode(*storageData.NameDisplayPlaylist), nil
	}
	return 0, nil
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
// resolvePlayMode maps a play mode setting to a runtime play mode. The
// value 3 (memory) and any other out-of-range value fall back to repeat one,
// so a missing saved mode never disables auto-advance.
//
// resolvePlayMode 将播放模式设置映射为运行时播放模式。取值 3（记忆）及
// 其他越界取值回退为单曲循环，保证缺少已保存模式时自动推进不会失效。
func resolvePlayMode(mode int) int {
	if mode < 0 || mode > 2 {
		return 0
	}
	return mode
}

func LoadPlayMode() (int, error) {
	storageData, err := loadStorageData()
	if err != nil {
		return resolvePlayMode(GlobalConfig.App.DefaultPlayMode), fmt.Errorf("could not load storage data for play mode: %v\n\n无法加载播放模式的存储数据: %v", err, err)
	}

	if storageData.PlayMode == nil {
		return resolvePlayMode(GlobalConfig.App.DefaultPlayMode), nil
	}

	// If saved mode is 3 (should not happen, but handle it), return default
	if *storageData.PlayMode == 3 {
		return resolvePlayMode(GlobalConfig.App.DefaultPlayMode), nil
	}

	return *storageData.PlayMode, nil
}

// SavePage saves the current page index to the storage.json file.
//
// SavePage 将当前页面索引保存到 storage.json 文件。
func SavePage(page int) error {
	storageData, err := loadStorageData()
	if err != nil {
		return fmt.Errorf("could not load storage data for page: %v\n\n无法加载页面的存储数据: %v", err, err)
	}

	storageData.Page = &page

	if err := saveStorageData(storageData); err != nil {
		return fmt.Errorf("could not save page data: %v\n\n无法保存页面数据: %v", err, err)
	}
	return nil
}

// LoadPage loads the page index from the storage.json file.
// If no page is saved, it returns 0 (player page).
//
// LoadPage 从 storage.json 文件加载页面索引。
// 如果没有保存页面，则返回 0（播放器页面）。
func LoadPage() (int, error) {
	storageData, err := loadStorageData()
	if err != nil {
		return 0, fmt.Errorf("could not load storage data for page: %v\n\n无法加载页面的存储数据: %v", err, err)
	}

	if storageData.Page == nil {
		return 0, nil
	}

	return *storageData.Page, nil
}

// SaveOverrideLayout saves the player page layout override to the storage.json file.
//
// SaveOverrideLayout 将播放页布局覆盖保存到 storage.json 文件。
func SaveOverrideLayout(overrideLayout int) error {
	storageData, err := loadStorageData()
	if err != nil {
		return fmt.Errorf("could not load storage data for layout: %v\n\n无法加载布局的存储数据: %v", err, err)
	}

	storageData.OverrideLayout = &overrideLayout

	if err := saveStorageData(storageData); err != nil {
		return fmt.Errorf("could not save layout data: %v\n\n无法保存布局数据: %v", err, err)
	}
	return nil
}

// LoadOverrideLayout loads the player page layout override from the
// storage.json file. It returns -1 when no layout is saved.
//
// LoadOverrideLayout 从 storage.json 文件加载播放页布局覆盖。
// 未保存过布局时返回 -1。
func LoadOverrideLayout() (int, error) {
	storageData, err := loadStorageData()
	if err != nil {
		return -1, fmt.Errorf("could not load storage data for layout: %v\n\n无法加载布局的存储数据: %v", err, err)
	}

	if storageData.OverrideLayout == nil {
		return -1, nil
	}
	return *storageData.OverrideLayout, nil
}
