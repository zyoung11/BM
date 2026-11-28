package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/mattn/go-runewidth"
)

// libraryColumn stores one column of the multi-column browser: either a
// directory listing or the search results that acted as its root.
//
// libraryColumn 保存多列浏览中的一列，既可以是目录列表，
// 也可以是作为根列的搜索结果。
type libraryColumn struct {
	path     string
	entries  []LibraryEntry
	items    []string
	dirCount int
	cursor   int
	offset   int
	isSearch bool
}

// columnGeometry describes where a single column is drawn on screen.
//
// columnGeometry 描述单列在屏幕上的绘制位置。
type columnGeometry struct {
	x          int
	width      int
	col        libraryColumn
	nameWidths nameWidths
}

// readLibraryEntries lists the browsable entries of a directory with
// directories first, matching what the browser shows.
//
// readLibraryEntries 列出目录中可浏览的条目，目录在前，与浏览视图一致。
func readLibraryEntries(path string) []LibraryEntry {
	entries := make([]LibraryEntry, 0)
	files, err := os.ReadDir(path)
	if err != nil {
		return entries
	}
	for _, file := range files {
		info, err := file.Info()
		if err != nil {
			continue
		}

		isDir := info.IsDir()
		isLink := info.Mode()&os.ModeSymlink != 0
		isValidAudio := isAudioFile(info.Name())

		if isLink {
			targetInfo, statErr := os.Stat(filepath.Join(path, file.Name()))
			if statErr != nil {
				continue
			}
			isDir = targetInfo.IsDir()
			isValidAudio = isAudioFile(targetInfo.Name())
		}

		if isDir || isValidAudio {
			entries = append(entries, LibraryEntry{entry: file, info: info, isDir: isDir})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].isDir != entries[j].isDir {
			return entries[i].isDir
		}
		return strings.ToLower(entries[i].entry.Name()) < strings.ToLower(entries[j].entry.Name())
	})
	return entries
}

// currentColumn snapshots the column the user is currently on.
//
// currentColumn 快照用户当前所在的列。
func (p *Library) currentColumn() libraryColumn {
	col := libraryColumn{path: p.currentPath}
	if p.searchQuery != "" {
		col.isSearch = true
		col.items = p.filteredSongPaths
		col.dirCount = p.searchDirCount
		col.cursor = p.searchCursor
		col.offset = p.searchOffset
	} else {
		col.entries = p.entries
		col.cursor = p.cursor
		col.offset = p.offset
	}
	return col
}

// pushColumn saves the current column before drilling into a subdirectory.
//
// pushColumn 在进入子目录前保存当前列。
func (p *Library) pushColumn() {
	p.columns = append(p.columns, p.currentColumn())
}

// popColumn restores the most recently saved column after stepping out,
// remembering where the left column was browsed to.
//
// popColumn 在退出一层后恢复最近保存的列，并记住离开列的浏览位置。
func (p *Library) popColumn() bool {
	if len(p.columns) == 0 {
		return false
	}
	p.pathHistory[p.currentPath] = browsePosition{cursor: p.cursor, offset: p.offset}
	col := p.columns[len(p.columns)-1]
	p.columns = p.columns[:len(p.columns)-1]
	p.currentPath = col.path
	p.entries = col.entries
	p.cursor = col.cursor
	p.offset = col.offset
	return true
}

// viewColumns assembles the navigation columns to draw: the search results
// act as the root when a search is active, earlier columns stay saved but
// hidden so leaving the search restores the browsing stack.
//
// viewColumns 组装待绘制的导航列：搜索激活时搜索结果作为根列，
// 更早的列保留但隐藏，退出搜索后恢复浏览列栈。
func (p *Library) viewColumns() []libraryColumn {
	cols := make([]libraryColumn, 0, len(p.columns)+1)
	cols = append(cols, p.columns...)
	cols = append(cols, p.currentColumn())
	start := 0
	for i, col := range cols {
		if col.isSearch {
			start = i
		}
	}
	return cols[start:]
}

