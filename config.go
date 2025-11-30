package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed default_config.toml
var defaultConfigContent string

// Key is a custom type to handle a single key or a list of keys in the TOML file.
//
// Key 是一个自定义类型，用于处理TOML文件中的单个或多个按键配置。
type Key []string

// UnmarshalTOML allows the Key type to be parsed from either a string or a list of strings.
//
// UnmarshalTOML 允许 Key 类型从字符串或字符串列表解析。
func (k *Key) UnmarshalTOML(data []byte) error {
	var single string
	if err := toml.Unmarshal(data, &single); err == nil {
		*k = []string{single}
		return nil
	}

	var multi []string
	if err := toml.Unmarshal(data, &multi); err == nil {
		*k = multi
		return nil
	}

	return fmt.Errorf("key must be a string or a list of strings\n\n键必须是字符串或字符串列表")
}

// Config holds the application's configuration, loaded from a TOML file.
//
// Config 保存从TOML文件加载的应用程序配置。
type Config struct {
	Keymap Keymap    `toml:"keymap"`
	App    AppConfig `toml:"app"`
}

// AppConfig holds application-level configuration settings.
//
// AppConfig 保存应用程序级别的配置设置。
type AppConfig struct {
	MaxHistorySize             int    `toml:"max_history_size"`
	SwitchDebounceMs           int    `toml:"switch_debounce_ms"`
	DefaultPage                int    `toml:"default_page"`
	DefaultPlayMode            int    `toml:"default_play_mode"`
	RememberLibraryPath        bool   `toml:"remember_library_path"`
	PlaylistHistory            bool   `toml:"playlist_history"`
	PlaybackHistoryPersistence bool   `toml:"playback_history_persistence"`
	RememberVolume             bool   `toml:"remember_volume"`
	RememberPlaybackRate       bool   `toml:"remember_playback_rate"`
	LibraryPath                string `toml:"library_path"`
	TargetSampleRate           int    `toml:"target_sample_rate"`
	Storage                    string `toml:"storage"`
}

// Keymap defines all the keybindings for the application, organized by page.
//
// Keymap 定义了应用程序的所有按键绑定，按页面组织。
type Keymap struct {
	Global   GlobalKeymap   `toml:"global"`
	Player   PlayerKeymap   `toml:"player"`
	Library  LibraryKeymap  `toml:"library"`
	Playlist PlaylistKeymap `toml:"playlist"`
}

// GlobalKeymap holds keybindings that work across all pages.
//
// GlobalKeymap 保存适用于所有页面的全局按键绑定。
type GlobalKeymap struct {
	Quit             Key `toml:"Quit"`
	CyclePages       Key `toml:"CyclePages"`
	SwitchToPlayer   Key `toml:"SwitchToPlayer"`
	SwitchToPlayList Key `toml:"SwitchToPlayList"`
	SwitchToLibrary  Key `toml:"SwitchToLibrary"`
}

// PlayerKeymap holds keybindings specific to the Player page.
//
// PlayerKeymap 保存播放器页面特有的按键绑定。
type PlayerKeymap struct {
	TogglePause     Key `toml:"TogglePause"`
	SeekForward     Key `toml:"SeekForward"`
	SeekBackward    Key `toml:"SeekBackward"`
	VolumeUp        Key `toml:"VolumeUp"`
	VolumeDown      Key `toml:"VolumeDown"`
	RateUp          Key `toml:"RateUp"`
	RateDown        Key `toml:"RateDown"`
	NextSong        Key `toml:"NextSong"`
	PrevSong        Key `toml:"PrevSong"`
	TogglePlayMode  Key `toml:"TogglePlayMode"`
	ToggleTextColor Key `toml:"ToggleTextColor"`
	Reset           Key `toml:"Reset"`
}

