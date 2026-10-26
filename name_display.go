package main

import (
	"path/filepath"
	"strings"

	"github.com/mattn/go-runewidth"
)

// Song name display modes for the browsing lists. The first four describe
// what to show and the last one is the memory setting that restores the saved
// choice.
//
// 浏览列表的歌曲名显示模式。前四种描述显示内容，
// 最后一种是记忆设置，恢复已保存的选择。
const (
	nameDisplayDefault         = 0
	nameDisplayNoExt           = 1
	nameDisplayAlign           = 2
	nameDisplayTitleFirst      = 3
	nameDisplayTitleFirstAlign = 4
	nameDisplayMemory          = 5
)

// nameDisplayTicks is how many ticks the name display mode stays visible in
// the footer after a switch.
//
// nameDisplayTicks 是切换后歌曲名显示模式在页脚显示的 tick 数。
const nameDisplayTicks = 10

// resolveNameDisplayMode maps a name display setting to a runtime mode. The
// value 5 (memory) and any other out-of-range value fall back to the default
// display.
//
// resolveNameDisplayMode 将歌曲名显示设置映射为运行时模式。取值 5（记忆）及
// 其他越界取值回退为默认显示。
func resolveNameDisplayMode(mode int) int {
	if mode < nameDisplayDefault || mode > nameDisplayTitleFirstAlign {
		return nameDisplayDefault
	}
	return mode
}

// initialNameDisplayMode resolves the song name display mode used at startup:
// the memory mode restores the saved choice, anything else uses the
// configured mode. The library and the playlist keep separate settings.
//
// initialNameDisplayMode 解析启动时的歌曲名显示模式：记忆模式恢复已保存的选择，
// 其他取值直接使用配置的模式。媒体库与播放列表各自独立设置。
func initialNameDisplayMode(library bool) int {
	configured := GlobalConfig.App.DefaultNameDisplayPlaylist
	if library {
		configured = GlobalConfig.App.DefaultNameDisplayLibrary
	}
	if configured == nameDisplayMemory {
		saved, err := LoadNameDisplay(library)
		if err != nil {
			l.Warnf("could not load saved name display: %v\n\n警告: 无法加载已保存的歌曲名显示模式: %v", err, err)
		} else {
			return saved
		}
	}
	return resolveNameDisplayMode(configured)
}

// nameDisplayModeName returns the footer name of a song name display mode.
//
// nameDisplayModeName 返回歌曲名显示模式在页脚显示的名称。
func nameDisplayModeName(mode int) string {
	switch mode {
	case nameDisplayNoExt:
		return "no-ext"
	case nameDisplayAlign:
		return "align"
	case nameDisplayTitleFirst:
		return "title-first"
	case nameDisplayTitleFirstAlign:
		return "title-first-align"
	}
	return "default"
}

// songNameParts splits a file name into artist, title and extension around
// the first " - ". It reports false when the name has no separator or one of
// the two fields is empty, and such names are shown unchanged.
//
// songNameParts 以第一个 " - " 为界把文件名拆成歌手、歌名和后缀。
// 名称不含分隔符或任一字段为空时返回 false，这类名称原样显示。
func songNameParts(name string) (artist, title, ext string, ok bool) {
	ext = filepath.Ext(name)
	stem := name[:len(name)-len(ext)]
	before, after, ok := strings.Cut(stem, " - ")
	if !ok {
		return "", "", "", false
	}
	artist = before
	title = after
	if artist == "" || title == "" {
		return "", "", "", false
	}
	return artist, title, ext, true
}

// nameDisplayPad returns the display width the first name field is padded to
// across the group. It is zero for modes without alignment and names without
// a separator do not contribute.
//
// nameDisplayPad 返回组内第一个名称字段的填充宽度。不对齐的模式返回 0，
// 不含分隔符的名称不计入。
func nameDisplayPad(mode int, names []string) int {
	if mode != nameDisplayAlign && mode != nameDisplayTitleFirstAlign {
		return 0
	}
	titleFirst := mode == nameDisplayTitleFirstAlign
	pad := 0
	for _, name := range names {
		artist, title, _, ok := songNameParts(name)
		if !ok {
			continue
		}
		first := artist
		if titleFirst {
			first = title
		}
		pad = max(pad, runewidth.StringWidth(first))
	}
	return pad
}

// displaySongName formats one song name under the given mode. Names without a
// separator are returned unchanged. The pad aligns the separator and the
// second field across the group and is ignored by modes without alignment.
//
// displaySongName 按给定模式格式化一个歌曲名。不含分隔符的名称原样返回。
// pad 使分隔符和第二个字段在组内对齐，不对齐的模式忽略该参数。
func displaySongName(name string, mode int, pad int) string {
	if mode == nameDisplayDefault {
		return name
	}
	artist, title, ext, ok := songNameParts(name)
	if !ok {
		return name
	}
	hideExt := mode == nameDisplayNoExt || mode == nameDisplayTitleFirst || mode == nameDisplayTitleFirstAlign
	titleFirst := mode == nameDisplayTitleFirst || mode == nameDisplayTitleFirstAlign
	align := mode == nameDisplayAlign || mode == nameDisplayTitleFirstAlign

	if !hideExt {
		title += ext
	}
	first, second := artist, title
	if titleFirst {
		first, second = title, artist
	}
	if align && pad > runewidth.StringWidth(first) {
		first += strings.Repeat(" ", pad-runewidth.StringWidth(first))
	}
	return first + " - " + second
}

// entryNames returns the file names of a listing; directories never take
// part in the song name display.
//
// entryNames 返回列表中的文件名；目录不参与歌曲名显示。
func entryNames(entries []LibraryEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.isDir {
			names = append(names, entry.entry.Name())
		}
	}
	return names
}

// itemBaseNames returns the base names of search result paths.
//
// itemBaseNames 返回搜索结果路径的基础名。
func itemBaseNames(items []string) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, filepath.Base(item))
	}
	return names
}