// entryPreviewPath returns the directory path whose preview the entry shows,
// or an empty string when the entry previews nothing.
//
// entryPreviewPath 返回条目展示预览的目录路径，无预览时返回空字符串。
func (p *Library) entryPreviewPath(idx int) string {
	if p.searchQuery != "" {
		if idx < 0 || idx >= len(p.filteredSongPaths) {
			return ""
		}
		info, err := os.Stat(p.filteredSongPaths[idx])
		if err != nil || !info.IsDir() {
			return ""
		}
		return p.filteredSongPaths[idx]
	}
	if idx < 0 || idx >= len(p.entries) || !p.entries[idx].isDir {
		return ""
	}
	return filepath.Join(p.currentPath, p.entries[idx].entry.Name())
}

// previewColumn returns the preview of the directory under the cursor, or nil
// when the cursor is not on a directory. The preview lists one level only and
// keeps the cursor out of it.
//
// previewColumn 返回光标所在目录的预览，光标不在目录上时返回 nil。
// 预览只列出一层内容，光标不会进入预览列。
func (p *Library) previewColumn(listHeight int) *libraryColumn {
	idx := p.cursor
	if p.searchQuery != "" {
		idx = p.searchCursor
	}
	path := p.entryPreviewPath(idx)
	if path == "" {
		return nil
	}
	if p.previewEntriesCache == nil {
		p.previewEntriesCache = make(map[string][]LibraryEntry)
	}
	entries, ok := p.previewEntriesCache[path]
	if !ok {
		entries = readLibraryEntries(path)
		p.sortEntries(path, entries)
		p.previewEntriesCache[path] = entries
	}
	saved := p.pathHistory[path]
	cursor := min(saved.cursor, max(len(entries)-1, 0))
	offset := min(saved.offset, cursor)
	if cursor >= offset+listHeight {
		offset = cursor - listHeight + 1
	}
	return &libraryColumn{path: path, entries: entries, cursor: -1, offset: offset}
}

// truncateWithEllipsis shortens text to the given display width and marks the
// cut with an ellipsis at the tail.
//
// truncateWithEllipsis 将文本截断到指定显示宽度，并在尾部以省略号标记截断。
func truncateWithEllipsis(text string, width int) string {
	if width <= 0 {
		return ""
	}
	return truncateToWidthFromStart(text, width)
}

// columnContentWidth measures the display width a column needs to show all of
// its entry names without cutting any of them. Search results size on the
// folder entries only, so long song names below the separator are shortened
// instead of pushing the preview column to the far right. The cursor padding
// is carved out of the column at draw time so the width never depends on where
// the cursor sits and the next column slot stays put.
//
// columnContentWidth 测量列完整显示所有条目名所需的显示宽度，不截断任何名字。
// 搜索列仅按文件夹条目计算宽度，分隔线下的长歌名在列内省略，
// 避免把预览列推到最右侧。光标行的对称填充在绘制时从列宽内扣除，
// 保证列宽与光标位置无关，下一列的位置保持不动。
func (p *Library) columnContentWidth(col libraryColumn, widths nameWidths) int {
	width := 1
	if col.isSearch {
		for i, item := range col.items {
			row, _ := p.getSearchEntryLine(item, i == col.cursor)
			width = max(width, rowDisplayWidth(row, widths))
		}
	} else {
		for i, libEntry := range col.entries {
			fullPath := filepath.Join(col.path, libEntry.entry.Name())
			row, _ := p.getDirEntryLine(libEntry, fullPath, i == col.cursor)
			width = max(width, rowDisplayWidth(row, widths))
		}
	}
	return width
}

// colNameWidths returns the name alignment widths of one column.
//
// colNameWidths 返回一列的名称对齐宽度。
func (p *Library) colNameWidths(col libraryColumn) nameWidths {
	if col.isSearch {
		return nameDisplayWidths(p.nameDisplayMode, itemBaseNames(col.items))
	}
	return nameDisplayWidths(p.nameDisplayMode, entryNames(col.entries))
}

