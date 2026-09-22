package main

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

// minHelpColumnWidth is the narrowest readable help page column; layouts that
// would squeeze a column below it are replaced by a short message.
//
// minHelpColumnWidth 是帮助页可读的最小列宽，会把列挤压到此宽度以下的布局
// 将被一行简短提示替代。
const minHelpColumnWidth = 26

// helpDescriptions maps keymap actions to their English and Chinese labels on
// the keyboard shortcuts help page, keyed by "<Section>.<Action>".
//
// helpDescriptions 按 "<分组>.<动作>" 保存快捷键帮助页动作的中英文标签。
var helpDescriptions = map[string][2]string{
	"Global.Quit":             {"Quit the program", "退出程序"},
	"Global.ShowHelp":         {"Show this help page", "显示本帮助页"},
	"Global.CyclePages":       {"Cycle through pages", "循环切换页面"},
	"Global.SwitchToPlayer":   {"Switch to the Player page", "切换到播放器页面"},
	"Global.SwitchToPlayList": {"Switch to the PlayList page", "切换到播放列表页面"},
	"Global.SwitchToLibrary":  {"Switch to the Library page", "切换到媒体库页面"},

	"Player.TogglePause":         {"Toggle pause", "切换播放/暂停"},
	"Player.SeekForward":         {"Seek forward", "快进"},
	"Player.SeekBackward":        {"Seek backward", "快退"},
	"Player.VolumeUp":            {"Volume up", "增大音量"},
	"Player.VolumeDown":          {"Volume down", "减小音量"},
	"Player.RateUp":              {"Speed up playback", "加快播放速度"},
	"Player.RateDown":            {"Slow down playback", "减慢播放速度"},
	"Player.NextSong":            {"Next song", "下一首"},
	"Player.PrevSong":            {"Previous song", "上一首"},
	"Player.TogglePlayMode":      {"Toggle play mode", "切换播放模式"},
	"Player.ToggleTextColor":     {"Toggle text color", "切换文字颜色"},
	"Player.Reset":               {"Reset playback", "重置播放进度"},
	"Player.ToggleLayout":        {"Toggle layout", "切换布局"},
	"Player.ToggleNotifications": {"Toggle notifications", "切换通知"},

	"PlayList.NavUp":           {"Navigate up", "向上导航"},
	"PlayList.NavDown":         {"Navigate down", "向下导航"},
	"PlayList.RemoveSong":      {"Remove song", "移除歌曲"},
	"PlayList.PlaySong":        {"Play song", "播放歌曲"},
	"PlayList.Search":          {"Search the playlist", "搜索播放列表"},
	"PlayList.ConfirmSearch":   {"Confirm search", "确认搜索"},
	"PlayList.EscapeSearch":    {"Exit search", "退出搜索"},
	"PlayList.SearchBackspace": {"Delete search character", "删除搜索字符"},

	"Library.NavUp":           {"Navigate up", "向上导航"},
	"Library.NavDown":         {"Navigate down", "向下导航"},
	"Library.NavEnterDir":     {"Enter directory", "进入目录"},
	"Library.NavExitDir":      {"Exit directory", "退出目录"},
	"Library.ToggleSelect":    {"Toggle selection", "切换选中"},
	"Library.ToggleSelectAll": {"Toggle select all", "全选/取消全选"},
	"Library.Search":          {"Search the library", "搜索媒体库"},
	"Library.ConfirmSearch":   {"Confirm search", "确认搜索"},
	"Library.EscapeSearch":    {"Exit search", "退出搜索"},
	"Library.SearchBackspace": {"Delete search character", "删除搜索字符"},
}

// helpSectionTitles holds the English and Chinese names of the help page sections.
//
// helpSectionTitles 保存帮助页分组的中英文名称。
var helpSectionTitles = map[string][2]string{
	"Global":   {"Global", "全局"},
	"Player":   {"Player", "播放器"},
	"PlayList": {"PlayList", "播放列表"},
	"Library":  {"Library", "媒体库"},
}

// helpDescription returns the localized label for a keymap action, falling back
// to the action name when no translation exists.
//
// helpDescription 返回按键映射动作的本地化标签，无翻译时回退到动作名。
func helpDescription(section, action string, zh bool) string {
	if labels, ok := helpDescriptions[section+"."+action]; ok {
		if zh {
			return labels[1]
		}
		return labels[0]
	}
	return action
}

