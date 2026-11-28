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
// configured mode. With the sync option on both pages share the Library
// setting; otherwise each page keeps its own.
//
// initialNameDisplayMode 解析启动时的歌曲名显示模式：记忆模式恢复已保存的选择，
// 其他取值直接使用配置的模式。同步选项开启时两个页面共用媒体库的设置；
// 否则各页面使用自己的设置。
func initialNameDisplayMode(library bool) int {
	if GlobalConfig.App.SyncNameDisplay {
		library = true
	}
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

// saveNameDisplay stores one song name display mode and logs a warning when
// the storage file cannot be written.
//
// saveNameDisplay 保存一个歌曲名显示模式，存储文件写入失败时记录警告。
func saveNameDisplay(library bool, mode int) {
	if err := SaveNameDisplay(library, mode); err != nil {
		l.Warnf("failed to save name display mode: %v\n\n警告: 保存歌曲名显示模式失败: %v", err, err)
	}
}

// applyNameDisplayMode applies a new song name display mode to one page and
// remembers it. With the sync option on both pages take the same mode and both
// stored values follow; otherwise only the requesting page changes.
//
// applyNameDisplayMode 把新的歌曲名显示模式应用到一个页面并记住它。
// 同步选项开启时两个页面取同一模式，两个存储值同时更新；
// 否则只改变请求的页面。
func (a *App) applyNameDisplayMode(library bool, mode int) {
	if GlobalConfig.App.SyncNameDisplay {
		if lp, ok := a.libraryPage(); ok {
			lp.nameDisplayMode = mode
		}
		if pp, ok := a.playlistPage(); ok {
			pp.nameDisplayMode = mode
		}
		saveNameDisplay(true, mode)
		saveNameDisplay(false, mode)
		return
	}
	if library {
		if lp, ok := a.libraryPage(); ok {
			lp.nameDisplayMode = mode
		}
	} else {
		if pp, ok := a.playlistPage(); ok {
			pp.nameDisplayMode = mode
		}
	}
	saveNameDisplay(library, mode)
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

// nameWidths is the alignment information of one name group: the display
// width of the widest first field and of the widest second field.
//
// nameWidths 是一个名称组的对齐信息：最宽第一字段与最宽第二字段的显示宽度。
type nameWidths struct {
	first  int
	second int
}

// nameDisplayWidths measures the widest first and second display fields of the
// group under the given mode. Only the aligned modes split names, every other
// mode reports zero widths, and names without a separator do not contribute.
//
// nameDisplayWidths 按给定模式测量组内最宽的第一与第二显示字段。只有对齐模式
// 拆分名称，其他模式返回零宽度，不含分隔符的名称不计入。
func nameDisplayWidths(mode int, names []string) nameWidths {
	if mode != nameDisplayAlign && mode != nameDisplayTitleFirstAlign {
		return nameWidths{}
	}
	var widths nameWidths
	for _, name := range names {
		first, second, aligned := nameDisplayRow(name, mode)
		if !aligned {
			continue
		}
		widths.first = max(widths.first, runewidth.StringWidth(first))
		widths.second = max(widths.second, runewidth.StringWidth(second))
	}
	return widths
}

// alignSeparatorCol returns the content column of the "-" separator in an
// aligned row. The remaining space goes to the longer name field but never
// past what that field needs: the separator hugs the first field when both
// fields fit, and the second field reaches the right edge when the first
// field still needs more room. The first field hugs the separator when it is
// shorter, and an equal pair splits the space evenly only when the two fields
// compete for it. A side that would fall below half of the available space is
// kept at half so both fields stay visible. prefixW is the display width of
// the row prefix.
//
// alignSeparatorCol 返回对齐行 "-" 分隔符所在的内容列。剩余空间给较长的名称
// 字段，但不超过该字段的需求：两个字段都放得下时分隔符紧贴第一字段，
// 第一字段还需要更多空间时第二字段贴右缘。第一字段较短时紧贴分隔符，
// 两者等长且争抢空间时才对半。任一侧不足可用空间一半时保留一半，
// 保证两个字段都可见。prefixW 是行前缀显示宽度。
func alignSeparatorCol(widths nameWidths, prefixW, contentW int) int {
	available := contentW - prefixW - 3
	halfBudget := available / 2
	firstBudget := halfBudget
	switch {
	case widths.second < widths.first:
		firstBudget = min(widths.first, max(available-widths.second, halfBudget))
	case widths.second > widths.first:
		firstBudget = min(widths.first, available-halfBudget)
	default:
		firstBudget = min(widths.first, halfBudget)
	}
	return prefixW + firstBudget + 1
}

// nameDisplayRow formats one song name into row fields. The aligned modes
// split the name into the two fields around the separator; every other mode
// keeps the whole line in first. Names without a separator keep the whole
// name in first under every mode.
//
// nameDisplayRow 把歌曲名格式化为行字段。对齐模式把名称拆成分隔符两侧的两个
// 字段；其他模式整行放 first。不含分隔符的名称在任何模式下都整名放 first。
func nameDisplayRow(name string, mode int) (first, second string, aligned bool) {
	if mode != nameDisplayAlign && mode != nameDisplayTitleFirstAlign {
		return displaySongName(name, mode), "", false
	}
	artist, title, ext, ok := songNameParts(name)
	if !ok {
		return name, "", false
	}
	if mode == nameDisplayTitleFirstAlign {
		return title, artist, true
	}
	return artist, title + ext, true
}

// displaySongName formats one song name in one whole line for the modes
// without alignment. Names without a separator are returned unchanged.
//
// displaySongName 为不对齐的模式把歌曲名格式化为整行文本。
// 不含分隔符的名称原样返回。
func displaySongName(name string, mode int) string {
	if mode == nameDisplayDefault {
		return name
	}
	artist, title, ext, ok := songNameParts(name)
	if !ok {
		return name
	}
	hideExt := mode == nameDisplayNoExt || mode == nameDisplayTitleFirst
	titleFirst := mode == nameDisplayTitleFirst
	if !hideExt {
		title += ext
	}
	first, second := artist, title
	if titleFirst {
		first, second = title, artist
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
