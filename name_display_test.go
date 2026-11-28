package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestAlignSeparatorColHugsShortSide(t *testing.T) {
	prefixW := 2
	contentW := 60
	cases := []struct {
		name   string
		widths nameWidths
		want   int
	}{
		{"second shorter hugs right", nameWidths{first: 50, second: 10}, 48},
		{"first shorter hugs left", nameWidths{first: 10, second: 50}, 13},
		{"equal splits evenly", nameWidths{first: 20, second: 20}, 30},
		{"both huge fall back to half", nameWidths{first: 50, second: 50}, 30},
		{"second past half keeps first at half", nameWidths{first: 50, second: 40}, 30},
	}
	for _, c := range cases {
		if got := alignSeparatorCol(c.widths, prefixW, contentW); got != c.want {
			t.Fatalf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestNameDisplayRowSplits(t *testing.T) {
	name := "JRND,VMK,Kédo Rebelle - Make Dem (feat. Kédo Rebelle).flac"
	first, second, aligned := nameDisplayRow(name, nameDisplayAlign)
	if !aligned || first != "JRND,VMK,Kédo Rebelle" || second != "Make Dem (feat. Kédo Rebelle).flac" {
		t.Fatalf("align: %q %q %v", first, second, aligned)
	}
	first, second, aligned = nameDisplayRow(name, nameDisplayTitleFirstAlign)
	if !aligned || first != "Make Dem (feat. Kédo Rebelle)" || second != "JRND,VMK,Kédo Rebelle" {
		t.Fatalf("title-first-align: %q %q %v", first, second, aligned)
	}
	first, second, aligned = nameDisplayRow(name, nameDisplayDefault)
	if aligned || second != "" || first != name {
		t.Fatalf("default: %q %q %v", first, second, aligned)
	}
}

func TestAlignedCellSeparatorColumnIsFixed(t *testing.T) {
	names := []string{
		"Gustavo Santaolalla - The Last of Us.flac",
		"JRND,VMK,Kédo Rebelle - Make Dem (feat. Kédo Rebelle).flac",
		"Joywave - Tongues (Feat. KOPPS).flac",
	}
	widths := nameDisplayWidths(nameDisplayAlign, names)
	width := 60
	dashCols := map[int]bool{}
	for _, name := range names {
		first, second, aligned := nameDisplayRow(name, nameDisplayAlign)
		if !aligned {
			t.Fatalf("not aligned: %s", name)
		}
		row := rowText{prefix: "  ", first: first, second: second, aligned: true}
		out := drawAlignedColumnCell(1, width, 3, row, "\x1b[0m", false, widths)
		col := sepColFromOutput(out)
		if col < 0 {
			t.Fatalf("no separator found: %q", out)
		}
		dashCols[col] = true
	}
	if len(dashCols) != 1 {
		t.Fatalf("separator columns differ: %v", dashCols)
	}
}

func sepColFromOutput(out string) int {
	marker := "H- "
	idx := strings.Index(out, marker)
	if idx < 0 {
		return -1
	}
	start := strings.LastIndex(out[:idx], "\x1b[")
	if start < 0 {
		return -1
	}
	var x int
	if _, err := fmt.Sscanf(out[start:idx], "\x1b[3;%d", &x); err != nil {
		return -1
	}
	return x
}