// helpTitle returns the localized name of a help page section.
//
// helpTitle 返回帮助页分组的本地化名称。
func helpTitle(section string, zh bool) string {
	if labels, ok := helpSectionTitles[section]; ok {
		if zh {
			return labels[1]
		}
		return labels[0]
	}
	return section
}

// helpRow describes a single keybinding line on the keyboard shortcuts help page.
//
// helpRow 描述快捷键帮助页中的一行按键绑定。
type helpRow struct {
	name   string
	keys   string
	search bool
}

// helpBlock groups the keybindings of one keymap section under a heading.
//
// helpBlock 将同一按键映射分组的快捷键归入一个标题下。
type helpBlock struct {
	title string
	rows  []helpRow
}

// height returns the number of lines the block occupies, heading included.
//
// height 返回该区块占用的行数，含标题行。
func (b helpBlock) height() int {
	return 1 + len(b.rows)
}

// helpLine is one rendered line of the help page, either a section heading or a
// keybinding row.
//
// helpLine 是帮助页中的一行渲染内容，为分组标题或按键绑定行。
type helpLine struct {
	blank bool
	title string
	row   helpRow
}

// collectHelpBlocks walks the configured keymaps by reflection and returns the
// sections shown on the keyboard shortcuts help page in the given language.
//
// collectHelpBlocks 通过反射遍历配置中的按键映射，
// 返回快捷键帮助页在指定语言下显示的分组。
func collectHelpBlocks(zh bool) []helpBlock {
	groups := []string{"Global", "Player", "PlayList", "Library"}
	pages := []any{
		GlobalConfig.Keymap.Global,
		GlobalConfig.Keymap.Player,
		GlobalConfig.Keymap.Playlist,
		GlobalConfig.Keymap.Library,
	}

	blocks := make([]helpBlock, 0, len(groups))
	for g, page := range pages {
		section := groups[g]
		block := helpBlock{title: helpTitle(section, zh)}
		v := reflect.ValueOf(page)
		t := v.Type()
		for i := range v.NumField() {
			field := v.Field(i)
			action := t.Field(i).Name
			if keys, ok := field.Interface().(Key); ok {
				block.rows = append(block.rows, helpRow{
					name: helpDescription(section, action, zh),
					keys: strings.Join(keys, ", "),
				})
			} else if searchMode, ok := field.Interface().(SearchModeKeymap); ok {
				sv := reflect.ValueOf(searchMode)
				st := sv.Type()
				for j := range sv.NumField() {
					if keys, ok := sv.Field(j).Interface().(Key); ok {
						subAction := st.Field(j).Name
						block.rows = append(block.rows, helpRow{
							name:   helpDescription(section, subAction, zh),
							keys:   strings.Join(keys, ", "),
							search: true,
						})
					}
				}
			}
		}
		blocks = append(blocks, block)
	}
	return blocks
}

// bestBlockPartition splits blocks into the given number of contiguous column
// groups and returns the cut positions minimizing the tallest column.
//
// bestBlockPartition 将区块切成指定数量的连续列分组，
// 返回使最高列最矮的切分点。
func bestBlockPartition(heights []int, groups int) ([]int, int) {
	n := len(heights)
	groupHeight := func(lo, hi int) int {
		total := 0
		for i := lo; i <= hi; i++ {
			total += heights[i]
		}
		return total + (hi - lo)
	}

	var bestCuts []int
	bestMax := -1
	var walk func(lo, col int, cuts []int, curMax int)
	walk = func(lo, col int, cuts []int, curMax int) {
		if col == groups-1 {
			tail := 0
			if lo <= n-1 {
				tail = groupHeight(lo, n-1)
			}
			m := max(curMax, tail)
			if bestMax < 0 || m < bestMax {
				bestMax = m
				bestCuts = append([]int(nil), cuts...)
			}
			return
		}
		for hi := lo - 1; hi < n; hi++ {
			height := 0
			if lo <= hi {
				height = groupHeight(lo, hi)
			}
			next := append(append([]int(nil), cuts...), hi)
			walk(hi+1, col+1, next, max(curMax, height))
		}
	}
	walk(0, 0, nil, 0)
	return bestCuts, bestMax
}

