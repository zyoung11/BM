package search

import (
	"math"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Engine provides tokenized search with BM25 scoring and BK-tree fuzzy correction.
//
// Engine 提供基于分词的搜索，使用 BM25 评分和 BK-tree 模糊纠错。
type Engine struct {
	bkTree    *bkNode
	termDF    map[string]int
	totalDocs int
	avgDocLen float64
}

// New creates a new search engine.
//
// New 创建一个新的搜索引擎。
func New() *Engine {
	return &Engine{}
}

// BuildFromPaths analyzes file paths to build BK-tree and IDF statistics.
// The index is built from basenames so fuzzy corrections match file/directory names.
//
// BuildFromPaths 分析文件路径以构建 BK-tree 和 IDF 统计信息。
// 索引基于文件名构建，使模糊纠错能匹配文件/目录名。
func (e *Engine) BuildFromPaths(paths []string) {
	e.termDF = make(map[string]int)
	e.totalDocs = len(paths)
	totalLen := 0

	termSet := make(map[string]bool)

	for _, path := range paths {
		name := filepath.Base(path)
		tokens := tokenize(name)
		totalLen += len(tokens)
		seen := make(map[string]bool)
		for _, t := range tokens {
			termSet[t] = true
			if !seen[t] {
				e.termDF[t]++
				seen[t] = true
			}
		}
	}

	if e.totalDocs > 0 {
		e.avgDocLen = float64(totalLen) / float64(e.totalDocs)
	} else {
		e.avgDocLen = 1
	}

	e.bkTree = nil
	for term := range termSet {
		if e.bkTree == nil {
			e.bkTree = &bkNode{term: term}
		} else {
			e.bkTree.insert(term)
		}
	}
}

// Match scores a target string against a query.
// At least one token must match. Multi-token queries get a coverage bonus.
// Short queries (≤3 runes total) are handled by sequential character matching as the primary path.
//
// Match 对目标字符串按查询评分。至少需要一个 token 匹配。
// 多词查询获得覆盖度奖励。短查询（≤3个字符）以字符级顺序匹配为主路径。
func (e *Engine) Match(query, target string) float64 {
	queryTokens := tokenize(query)
	if len(queryTokens) == 0 {
		return 1.0
	}

	targetTokens := tokenize(target)
	docLen := len(targetTokens)

	queryRuneLen := utf8.RuneCountInString(query)

	totalScore := 0.0
	matchedCount := 0
	for _, qt := range queryTokens {
		bestScore := e.matchToken(qt, target, targetTokens, docLen, queryRuneLen)
		if bestScore > 0 {
			totalScore += bestScore
			matchedCount++
		}
	}

	if matchedCount == 0 {
		return 0
	}

	coverage := float64(matchedCount) / float64(len(queryTokens))
	return totalScore * (0.6 + 0.4*coverage)
}

func (e *Engine) matchToken(queryToken, target string, targetTokens []string, docLen int, queryRuneLen int) float64 {
	baseName := filepath.Base(target)
	baseLower := strings.ToLower(baseName)

	if slices.Contains(targetTokens, queryToken) {
		tf := countIn(queryToken, targetTokens)
		return e.bm25Score(queryToken, tf, docLen)
	}

	for _, tt := range targetTokens {
		if strings.HasPrefix(tt, queryToken) {
			return e.bm25Score(queryToken, 1, docLen) * 0.9
		}
	}

	if strings.Contains(baseLower, queryToken) {
		return e.bm25Score(queryToken, 1, docLen) * 0.6
	}

	seqWeight := 0.7
	if queryRuneLen <= 3 {
		seqWeight = 0.8
	}
	if seqMatch(queryToken, baseName) {
		return e.bm25Score(queryToken, 1, docLen) * seqWeight
	}

	if e.bkTree != nil {
		candidates := e.bkTree.search(queryToken, 2)
		for _, c := range candidates {
			if c == queryToken {
				continue
			}
			if slices.Contains(targetTokens, c) {
				return e.bm25Score(c, 1, docLen) * 0.5
			}
		}
	}

	return 0
}

func (e *Engine) bm25Score(term string, tf, docLen int) float64 {
	const k1 = 1.2
	const b = 0.75

	df := e.termDF[term]
	if df == 0 {
		df = 1
	}

	idfVal := math.Log((float64(e.totalDocs)-float64(df)+0.5)/(float64(df)+0.5) + 1)

	tfD := float64(tf)
	dl := float64(docLen)
	numerator := tfD * (k1 + 1)
	denominator := tfD + k1*(1-b+b*dl/e.avgDocLen)

	return idfVal * numerator / denominator
}

func countIn(term string, tokens []string) int {
	n := 0
	for _, t := range tokens {
		if t == term {
			n++
		}
	}
	return n
}

func seqMatch(query, target string) bool {
	queryRunes := []rune(query)
	targetRunes := []rune(target)
	qi := 0
	for _, tr := range targetRunes {
		if unicodeFold(tr) == unicodeFold(queryRunes[qi]) {
			qi++
			if qi == len(queryRunes) {
				return true
			}
		}
	}
	return false
}

func unicodeFold(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return unicode.ToLower(r)
}

func tokenize(text string) []string {
	text = strings.ToLower(text)

	replacer := strings.NewReplacer(
		"/", " ", "\\", " ", "-", " ", "_", " ", ".", " ",
		"(", " ", ")", " ", "[", " ", "]", " ",
		"&", " ", ",", " ", "!", " ", "?", " ",
		"'", "", "\"", "",
	)
	text = replacer.Replace(text)

	var tokens []string
	for raw := range strings.FieldsSeq(text) {
		var current []rune
		for _, r := range raw {
			if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
				if len(current) > 0 {
					tokens = append(tokens, string(current))
					current = current[:0]
				}
				tokens = append(tokens, string(r))
			} else {
				current = append(current, r)
			}
		}
		if len(current) > 0 {
			tokens = append(tokens, string(current))
		}
	}

	result := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if len(t) > 0 {
			result = append(result, t)
		}
	}
	return result
}
