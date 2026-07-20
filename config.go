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

//go:embed default.jpg
var defaultCoverImage []byte

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

// IconsConfig holds the customizable icons used in the player UI.
//
// IconsConfig 保存播放器UI中可自定义的图标。
type IconsConfig struct {
	Play           string `toml:"play"`
	Pause          string `toml:"pause"`
	ProgressFilled string `toml:"progress_filled"`
	ProgressEmpty  string `toml:"progress_empty"`
	RepeatOne      string `toml:"repeat_one"`
	RepeatAll      string `toml:"repeat_all"`
	Shuffle        string `toml:"shuffle"`
}

// Config holds the application's configuration, loaded from a TOML file.
//
// Config 保存从TOML文件加载的应用程序配置。
type Config struct {
	Keymap      Keymap                 `toml:"keymap"`
	App         AppConfig              `toml:"app"`
	Icons       map[string]IconsConfig `toml:"icons"`
	ActiveIcons *IconsConfig           `toml:"-"`
}

// AppConfig holds application-level configuration settings.
//
// AppConfig 保存应用程序级别的配置设置。
type AppConfig struct {
	MaxHistorySize       int    `toml:"max_history_size"`
	SwitchDebounceMs     int    `toml:"switch_debounce_ms"`
	LayoutDebounceMs     int    `toml:"layout_debounce_ms"`
	DefaultPage          int    `toml:"default_page"`
	DefaultPlayMode      int    `toml:"default_play_mode"`
	RememberLibraryPath  bool   `toml:"remember_library_path"`
	PlaylistHistory      bool   `toml:"playlist_history"`
	AutostartLastPlayed  bool   `toml:"autostart_last_played"`
	RememberVolume       bool   `toml:"remember_volume"`
	RememberPlaybackRate bool   `toml:"remember_playback_rate"`
	DefaultColorR        int    `toml:"default_color_r"`
	DefaultColorG        int    `toml:"default_color_g"`
	DefaultColorB        int    `toml:"default_color_b"`
	ImageProtocol        string `toml:"image_protocol"`
	EnableNotifications  bool   `toml:"enable_notifications"`
	LibraryPath          string `toml:"library_path"`
	Storage              string `toml:"storage"`
	DefaultCoverPath     string `toml:"default_cover_path"`
	EnableFolderCovers   bool   `toml:"enable_folder_covers"`
	Icons                string `toml:"icons"`
	ShuffleHistoryWindow int    `toml:"shuffle_history_window"`
	MaxSearchDirs        int    `toml:"max_search_dirs"`
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
	ToggleLayout    Key `toml:"ToggleLayout"`
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
// Also ensures the default cover image is copied to the config directory.
//
// LoadConfig 从 ~/.config/BM/config.toml 加载配置。
// 如果文件不存在，它会使用默认值创建它。
// 同时确保默认封面图片被复制到配置目录。
func LoadConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not get user home directory: %v\n\n无法获取用户主目录: %v", err, err)
	}
	configPath := filepath.Join(home, ".config", "BM")
	if err := os.MkdirAll(configPath, 0755); err != nil {
		return fmt.Errorf("could not create config directory: %v\n\n无法创建配置目录: %v", err, err)
	}

	// Copy default cover image if it doesn't exist
	defaultCoverPath := filepath.Join(configPath, "default.jpg")
	if _, err := os.Stat(defaultCoverPath); os.IsNotExist(err) {
		if err := os.WriteFile(defaultCoverPath, defaultCoverImage, 0644); err != nil {
			return fmt.Errorf("could not write default cover image: %v\n\n无法写入默认封面图片: %v", err, err)
		}
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
		if err := updateConfigFile(configFile); err != nil {
			return fmt.Errorf("could not update config file: %v\n\n无法更新配置文件: %v", err, err)
		}
		var config Config
		if _, err := toml.DecodeFile(configFile, &config); err != nil {
			return fmt.Errorf("could not decode config file: %v\n\n无法解析配置文件: %v", err, err)
		}
		GlobalConfig = &config
	}

	if GlobalConfig.App.SwitchDebounceMs <= 0 {
		GlobalConfig.App.SwitchDebounceMs = 200
	}
	if GlobalConfig.App.LayoutDebounceMs <= 0 {
		GlobalConfig.App.LayoutDebounceMs = 200
	}
	if GlobalConfig.App.AutostartLastPlayed && (!GlobalConfig.App.RememberLibraryPath || !GlobalConfig.App.PlaylistHistory) {
		return fmt.Errorf("autostart_last_played can only be enabled when both remember_library_path and playlist_history are also enabled\n\nautostart_last_played 只能在 remember_library_path 和 playlist_history 同时开启时才能开启")
	}

	resolveIconSet(GlobalConfig)

	return validateKeymap(GlobalConfig.Keymap)
}