// columnLines builds the lines of one column from the blocks in the given range.
//
// columnLines 用给定范围内的区块构建一列的行。
func columnLines(blocks []helpBlock, lo, hi int) []helpLine {
	lines := make([]helpLine, 0)
	for i := lo; i <= hi; i++ {
		if i > lo {
			lines = append(lines, helpLine{blank: true})
		}
		lines = append(lines, helpLine{title: blocks[i].title})
		for _, row := range blocks[i].rows {
			lines = append(lines, helpLine{row: row})
		}
	}
	return lines
}

// buildPartitionColumns packs whole blocks into columns along the given cuts.
//
// buildPartitionColumns 按给定切分点把整块分组装入各列。
func buildPartitionColumns(blocks []helpBlock, cuts []int) [][]helpLine {
	columns := make([][]helpLine, 0, len(cuts)+1)
	start := 0
	for _, end := range cuts {
		columns = append(columns, columnLines(blocks, start, end))
		start = end + 1
	}
	columns = append(columns, columnLines(blocks, start, len(blocks)-1))
	return columns
}

// buildFlowColumns streams the lines through the columns, splitting oversized
// blocks only when no whole-block layout fits.
//
// buildFlowColumns 将行流式排入各列，仅在整块布局放不下时才拆分超大分组。
func buildFlowColumns(blocks []helpBlock, contentRows, cols int) [][]helpLine {
	all := make([]helpLine, 0)
	for i, block := range blocks {
		if i > 0 {
			all = append(all, helpLine{blank: true})
		}
		all = append(all, helpLine{title: block.title})
		for _, row := range block.rows {
			all = append(all, helpLine{row: row})
		}
	}

	columns := make([][]helpLine, 0, cols)
	col := make([]helpLine, 0, contentRows)
	for _, line := range all {
		if len(col) >= contentRows {
			columns = append(columns, col)
			if len(columns) == cols {
				break
			}
			col = make([]helpLine, 0, contentRows)
		}
		if line.title != "" && len(col) == contentRows-1 {
			col = append(col, helpLine{blank: true})
			continue
		}
		col = append(col, line)
	}
	if len(columns) < cols {
		columns = append(columns, col)
	}
	return columns
}

// planHelpColumns assigns the help blocks to readable columns and returns the
// lines of each column, or nil when the terminal is too small to be readable.
//
// planHelpColumns 将帮助分组排入可读的列并返回每列的行，
// 终端过小无法阅读时返回 nil。
func planHelpColumns(blocks []helpBlock, contentRows, maxCols int) [][]helpLine {
	if maxCols < 2 {
		return nil
	}

	heights := make([]int, len(blocks))
	totalRows := 0
	for i, block := range blocks {
		heights[i] = block.height()
		totalRows += heights[i]
	}
	totalRows += max(len(blocks)-1, 0)

	for groups := 2; groups <= min(maxCols, len(blocks)); groups++ {
		cuts, best := bestBlockPartition(heights, groups)
		if best <= contentRows {
			return buildPartitionColumns(blocks, cuts)
		}
	}

	flowCols := max((totalRows+contentRows-1)/contentRows, 2)
	if flowCols > maxCols {
		return nil
	}
	return buildFlowColumns(blocks, contentRows, flowCols)
}

// renderHelpLine draws one help page line at the given position, truncated to
// the column width.
//
// renderHelpLine 在给定位置绘制一行帮助页内容，并按列宽截断。
func renderHelpLine(y, x, colWidth, keysWidth int, line helpLine) {
	if line.blank {
		return
	}
	if line.title != "" {
		heading := truncateToWidthFromStart(line.title, colWidth-1)
		dashes := max(colWidth-runewidth.StringWidth(heading)-2, 0)
		fmt.Printf("\x1b[%d;%dH\x1b[1m%s\x1b[0m \x1b[90m%s\x1b[0m", y, x, heading, strings.Repeat("─", dashes))
		return
	}
	keys := truncateToWidthFromStart(line.row.keys, keysWidth)
	nameWidth := max(colWidth-keysWidth-3, 1)
	name := truncateToWidthFromStart(line.row.name, nameWidth)
	fmt.Printf("\x1b[%d;%dH\x1b[32m%s\x1b[0m", y, x, keys)
	if line.row.search {
		name += "\x1b[90m*\x1b[0m"
	}
	fmt.Printf("\x1b[%d;%dH%s", y, x+keysWidth+2, name)
}

