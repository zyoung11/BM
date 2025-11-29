package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"
)

// Key is a custom type to handle single keys or a list of keys in the TOML file.
type Key []string

// UnmarshalTOML allows the Key type to be parsed from either a string or a list of strings.
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

	return fmt.Errorf("key must be a string or a list of strings")
}

// Config holds the application's configuration, loaded from a TOML file.
type Config struct {
	Keymap Keymap    `toml:"keymap"`
	App    AppConfig `toml:"app"`
}

// AppConfig holds application-level configuration settings.
type AppConfig struct {
	MaxHistorySize   int `toml:"max_history_size"`   // 最大历史记录数量
	SwitchDebounceMs int `toml:"switch_debounce_ms"` // 切歌防抖时间（毫秒）
	DefaultPage      int `toml:"default_page"`       // 默认启动页面
	DefaultPlayMode  int `toml:"default_play_mode"`  // 默认播放模式
}

// Keymap defines all the keybindings for the application, organized by page.
type Keymap struct {
	Global   GlobalKeymap   `toml:"global"`
	Player   PlayerKeymap   `toml:"player"`
	Library  LibraryKeymap  `toml:"library"`
	Playlist PlaylistKeymap `toml:"playlist"`
}

// GlobalKeymap holds keybindings that work across all pages.
type GlobalKeymap struct {
	Quit             Key `toml:"Quit"`
	CyclePages       Key `toml:"CyclePages"`
	SwitchToPlayer   Key `toml:"SwitchToPlayer"`
	SwitchToPlayList Key `toml:"SwitchToPlayList"`
	SwitchToLibrary  Key `toml:"SwitchToLibrary"`
}

// PlayerKeymap holds keybindings specific to the Player page.
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
type LibraryKeymap struct {
	// Normal mode keybindings
	NavUp           Key `toml:"NavUp"`
	NavDown         Key `toml:"NavDown"`
	NavEnterDir     Key `toml:"NavEnterDir"`
	NavExitDir      Key `toml:"NavExitDir"`
	ToggleSelect    Key `toml:"ToggleSelect"`
	ToggleSelectAll Key `toml:"ToggleSelectAll"`
	Search          Key `toml:"Search"`

	// Search mode keybindings
	SearchMode SearchModeKeymap `toml:"SearchMode"`
}

// SearchModeKeymap holds keybindings specific to search mode.
type SearchModeKeymap struct {
	ConfirmSearch   Key `toml:"ConfirmSearch"`
	EscapeSearch    Key `toml:"EscapeSearch"`
	SearchBackspace Key `toml:"SearchBackspace"`
}

// PlaylistKeymap holds keybindings for the Playlist page.
type PlaylistKeymap struct {
	// Normal mode keybindings
	NavUp      Key `toml:"NavUp"`
	NavDown    Key `toml:"NavDown"`
	RemoveSong Key `toml:"RemoveSong"`
	PlaySong   Key `toml:"PlaySong"`
	Search     Key `toml:"Search"`

	// Search mode keybindings
	SearchMode SearchModeKeymap `toml:"SearchMode"`
}

// GlobalConfig is the global configuration instance.
var GlobalConfig *Config

// specialKeyMap maps string representations of special keys to their rune values.
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
func stringToRune(s string) (rune, error) {
	s = strings.ToLower(s)
	if r, ok := specialKeyMap[s]; ok {
		return r, nil
	}
	if len(s) == 1 {
		return rune(s[0]), nil
	}
	return 0, fmt.Errorf("invalid key: '%s'", s)
}

// getDefaultConfig returns a Config struct with the default keybindings.
func getDefaultConfig() *Config {
	return &Config{
		App: AppConfig{
			MaxHistorySize:   100,  // 默认历史记录数量
			SwitchDebounceMs: 1000, // 默认切歌防抖时间1秒
			DefaultPage:      2,    // 默认启动页面（Library）
			DefaultPlayMode:  0,    // 默认播放模式（单曲循环）
		},
		Keymap: Keymap{
			Global: GlobalKeymap{
				Quit:             Key{"esc"},
				CyclePages:       Key{"tab"},
				SwitchToPlayer:   Key{"1"},
				SwitchToPlayList: Key{"2"},
				SwitchToLibrary:  Key{"3"},
			},
			Player: PlayerKeymap{
				TogglePause:     Key{"space"},
				SeekForward:     Key{"e"},
				SeekBackward:    Key{"q"},
				VolumeUp:        Key{"w"},
				VolumeDown:      Key{"s"},
				RateUp:          Key{"x"},
				RateDown:        Key{"z"},
				NextSong:        Key{"d"},
				PrevSong:        Key{"a"},
				TogglePlayMode:  Key{"r"},
				ToggleTextColor: Key{"c"},
				Reset:           Key{"backspace"},
			},
			Library: LibraryKeymap{
				NavUp:           Key{"k", "w", "up"},
				NavDown:         Key{"j", "s", "down"},
				NavEnterDir:     Key{"l", "d", "right"},
				NavExitDir:      Key{"h", "a", "left"},
				ToggleSelect:    Key{"space"},
				ToggleSelectAll: Key{"e"},
				Search:          Key{"f"},
				SearchMode: SearchModeKeymap{
					ConfirmSearch:   Key{"enter"},
					EscapeSearch:    Key{"esc"}, // Consolidated escape/clear search
					SearchBackspace: Key{"backspace"},
				},
			},
			Playlist: PlaylistKeymap{
				NavUp:      Key{"k", "w", "up"},
				NavDown:    Key{"j", "s", "down"},
				RemoveSong: Key{"space"},
				PlaySong:   Key{"enter"},
				Search:     Key{"f"},
				SearchMode: SearchModeKeymap{
					ConfirmSearch:   Key{"enter"}, // Exit search input mode
					EscapeSearch:    Key{"esc"},   // Consolidated escape/clear search
					SearchBackspace: Key{"backspace"},
				},
			},
		},
	}
}