// planColumns computes the geometry of the visible navigation columns and of
// the preview column. Columns are drawn from the left edge with a fixed gap
// between them and grow to the right only until the current column reaches the
// middle of the screen. From then on the current column stays put and entering
// a deeper folder collapses the leftmost column instead of pushing the current
// column further right. The preview takes the very slot the next navigation
// column would occupy, shows one level of the directory under the cursor and
// is dropped when even a shortened list of names would fall below the
// configured minimum width.
//
// planColumns 计算可见导航列与预览列的几何位置。列以固定间隔从左边缘向右画出，
// 仅生长到当前列抵达屏幕中部为止。此后当前列位置固定，
// 进入更深层文件夹时折叠最左列而不是把当前列继续右推。
// 预览列占据下一个导航列本应出现的位置，显示光标所在目录的一层内容，
// 若缩短后的名字宽度仍低于配置的最小宽度则不显示预览。
func (p *Library) planColumns(w, listHeight int) ([]columnGeometry, *columnGeometry) {
	cols := p.viewColumns()
	minWidth := GlobalConfig.App.MinColumnWidth
	full := make([]int, len(cols))
	pads := make([]nameWidths, len(cols))
	for i, col := range cols {
		pads[i] = p.colNameWidths(col)
		full[i] = p.columnContentWidth(col, pads[i])
	}
	pcol := p.previewColumn(listHeight)
	var pfull int
	var ppad nameWidths
	if pcol != nil {
		ppad = p.colNameWidths(*pcol)
		pfull = p.columnContentWidth(*pcol, ppad)
	}

	avail := w - 1
	mid := w / 2
	last := len(cols) - 1
	for i, col := range cols {
		if col.isSearch && i != last {
			full[i] = p.searchFolderWidth(col, pads[i])
		}
	}

	parentBudget := 0
	if last > 0 {
		parentBudget = full[last-1] + 1
	}
	curWidth := min(full[last], avail-parentBudget)
	floor := min(full[last], minWidth)
	onlyCurrent := false
	if curWidth < floor {
		onlyCurrent = true
		curWidth = min(full[last], avail)
	}

	placed := make([]int, 0, len(cols))
	widths := make(map[int]int)
	widths[last] = max(curWidth, 1)
	placed = append(placed, last)

	midRemain := mid - widths[last]
	if !onlyCurrent && last > 0 {
		i := last - 1
		widths[i] = full[i]
		placed = append(placed, i)
		midRemain -= full[i] + 1
	}

	for i := last - 2; i >= 0 && !onlyCurrent; i-- {
		if full[i] <= midRemain {
			widths[i] = full[i]
			placed = append(placed, i)
			midRemain -= full[i] + 1
		} else {
			break
		}
	}

	folded := len(placed) < len(cols)
	total := 0
	for _, i := range placed {
		total += widths[i] + 1
	}
	total--
	x := 1
	if folded {
		x = max(mid-total+1, 1)
	}
	geoms := make([]columnGeometry, 0, len(placed))
	for _, i := range slices.Backward(placed) {
		geoms = append(geoms, columnGeometry{x: x, width: widths[i], col: cols[i], nameWidths: pads[i]})
		x += widths[i] + 1
	}

	var preview *columnGeometry
	if pcol != nil {
		lastGeom := geoms[len(geoms)-1]
		previewX := p.previewAnchorX(lastGeom)
		previewAvail := avail - (previewX - 1)
		previewWidth := 0
		switch {
		case pfull <= previewAvail:
			previewWidth = pfull
		case previewAvail >= minWidth:
			previewWidth = previewAvail
		}
		if previewWidth > 0 {
			preview = &columnGeometry{x: previewX, width: previewWidth, col: *pcol, nameWidths: ppad}
		}
	}
	return geoms, preview
}

// searchFolderWidth measures the folder section above the separator line of a
// search column. The preview column anchors to this width so long song names
// below the line never push it around.
//
// searchFolderWidth 测量搜索列分隔线上方文件夹区的宽度。
// 预览列以此定位，横线下的长歌名不会推动预览位置。
func (p *Library) searchFolderWidth(col libraryColumn, widths nameWidths) int {
	width := 1
	for i, item := range col.items {
		if col.dirCount > 0 && i >= col.dirCount {
			break
		}
		row, _ := p.getSearchEntryLine(item, i == col.cursor)
		width = max(width, rowDisplayWidth(row, widths))
	}
	return width
}