// overlayOpen reports whether the keyboard shortcuts help page or the quit
// confirmation prompt is currently shown.
//
// overlayOpen 判断快捷键帮助页或退出确认提示是否正在显示。
func (a *App) overlayOpen() bool {
	return a.helpOpen || a.confirmQuitOpen
}

// redrawOverlay redraws the topmost overlay on top of whatever the page just
// rendered inside the same atomic frame, so modal overlays are never wiped by
// page updates and never blink.
//
// redrawOverlay 在同一原子帧内、于页面刚渲染完的内容之上重绘最上层浮层，
// 使模态浮层不会被页面更新冲掉，也不会闪烁。
func (a *App) redrawOverlay() {
	if a.helpOpen {
		a.drawHelpPage()
	} else if a.confirmQuitOpen {
		a.drawQuitPrompt()
	}
}

// quitPromptFits reports whether the terminal is large enough to display the
// quit confirmation prompt readably; below that the prompt is skipped entirely.
//
// quitPromptFits 判断终端是否大到可以清晰显示退出确认提示；
// 不满足时直接跳过提示。
func quitPromptFits(w, h int) bool {
	return w >= 40 && h >= 7
}

// wantsQuitConfirm reports whether the given page shows the quit confirmation
// prompt instead of quitting immediately.
//
// wantsQuitConfirm 判断给定页面是否应显示退出确认提示而非直接退出。
func wantsQuitConfirm(page Page) bool {
	enabled := false
	switch page.(type) {
	case *PlayerPage:
		enabled = GlobalConfig.App.ConfirmQuitPlayer
	case *PlayList:
		enabled = GlobalConfig.App.ConfirmQuitPlaylist
	case *Library:
		enabled = GlobalConfig.App.ConfirmQuitLibrary
	}
	if !enabled {
		return false
	}
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}
	return quitPromptFits(w, h)
}

// handleOverlayKey processes a key while the keyboard shortcuts help page or the
// quit confirmation prompt is open. Both overlays are modal and consume every
// key. The return value reports whether the application should quit.
//
// handleOverlayKey 处理快捷键帮助页或退出确认提示打开时的按键。
// 两个浮层均为模态并消费所有按键，返回值表示应用程序是否应退出。
func (a *App) handleOverlayKey(key rune) bool {
	if a.confirmQuitOpen {
		if key == KeyEnter {
			return true
		}
		if key == '\x1b' || IsKey(key, GlobalConfig.Keymap.Global.Quit) {
			a.pages[a.currentPageIndex].View()
			a.confirmQuitOpen = false
		}
		return false
	}
	if key == '\x1b' || IsKey(key, GlobalConfig.Keymap.Global.Quit) || IsKey(key, GlobalConfig.Keymap.Global.ShowHelp) {
		a.pages[a.currentPageIndex].View()
		a.helpOpen = false
	}
	return false
}

// drawHelpPage renders the full-screen keyboard shortcuts help page from the
// user's configured keybindings.
//
// drawHelpPage 根据用户配置的按键绑定渲染全屏快捷键帮助页。
func (a *App) drawHelpPage() {
	a.beginFrame()
	defer a.endFrame()
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	fmt.Print("\x1b[2J\x1b[3J\x1b[H")

	zh := GlobalConfig.App.HelpLanguage == "zh"
	title := "Keyboard Shortcuts"
	if zh {
		title = "快捷键"
	}
	titleX := max((w-runewidth.StringWidth(title))/2, 0) + 1
	fmt.Printf("\x1b[1;%dH\x1b[1m%s\x1b[0m", titleX, title)

	blocks := collectHelpBlocks(zh)
	contentRows := max(h-4, 1)
	columns := planHelpColumns(blocks, contentRows, w/minHelpColumnWidth)

	if columns != nil {
		colWidth := max(w/len(columns), 1)
		for col, lines := range columns {
			keysWidth := 0
			for _, line := range lines {
				keysWidth = max(keysWidth, runewidth.StringWidth(line.row.keys))
			}
			for i, line := range lines {
				renderHelpLine(3+i, 1+col*colWidth, colWidth, keysWidth, line)
			}
		}
	} else {
		msg := "Terminal too small to show the keymap"
		if zh {
			msg = "终端过小，无法显示快捷键"
		}
		msg = truncateToWidthFromStart(msg, max(w-1, 1))
		x := max((w-runewidth.StringWidth(msg))/2, 0) + 1
		fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", max(h/2, 1), x, msg)
	}

	legend := "* Only active"
	hint := "Press " + strings.Join(GlobalConfig.Keymap.Global.Quit, ", ") + " to go back"
	if zh {
		legend = "* 仅输入时有效"
		hint = "按 " + strings.Join(GlobalConfig.Keymap.Global.Quit, ", ") + " 返回"
	}
	hintX := max((w-runewidth.StringWidth(hint))/2, 0) + 1
	if columns != nil {
		legendX := max((w-runewidth.StringWidth(legend))/2, 0) + 1
		fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", h-1, legendX, legend)
	}
	fmt.Printf("\x1b[%d;%dH\x1b[90m%s\x1b[0m", h, hintX, hint)
}