// validateKeymap checks for duplicate or invalid keybindings.
//
// validateKeymap 检查重复或无效的按键绑定。
// updateConfigFile checks for missing keys in the config file and adds defaults.
//
// updateConfigFile 检查配置文件中是否有缺失的键，并添加默认值。
func updateConfigFile(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	content := string(data)
	updated := false

	missingKeys := []struct {
		section string
		key     string
		value   string
		comment string
	}{
		{"[keymap.player]", "ToggleLayout", "    ToggleLayout = [\"o\"]", "    # Toggle layout mode (only works in wide/narrow mode).\n    #\n    # 切换布局模式（仅在宽/窄模式下有效）。"},
		{"[app]", "max_history_size", "max_history_size = 100", "# Maximum number of history entries - limits the maximum number of playback history records.\n#\n# 最大历史记录数量 - 限制播放历史记录的最大条数"},
		{"[app]", "switch_debounce_ms", "switch_debounce_ms = 50", "# Song switching debounce time (milliseconds) - prevents rapid continuous song switching, avoiding misoperation.\n#\n# 切歌防抖时间（毫秒）- 防止快速连续切歌，避免误操作"},
		{"[app]", "default_page", "default_page = 3", "# Default starting page - the page displayed when the program starts.\n# 0 = Player page, 1 = PlayList page, 2 = Library page, 3 = memory (use saved page from last session).\n#\n# 默认启动页面 - 程序启动时显示的页面。\n# 0 = 播放器页面, 1 = 播放列表页面, 2 = 媒体库页面, 3 = 记忆（使用上次保存的页面）。"},
		{"[app]", "default_play_mode", "default_play_mode = 3", "# Default play mode - the playback mode when the program starts.\n# 0 = repeat one, 1 = repeat all, 2 = random, 3 = memory (use saved play mode from last session).\n#\n# 默认播放模式 - 程序启动时的播放模式。\n# 0 = 单曲循环, 1 = 列表循环, 2 = 随机播放, 3 = 记忆（使用上次保存的播放模式）。"},
		{"[app]", "remember_library_path", "remember_library_path = true", "# Whether to remember the music library path - if true, the program will remember the last used music library path.\n# If no path parameter is specified next time the program starts, the saved path will be used automatically.\n#\n# 是否记录音乐库路径 - 如果为true，程序会记住上次使用的音乐库路径。\n# 下次启动时如果不指定路径参数，会自动使用保存的路径。"},
		{"[app]", "playlist_history", "playlist_history = true", "# Whether to record the playlist - if true, the program will record the playlist and load it next time it starts.\n# Note: 'remember_library_path' must also be true for this to take effect.\n#\n# 是否记录播放列表 - 如果为true，程序会记录播放列表并在下次启动时加载。\n# 注意: 'remember_library_path' 也必须为 true 才能生效。"},
		{"[app]", "autostart_last_played", "autostart_last_played = true", "# Autostart last played song\n# When enabled, the program will automatically play the last played song when starting.\n# Note: This requires both 'remember_library_path' and 'playlist_history' to be enabled.\n#\n# 自动播放上次播放的歌曲\n# 启用后，程序启动时会自动播放上次播放的歌曲。\n# 注意：这需要 'remember_library_path' 和 'playlist_history' 同时启用。"},
		{"[app]", "remember_volume", "remember_volume = true", "# Whether to remember the volume of the last playback\n#\n# 是否记住上次播放的音量"},
		{"[app]", "remember_playback_rate", "remember_playback_rate = true", "# Whether to remember the playback rate\n#\n# 是否记录播放速度"},
		{"[app]", "default_color_r", "default_color_r = 100", "# Default text color - the color used for text display when no suitable color is found from album art.\n# RGB values range from 0 to 255.\n#\n# 默认文字颜色 - 当从专辑封面中找不到合适的颜色时，用于文字显示的颜色。\n# RGB 值的范围是 0 到 255。"},
		{"[app]", "default_color_g", "default_color_g = 149", ""},
		{"[app]", "default_color_b", "default_color_b = 237", ""},
		{"[app]", "image_protocol", "image_protocol = \"auto\"", "# Image rendering protocol - determines which terminal protocol to use for displaying album art.\n# Available options: \"auto\", \"kitty\", \"sixel\", \"iterm2\"\n# \"auto\" will automatically detect the best available protocol.\n#\n# 图像渲染协议 - 决定使用哪种终端协议来显示专辑封面。\n# 可用选项: \"auto\", \"kitty\", \"sixel\", \"iterm2\"\n# \"auto\" 会自动检测最佳可用协议。"},
		{"[app]", "enable_notifications", "enable_notifications = true", "# Enable desktop notifications - whether to show desktop notifications when songs change.\n# If true, the program will send desktop notifications using the freedesktop.org standard.\n#\n# 启用桌面通知 - 是否在歌曲切换时显示桌面通知。\n# 如果为true，程序将使用freedesktop.org标准发送桌面通知。"},
		{"[app]", "storage", "storage = \"~/.config/BM/storage.json\"", "# Storage file path - used to save full path information entered by the user.\n#\n# 存储文件路径 - 用于保存用户输入的完整路径信息。"},
		{"[app]", "default_cover_path", "default_cover_path = \"~/.config/BM/default.jpg\"", "# Default cover art path - the image file to use when a song has no embedded cover art.\n# Supports JPG and PNG formats. If empty or the file doesn't exist, no cover will be shown.\n#\n# 默认封面图片路径 - 当歌曲没有内嵌封面时使用的图片文件。\n# 支持 JPG 和 PNG 格式。如果为空或文件不存在，则不显示封面。"},
		{"[app]", "enable_folder_covers", "enable_folder_covers = true", "# Enable folder cover art - when a song has no embedded cover art, search for image files\n# (jpg, jpeg, png) in the same folder and use one as the cover.\n# If multiple images are found, one is randomly selected.\n# Priority is given to files with names containing cover-related keywords (cover, folder, album, etc.)\n#\n# 启用文件夹封面功能 - 当歌曲没有内嵌封面时，在相同文件夹中搜索图片文件\n# (jpg, jpeg, png) 并使用其中一张作为封面。\n# 如果找到多个图片，随机选择一张。\n# 优先选择文件名包含封面相关关键词的图片（cover、folder、album等）"},
		{"[app]", "icons", "icons = \"auto\"", "# Icon set - which icon set to use for player UI elements (play, pause, progress bar, etc.).\n# Set to \"auto\" to auto-detect based on $TERM and $TERM_PROGRAM.\n# Set to a named set like \"default\" or \"nerd_font\" to use that specific set.\n# Named icon sets are defined in [icons.<name>] sections below.\n#\n# 图标集 - 播放器UI元素使用哪套图标（播放、暂停、进度条等）。\n# 设置为 \"auto\" 时根据 $TERM 和 $TERM_PROGRAM 自动检测。\n# 设置为 \"default\" 或 \"nerd_font\" 等命名集合直接使用对应图标集。\n# 命名图标集定义在下方 [icons.<name>] 节中。"},
		{"[app]", "shuffle_history_window", "shuffle_history_window = -1", "# Shuffle history window - when in random play mode, exclude the last N unique songs from play history\n# from the random selection to avoid frequent repeats.\n# 0 = disabled (pure random).\n# -1 or value >= playlist length = never repeat until all songs in the playlist have been played.\n# Positive value = exclude the last N unique songs from history.\n#\n# 随机播放历史窗口 - 在随机播放模式下，从播放历史的最近 N 首不重复歌曲中排除，\n# 避免频繁重复。设为 0 禁用此功能（纯随机）。负值或大于等于歌单长度的值表示歌单内\n# 所有歌曲都听过一遍之前不重复。"},
		{"[app]", "max_search_dirs", "max_search_dirs = 15", "# Maximum number of directory results to show in search - limits the visible directory entries\n# in search results on the Library page. The rest of the directories are still accessible via scrolling.\n# Files below the separator are not limited.\n#\n# 搜索结果中最多显示的目录数量 - 限制媒体库页面搜索结果中可见的目录条目。\n# 其余目录仍可通过滚动访问。分割线下的文件不受此限制。"},
		{"[app]", "layout_debounce_ms", "layout_debounce_ms = 200", "# Layout switching debounce time (milliseconds) - prevents rapid layout switching.\n#\n# 布局切换防抖时间（毫秒）- 防止快速连续切换布局。"},
	}

	for _, missing := range missingKeys {
		if strings.Contains(content, missing.section) && !strings.Contains(content, missing.key) {
			lines := strings.Split(content, "\n")
			var newLines []string
			inSection := false
			added := false

			for _, line := range lines {
				newLines = append(newLines, line)
				if strings.Contains(line, missing.section) {
					inSection = true
				}
				if inSection && !added {
					if strings.HasPrefix(strings.TrimSpace(line), "Reset") || (strings.HasPrefix(strings.TrimSpace(line), "[") && !strings.Contains(line, missing.section)) {
						if missing.comment != "" {
							newLines = append(newLines, "\n"+missing.comment)
						}
						newLines = append(newLines, missing.value)
						added = true
						updated = true
					}
				}
				if inSection && strings.HasPrefix(strings.TrimSpace(line), "[") && !strings.Contains(line, missing.section) {
					inSection = false
				}
			}

			if added {
				content = strings.Join(newLines, "\n")
			}
		}
	}

	if updated {
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			return err
		}
	}

	return nil
}