// clearColumnCell blanks one cell inside a column.
//
// clearColumnCell 清空列内单元。
func clearColumnCell(x, width, y int) string {
	return fmt.Sprintf("\x1b[%d;%dH%s", y, x, strings.Repeat(" ", width))
}

// cursorRowPad is the number of columns the cursor row extends past the name
// on the right. It matches the icon column on the left, so the highlight stays
// symmetric around every name and looks the same on selected and unselected
// rows.
//
// cursorRowPad 是光标行在歌名右侧延伸的列数，与左侧图标列等宽，
// 使高亮围绕每个名字保持对称，选中与未选中的行观感一致。
const cursorRowPad = 2

// rowText is the display text of one list row before layout. The aligned name
// display modes split the song name into first and second; every other mode
// keeps the whole line in first and leaves aligned unset.
//
// rowText 是一行列表显示文本布局前的结构。对齐显示模式把歌曲名拆成 first 与
// second；其他模式整行放 first，aligned 留空。
type rowText struct {
	prefix  string
	first   string
	second  string
	aligned bool
}

// rowDisplayWidth measures the display width one row needs without any
// cutting. Aligned rows measure the padded first field plus the separator and
// the second field.
//
// rowDisplayWidth 测量一行不截断所需的显示宽度。对齐行按填充后的第一字段加
// 分隔符加第二字段测量。
func rowDisplayWidth(row rowText, widths nameWidths) int {
	if row.aligned {
		return runewidth.StringWidth(row.prefix) + widths.first + 3 + runewidth.StringWidth(row.second)
	}
	return runewidth.StringWidth(row.prefix + row.first)
}

// alignMinColumns is the smallest column width where the aligned name display
// keeps its two-field layout. Narrower columns fall back to whole-line
// drawing so the fields do not collapse into ellipses.
//
// alignMinColumns 是对齐显示保持双字段布局的最小列宽。更窄的列回退到整行
// 绘制，避免字段塌成省略号。
const alignMinColumns = 10

// drawAlignedColumnCell draws one aligned row: the first field fills the space
// before the fixed separator column and the second field fills the rest of the
// column, both cut with a tail ellipsis. The separator is placed by cursor
// escape so unusual glyph widths in a name can never move it.
//
// drawAlignedColumnCell 绘制一行对齐内容：第一字段占固定分隔符列之前的空间，
// 第二字段占列内剩余空间，两者都以尾部省略号截断。分隔符用光标转义定位，
// 名称字形宽度异常也不会移动它。
func drawAlignedColumnCell(x, width, y int, row rowText, style string, isCursor bool, widths nameWidths) string {
	prefixW := runewidth.StringWidth(row.prefix)
	sepCol := alignSeparatorCol(widths, prefixW, width)
	head := row.prefix + truncateWithEllipsis(row.first, sepCol-prefixW-1)
	tail := truncateWithEllipsis(row.second, width-sepCol-2)
	var buf strings.Builder
	fmt.Fprintf(&buf, "\x1b[%d;%dH%s%s", y, x, style, head)
	if gap := sepCol - runewidth.StringWidth(head); gap > 0 {
		buf.WriteString(strings.Repeat(" ", gap))
	}
	fmt.Fprintf(&buf, "\x1b[%d;%dH- %s", y, x+sepCol, tail)
	used := sepCol + 2 + runewidth.StringWidth(tail)
	end := width
	if isCursor {
		end = min(width, used+cursorRowPad)
	}
	buf.WriteString(strings.Repeat(" ", max(end-used, 0)))
	buf.WriteString("\x1b[0m")
	buf.WriteString(strings.Repeat(" ", max(width-end, 0)))
	return buf.String()
}