// buildQuitKeysLine renders the quit prompt key hint sized to fit the given
// width, dropping the action words when space is tight.
//
// buildQuitKeysLine 在给定宽度内渲染退出提示的按键行，
// 空间不足时省略动作词。
func buildQuitKeysLine(width int, confirmKey, cancelKey string, zh bool) string {
	suffixConfirm := " Quit"
	suffixCancel := " Cancel"
	if zh {
		suffixConfirm = " 退出"
		suffixCancel = " 取消"
	}
	gap := 6
	total := runewidth.StringWidth(confirmKey+suffixConfirm) + gap + runewidth.StringWidth(cancelKey+suffixCancel)
	if total > width {
		suffixConfirm = ""
		suffixCancel = ""
		gap = 2
		total = runewidth.StringWidth(confirmKey) + gap + runewidth.StringWidth(cancelKey)
	}
	pad := max((width-total)/2, 0)
	tail := max(width-total-pad, 0)
	return strings.Repeat(" ", pad) +
		"\x1b[32m" + confirmKey + "\x1b[0m" + suffixConfirm + strings.Repeat(" ", gap) +
		"\x1b[32m" + cancelKey + "\x1b[0m" + suffixCancel + strings.Repeat(" ", tail)
}

// drawQuitPrompt renders the modal quit confirmation prompt centered over the
// current page, overwriting only the columns the box occupies.
//
// drawQuitPrompt 在当前页面之上居中渲染模态的退出确认提示，
// 只覆写方框占用的列。
func (a *App) drawQuitPrompt() {
	a.beginFrame()
	defer a.endFrame()
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	zh := GlobalConfig.App.HelpLanguage == "zh"
	confirmKey := "enter"
	cancelKey := "esc"
	if keys := GlobalConfig.Keymap.Global.Quit; len(keys) > 0 {
		cancelKey = keys[0]
	}

	if !quitPromptFits(w, h) {
		return
	}

	const boxW = 38
	const boxH = 5
	width := min(boxW, w)
	inner := width - 2
	left := max((w-width)/2, 0) + 1
	top := max((h-boxH)/2, 0) + 1

	fmt.Printf("\x1b[%d;%dH\x1b[90m┌%s┐\x1b[0m", top, left, strings.Repeat("─", inner))

	msg := "Are you sure you want to quit?"
	if zh {
		msg = "确定要退出吗？"
	}
	msg = truncateToWidthFromStart(msg, inner)
	msgWidth := runewidth.StringWidth(msg)
	msgPad := max((inner-msgWidth)/2, 0)
	msgLine := strings.Repeat(" ", msgPad) + msg + strings.Repeat(" ", max(inner-msgWidth-msgPad, 0))
	fmt.Printf("\x1b[%d;%dH\x1b[90m│\x1b[0m\x1b[1m%s\x1b[0m\x1b[90m│\x1b[0m", top+1, left, msgLine)

	fmt.Printf("\x1b[%d;%dH\x1b[90m│%s│\x1b[0m", top+2, left, strings.Repeat(" ", inner))

	keysLine := buildQuitKeysLine(inner, confirmKey, cancelKey, zh)
	fmt.Printf("\x1b[%d;%dH\x1b[90m│\x1b[0m%s\x1b[90m│\x1b[0m", top+3, left, keysLine)

	fmt.Printf("\x1b[%d;%dH\x1b[90m└%s┘\x1b[0m", top+4, left, strings.Repeat("─", inner))
}