// LibraryKeymap holds keybindings for the Library page.
//
// LibraryKeymap 保存媒体库页面的按键绑定。
type LibraryKeymap struct {
	// Normal mode keybindings
	// 普通模式按键绑定
	NavUp           Key `toml:"NavUp"`
	NavDown         Key `toml:"NavDown"`
	NavEnterDir     Key `toml:"NavEnterDir"`
	NavExitDir      Key `toml:"NavExitDir"`
	ToggleSelect    Key `toml:"ToggleSelect"`
	ToggleSelectAll Key `toml:"ToggleSelectAll"`
	Search          Key `toml:"Search"`

	// Search mode keybindings
	// 搜索模式按键绑定
	SearchMode SearchModeKeymap `toml:"SearchMode"`
}

// SearchModeKeymap holds keybindings specific to search mode.
//
// SearchModeKeymap 保存搜索模式特有的按键绑定。
type SearchModeKeymap struct {
	ConfirmSearch   Key `toml:"ConfirmSearch"`
	EscapeSearch    Key `toml:"EscapeSearch"`
	SearchBackspace Key `toml:"SearchBackspace"`
}

// PlaylistKeymap holds keybindings for the Playlist page.
//
// PlaylistKeymap 保存播放列表页面的按键绑定。
type PlaylistKeymap struct {
	// Normal mode keybindings
	// 普通模式按键绑定
	NavUp      Key `toml:"NavUp"`
	NavDown    Key `toml:"NavDown"`
	RemoveSong Key `toml:"RemoveSong"`
	PlaySong   Key `toml:"PlaySong"`
	Search     Key `toml:"Search"`

	// Search mode keybindings
	// 搜索模式按键绑定
	SearchMode SearchModeKeymap `toml:"SearchMode"`
}

// GlobalConfig is the global configuration instance.
//
// GlobalConfig 是全局配置实例。
var GlobalConfig *Config

// specialKeyMap maps string representations of special keys to their rune values.
//
// specialKeyMap 将特殊按键的字符串表示映射到它们的符文值。
var specialKeyMap = map[string]rune{
	"enter":      KeyEnter,
	"space":      ' ',
	"backspace":  KeyBackspace,
	"esc":        '\x1b',
	"tab":        '\t',
	"arrowup":    KeyArrowUp,
	"arrowdown":  KeyArrowDown,
	"arrowleft":  KeyArrowLeft,
	"arrowright": KeyArrowRight,
	"up":         KeyArrowUp,
	"down":       KeyArrowDown,
	"left":       KeyArrowLeft,
	"right":      KeyArrowRight,
}

// stringToRune converts a key string from the config to its corresponding rune.
//
// stringToRune 将配置文件中的按键字符串转换为对应的符文。
func stringToRune(s string) (rune, error) {
	s = strings.ToLower(s)
	if r, ok := specialKeyMap[s]; ok {
		return r, nil
	}
	if len(s) == 1 {
		return rune(s[0]), nil
	}
	return 0, fmt.Errorf("invalid key: '%s'\n\n无效的键: '%s'", s, s)
}

// LoadConfig loads the configuration from ~/.config/BM/config.toml.
// If the file doesn't exist, it creates it with default values.
//
// LoadConfig 从 ~/.config/BM/config.toml 加载配置。
// 如果文件不存在，它会使用默认值创建它。
func LoadConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not get user home directory: %v\n\n无法获取用户主目录: %v", err, err)
	}
	configPath := filepath.Join(home, ".config", "BM")
	if err := os.MkdirAll(configPath, 0755); err != nil {
		return fmt.Errorf("could not create config directory: %v\n\n无法创建配置目录: %v", err, err)
	}

	configFile := filepath.Join(configPath, "config.toml")

	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		if err := os.WriteFile(configFile, []byte(defaultConfigContent), 0644); err != nil {
			return fmt.Errorf("could not write default config file: %v\n\n无法写入默认配置文件: %v", err, err)
		}
		var config Config
		if _, err := toml.Decode(defaultConfigContent, &config); err != nil {
			return fmt.Errorf("could not decode default config: %v\n\n无法解析默认配置: %v", err, err)
		}
		GlobalConfig = &config
	} else {
		var config Config
		if _, err := toml.DecodeFile(configFile, &config); err != nil {
			return fmt.Errorf("could not decode config file: %v\n\n无法解析配置文件: %v", err, err)
		}
		GlobalConfig = &config
	}

	if GlobalConfig.App.TargetSampleRate <= 0 {
		GlobalConfig.App.TargetSampleRate = 44100
	}
	if GlobalConfig.App.SwitchDebounceMs <= 0 {
		GlobalConfig.App.SwitchDebounceMs = 200
	}

	return validateKeymap(GlobalConfig.Keymap)
}