// drawColumnCell draws one cell inside a column, padding it with spaces so no
// stale content survives without touching neighbouring columns. Aligned rows
// draw the separator at the fixed column and cut both name fields with a tail
// ellipsis; other rows keep whole-line drawing. The cursor highlight extends
// cursorRowPad columns past the name, capped at the column width.
//
// drawColumnCell 绘制列内单元，用空格补齐避免残留旧内容且不碰相邻列。
// 对齐行把分隔符画在固定列，两个名称字段尾部省略号截断；
// 其他行保持整行绘制。光标高亮在名字右侧延伸 cursorRowPad 列，不超过列宽。
func drawColumnCell(x, width, y int, row rowText, style string, isCursor, dim bool, widths nameWidths) string {
	if dim {
		style += "\x1b[2m"
	}
	if row.aligned && width >= alignMinColumns {
		return drawAlignedColumnCell(x, width, y, row, style, isCursor, widths)
	}
	line := truncateWithEllipsis(row.prefix+row.first, width)
	if isCursor {
		line += strings.Repeat(" ", min(cursorRowPad, width-runewidth.StringWidth(line)))
	}
	padRight := max(width-runewidth.StringWidth(line), 0)
	return fmt.Sprintf("\x1b[%d;%dH%s%s\x1b[0m%s", y, x, style, line, strings.Repeat(" ", padRight))
}

// ancestorOffset recenters an ancestor column on its remembered cursor entry.
//
// ancestorOffset 将祖先列重新居中到其记忆的光标条目。
func ancestorOffset(col libraryColumn, listHeight int) int {
	count := len(col.entries)
	if col.isSearch {
		count = len(col.items)
	}
	if count <= listHeight {
		return 0
	}
	return min(max(col.cursor-listHeight/2, 0), count-listHeight)
}

// searchSepRow returns the screen row of the separator inside a search column,
// or -1 when it is not visible.
//
// searchSepRow 返回搜索列分隔线所在的屏幕行，不可见时返回 -1。
func searchSepRow(col libraryColumn, listHeight int) int {
	if !col.isSearch || col.dirCount <= 0 || col.dirCount >= len(col.items) {
		return -1
	}
	visualOffset := col.offset
	if col.offset >= col.dirCount {
		visualOffset = col.offset + 1
	}
	row := col.dirCount - visualOffset
	if row < 0 || row >= listHeight {
		return -1
	}
	return row
}