func validateKeymap(keymap Keymap) error {
	pages := []any{keymap.Global, keymap.Player, keymap.Library, keymap.Playlist}
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

// resolveIconSet resolves which icon set to use based on the app.icons setting.
// If set to "auto", it auto-detects from $TERM and $TERM_PROGRAM environment variables.
// Falls back to the "default" set or hardcoded defaults.
//
// resolveIconSet 根据 app.icons 设置解析使用哪个图标集。
// 如果设置为 "auto"，则从 $TERM 和 $TERM_PROGRAM 环境变量自动检测。
// 回退到 "default" 集合或硬编码的默认值。
func resolveIconSet(config *Config) {
	defaultIcons := IconsConfig{
		Play:           "▶",
		Pause:          "⏸",
		ProgressFilled: "█",
		ProgressEmpty:  "░",
		RepeatOne:      "🗘",
		RepeatAll:      "⇆",
		Shuffle:        "⤮",
	}

	iconSetName := config.App.Icons
	if iconSetName == "" {
		iconSetName = "auto"
	}

	if iconSetName == "auto" {
		for _, env := range []string{"TERM", "TERM_PROGRAM"} {
			if term := os.Getenv(env); term != "" {
				term = strings.ToLower(term)
				if icons, ok := config.Icons[term]; ok {
					config.ActiveIcons = &icons
					return
				}
			}
		}
		if icons, ok := config.Icons["default"]; ok {
			config.ActiveIcons = &icons
			return
		}
		config.ActiveIcons = &defaultIcons
		return
	}

	if icons, ok := config.Icons[iconSetName]; ok {
		config.ActiveIcons = &icons
		return
	}

	config.ActiveIcons = &defaultIcons
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