// validateKeymap checks for duplicate or invalid keybindings.
//
// validateKeymap 检查重复或无效的按键绑定。
func validateKeymap(keymap Keymap) error {
	pages := []interface{}{keymap.Global, keymap.Player, keymap.Library, keymap.Playlist}
	pageNames := []string{"Global", "Player", "Library", "Playlist"}

	for i, page := range pages {
		normalModeKeys := make(map[rune]string)
		searchModeKeys := make(map[rune]string)

		v := reflect.ValueOf(page)
		t := v.Type()

		for j := 0; j < v.NumField(); j++ {
			field := v.Field(j)
			fieldName := t.Field(j).Name

			if keys, ok := field.Interface().(Key); ok {
				for _, keyStr := range keys {
					r, err := stringToRune(keyStr)
					if err != nil {
						return fmt.Errorf("invalid key '%s' in [%s] %s\n\n在 [%s] %s 中的无效键 '%s'", keyStr, pageNames[i], fieldName, pageNames[i], fieldName, keyStr)
					}

					if existing, duplicated := normalModeKeys[r]; duplicated {
						return fmt.Errorf("key conflict in [%s]: key '%s' is assigned to both '%s' and '%s'\n\n在 [%s] 中存在按键冲突: '%s' 同时分配给了 '%s' 和 '%s'", pageNames[i], keyStr, existing, fieldName, pageNames[i], keyStr, existing, fieldName)
					}
					normalModeKeys[r] = fieldName
				}
			} else if searchMode, ok := field.Interface().(SearchModeKeymap); ok {
				searchModeV := reflect.ValueOf(searchMode)
				searchModeT := searchModeV.Type()

				for k := 0; k < searchModeV.NumField(); k++ {
					searchField := searchModeV.Field(k)
					searchFieldName := searchModeT.Field(k).Name

					if searchKeys, ok := searchField.Interface().(Key); ok {
						for _, keyStr := range searchKeys {
							r, err := stringToRune(keyStr)
							if err != nil {
								return fmt.Errorf("invalid key '%s' in [%s] SearchMode.%s\n\n在 [%s] SearchMode.%s 中的无效键 '%s'", keyStr, pageNames[i], searchFieldName, pageNames[i], searchFieldName, keyStr)
							}

							if existing, duplicated := searchModeKeys[r]; duplicated {
								return fmt.Errorf("key conflict in [%s] SearchMode: key '%s' is assigned to both '%s' and '%s'\n\n在 [%s] SearchMode 中存在按键冲突: '%s' 同时分配给了 '%s' 和 '%s'", pageNames[i], keyStr, existing, searchFieldName, pageNames[i], keyStr, existing, searchFieldName)
							}
							searchModeKeys[r] = searchFieldName
						}
					}
				}
			}
		}
	}
	return nil
}

// IsKey checks if the given rune matches any of the keys for the given action.
//
// IsKey 检查给定的符文是否与给定操作的任何一个按键匹配。
func IsKey(key rune, actionKeys Key) bool {
	for _, keyStr := range actionKeys {
		if r, err := stringToRune(keyStr); err == nil && r == key {
			return true
		}
	}
	return false
}