// renderColumns draws the multi-column browser: the current column sits at the
// right end of the navigation columns, ancestors to its left and are dimmed,
// and a dimmed preview of the directory under the cursor may sit further
// right. Each row is erased before its cells are drawn so no stale column
// survives a layout change.
//
// renderColumns 绘制多列浏览器，当前列位于导航列最右，祖先列居左并淡化，
// 光标所在目录的弱化预览列可再居其右。每行先擦除再绘制单元，
// 保证布局变化后不留旧列残影。
func (p *Library) renderColumns(w, listHeight int) string {
	geoms, preview := p.planColumns(w, listHeight)
	last := len(geoms) - 1
	for i, geom := range geoms {
		if i != last {
			geom.col.offset = ancestorOffset(geom.col, listHeight)
		}
	}
	sepRow := -1
	for _, geom := range geoms {
		if geom.col.isSearch {
			sepRow = searchSepRow(geom.col, listHeight)
		}
	}

	var buf strings.Builder
	for row := range listHeight {
		y := row + 3
		fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[K", y)
		if row == sepRow {
			sepX := 1
			lineEnd := w - 1
			for _, geom := range geoms {
				if geom.col.isSearch {
					sepX = geom.x
				} else if geom.x > sepX {
					lineEnd = min(lineEnd, geom.x-1)
				}
			}
			fmt.Fprintf(&buf, "\x1b[%d;%dH\x1b[90m%s\x1b[0m", y, sepX, strings.Repeat("─", max(lineEnd-sepX+1, 1)))
		}
		for i, geom := range geoms {
			if row == sepRow && geom.col.isSearch {
				continue
			}
			buf.WriteString(p.drawColumnRow(geom, row, i != last))
		}
		if preview != nil && previewRow(geoms[last].col, row) {
			buf.WriteString(p.drawColumnRow(*preview, row, true))
		}
	}
	return buf.String()
}

// drawColumnRow draws one visible row of a column.
//
// drawColumnRow 绘制列的一个可见行。
func (p *Library) drawColumnRow(geom columnGeometry, row int, dim bool) string {
	col := geom.col
	y := row + 3
	if col.isSearch {
		hasSep := col.dirCount > 0 && col.dirCount < len(col.items)
		visualOffset := col.offset
		if hasSep && col.offset >= col.dirCount {
			visualOffset = col.offset + 1
		}
		visualRow := visualOffset + row
		if hasSep && visualRow == col.dirCount {
			sepStyle := "\x1b[90m"
			if dim {
				sepStyle += "\x1b[2m"
			}
			return fmt.Sprintf("\x1b[%d;%dH%s%s\x1b[0m", y, geom.x, sepStyle, strings.Repeat("─", geom.width))
		}
		itemIdx := visualRow
		if hasSep && visualRow > col.dirCount {
			itemIdx = visualRow - 1
		}
		if itemIdx < 0 || itemIdx >= len(col.items) {
			return ""
		}
		line, style := p.getSearchEntryLine(col.items[itemIdx], itemIdx == col.cursor)
		return drawColumnCell(geom.x, geom.width, y, line, style, itemIdx == col.cursor, dim, geom.nameWidths)
	}
	idx := col.offset + row
	if idx < 0 || idx >= len(col.entries) {
		return ""
	}
	fullPath := filepath.Join(col.path, col.entries[idx].entry.Name())
	line, style := p.getDirEntryLine(col.entries[idx], fullPath, idx == col.cursor)
	return drawColumnCell(geom.x, geom.width, y, line, style, idx == col.cursor, dim, geom.nameWidths)
}

// previewRow reports whether one screen row of the given navigation column
// shows folder entries, which is the only place the preview column may draw.
// Song rows below the separator keep their names clear. The separator can
// scroll out of the view, so the row kind is resolved from the visual row
// index instead of the visible separator position.
//
// previewRow 判断给定导航列的某个屏幕行是否显示文件夹条目，预览列只画在这些行。
// 分隔线以下的歌曲行保持歌名不被覆盖。分隔线可能滚出视野，
// 因此按视觉行号判断行的类型，不依赖分隔线的可见位置。
func previewRow(col libraryColumn, row int) bool {
	if !col.isSearch {
		return true
	}
	if col.dirCount <= 0 {
		return false
	}
	if col.dirCount >= len(col.items) {
		return true
	}
	visualOffset := col.offset
	if col.offset >= col.dirCount {
		visualOffset++
	}
	return visualOffset+row < col.dirCount
}

// previewAnchorX returns the screen column where the preview column starts.
// The preview of a search column anchors to the folder section width so long
// song names below the separator never push it around.
//
// previewAnchorX 返回预览列起始的屏幕列。搜索列的预览锚定在文件夹区宽度处，
// 分隔线下的长歌名不会推动它。
func (p *Library) previewAnchorX(geom columnGeometry) int {
	if geom.col.isSearch {
		return geom.x + p.searchFolderWidth(geom.col, geom.nameWidths) + 1
	}
	return geom.x + geom.width + 1
}

// redrawPreviewArea repaints the preview strip after a cursor move, leaving
// the navigation columns untouched. Above the separator the repaint starts at
// the preview anchor so a narrower preview never leaves stale cells behind;
// below the separator it starts past the search column so song names stay
// untouched.
//
// redrawPreviewArea 在光标移动后仅重绘预览区，不碰导航列。分隔线以上从预览
// 锚点开始重绘，较窄的预览不留残影；分隔线以下从搜索列右侧开始，不碰歌名。
func (p *Library) redrawPreviewArea(w, listHeight int) string {
	geoms, preview := p.planColumns(w, listHeight)
	if len(geoms) == 0 {
		return ""
	}
	lastGeom := geoms[len(geoms)-1]
	startX := lastGeom.x + lastGeom.width + 1
	previewX := p.previewAnchorX(lastGeom)
	sepRow := -1
	for _, geom := range geoms {
		if geom.col.isSearch {
			sepRow = searchSepRow(geom.col, listHeight)
		}
	}
	var buf strings.Builder
	for row := range listHeight {
		if row == sepRow {
			continue
		}
		clearX := startX
		if previewRow(lastGeom.col, row) {
			clearX = previewX
		}
		fmt.Fprintf(&buf, "\x1b[%d;%dH%s", row+3, clearX, strings.Repeat(" ", max(w-clearX, 0)))
	}
	if preview != nil {
		for row := range listHeight {
			if !previewRow(lastGeom.col, row) {
				continue
			}
			buf.WriteString(p.drawColumnRow(*preview, row, true))
		}
	}
	return buf.String()
}

// redrawColumnRow repaints a single entry row inside the current column.
//
// redrawColumnRow 重绘当前列内的单行条目。
func (p *Library) redrawColumnRow(geom columnGeometry, entryIdx int) string {
	col := geom.col
	y := entryIdx - col.offset + 3
	if entryIdx < 0 || entryIdx >= len(col.entries) {
		return clearColumnCell(geom.x, geom.width, y)
	}
	fullPath := filepath.Join(col.path, col.entries[entryIdx].entry.Name())
	line, style := p.getDirEntryLine(col.entries[entryIdx], fullPath, entryIdx == col.cursor)
	return drawColumnCell(geom.x, geom.width, y, line, style, entryIdx == col.cursor, false, geom.nameWidths)
}

// redrawSearchRow repaints a single search result row inside the current
// column, keeping the separator row in account.
//
// redrawSearchRow 重绘当前列内的单行搜索结果，并考虑分隔线占用的行。
func (p *Library) redrawSearchRow(geom columnGeometry, itemIdx int) string {
	col := geom.col
	hasSep := col.dirCount > 0 && col.dirCount < len(col.items)
	visualOffset := col.offset
	if hasSep && col.offset >= col.dirCount {
		visualOffset++
	}
	visualRow := itemIdx
	if hasSep && itemIdx >= col.dirCount {
		visualRow = itemIdx + 1
	}
	y := visualRow - visualOffset + 3
	if itemIdx < 0 || itemIdx >= len(col.items) {
		return clearColumnCell(geom.x, geom.width, y)
	}
	line, style := p.getSearchEntryLine(col.items[itemIdx], itemIdx == col.cursor)
	return drawColumnCell(geom.x, geom.width, y, line, style, itemIdx == col.cursor, false, geom.nameWidths)
}

// getSearchEntryLine generates the display row and style for one search result.
//
// getSearchEntryLine 为单条搜索结果生成显示行和样式。
func (p *Library) getSearchEntryLine(fullPath string, isCursor bool) (rowText, string) {
	cleanInitial := filepath.Clean(p.initialPath)

	isDir := p.globalDirSet[fullPath]

	row := rowText{}
	if isDir {
		cleanPath := filepath.Clean(fullPath)
		displayPath := cleanPath
		if rel, relErr := filepath.Rel(cleanInitial, cleanPath); relErr == nil && !strings.HasPrefix(rel, "..") {
			displayPath = rel
		}
		if displayPath == "." {
			displayPath = filepath.Base(cleanInitial)
			if displayPath == "." {
				displayPath = "(root)"
			}
		}
		row.first = displayPath + "/"
	} else {
		row.first, row.second, row.aligned = nameDisplayRow(filepath.Base(fullPath), p.nameDisplayMode)
	}

	isSelected := p.selected[fullPath]
	if isDir && !isSelected {
		if cached, ok := p.dirSelectionCache[fullPath]; ok {
			isSelected = cached
		} else {
			isSelected = p.dirHasSelection(fullPath)
			p.dirSelectionCache[fullPath] = isSelected
		}
	}

	style := "\x1b[0m"
	switch {
	case isSelected:
		style += "\x1b[32m"
		row.prefix = "✓ "
	case isDir:
		row.prefix = "▸ "
	default:
		row.prefix = "  "
	}

	if isCursor {
		style += "\x1b[7m"
	}
	return row, style
}

// dirHasSelection reports whether any song under the directory is selected.
//
// dirHasSelection 报告目录下是否有任意歌曲被选中。
func (p *Library) dirHasSelection(dirPath string) bool {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return false
	}
	for _, file := range files {
		entryPath := filepath.Join(dirPath, file.Name())
		info, err := file.Info()
		if err != nil {
			continue
		}

		isDir := info.IsDir()
		if info.Mode()&os.ModeSymlink != 0 {
			statInfo, statErr := os.Stat(entryPath)
			if statErr == nil {
				isDir = statInfo.IsDir()
			} else {
				continue
			}
		}

		if isDir {
			if p.dirHasSelection(entryPath) {
				return true
			}
		} else if p.selected[entryPath] {
			return true
		}
	}
	return false
}