// LoadConfig loads the configuration from ~/.config/BM/config.toml.
// If the file doesn't exist, it creates it with default values.
func LoadConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not get user home directory: %v", err)
	}
	configPath := filepath.Join(home, ".config", "BM")
	if err := os.MkdirAll(configPath, 0755); err != nil {
		return fmt.Errorf("could not create config directory: %v", err)
	}

	configFile := filepath.Join(configPath, "config.toml")

	defaultConf := getDefaultConfig()

	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		// File does not exist, create it with default config
		buf := new(bytes.Buffer)
		if err := toml.NewEncoder(buf).Encode(defaultConf); err != nil {
			return fmt.Errorf("could not encode default config: %v", err)
		}
		if err := os.WriteFile(configFile, buf.Bytes(), 0644); err != nil {
			return fmt.Errorf("could not write default config file: %v", err)
		}
		GlobalConfig = defaultConf
	} else {
		// File exists, load it
		var config Config
		if _, err := toml.DecodeFile(configFile, &config); err != nil {
			return fmt.Errorf("could not decode config file: %v", err)
		}
		GlobalConfig = &config
	}

	// Validate the loaded keymap
	return validateKeymap(GlobalConfig.Keymap)
}

// validateKeymap checks for duplicate or invalid keybindings.
func validateKeymap(keymap Keymap) error {
	// Page-level validation
	pages := []interface{}{keymap.Global, keymap.Player, keymap.Library, keymap.Playlist}
	pageNames := []string{"Global", "Player", "Library", "Playlist"}

	for i, page := range pages {
		// Separate maps for normal mode and search mode
		normalModeKeys := make(map[rune]string)
		searchModeKeys := make(map[rune]string)

		v := reflect.ValueOf(page)
		t := v.Type()

		for j := 0; j < v.NumField(); j++ {
			field := v.Field(j)
			fieldName := t.Field(j).Name

			if keys, ok := field.Interface().(Key); ok {
				// Normal mode fields
				for _, keyStr := range keys {
					r, err := stringToRune(keyStr)
					if err != nil {
						return fmt.Errorf("invalid key '%s' in [%s] %s", keyStr, pageNames[i], fieldName)
					}

					if existing, duplicated := normalModeKeys[r]; duplicated {
						return fmt.Errorf("key conflict in [%s]: key '%s' is assigned to both '%s' and '%s'", pageNames[i], keyStr, existing, fieldName)
					}
					normalModeKeys[r] = fieldName
				}
			} else if searchMode, ok := field.Interface().(SearchModeKeymap); ok {
				// Search mode fields
				searchModeV := reflect.ValueOf(searchMode)
				searchModeT := searchModeV.Type()

				for k := 0; k < searchModeV.NumField(); k++ {
					searchField := searchModeV.Field(k)
					searchFieldName := searchModeT.Field(k).Name

					if searchKeys, ok := searchField.Interface().(Key); ok {
						for _, keyStr := range searchKeys {
							r, err := stringToRune(keyStr)
							if err != nil {
								return fmt.Errorf("invalid key '%s' in [%s] SearchMode.%s", keyStr, pageNames[i], searchFieldName)
							}

							if existing, duplicated := searchModeKeys[r]; duplicated {
								return fmt.Errorf("key conflict in [%s] SearchMode: key '%s' is assigned to both '%s' and '%s'", pageNames[i], keyStr, existing, searchFieldName)
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
func IsKey(key rune, actionKeys Key) bool {
	for _, keyStr := range actionKeys {
		if r, err := stringToRune(keyStr); err == nil && r == key {
			return true
		}
	}
	return false
}
