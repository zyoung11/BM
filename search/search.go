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
	bkTree     *bkNode
	termDF     map[string]int
	totalDocs  int
	avgDocLen  float64
	tokenCache map[string][]string
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
	e.tokenCache = make(map[string][]string, len(paths))
	e.totalDocs = len(paths)
	totalLen := 0

	termSet := make(map[string]bool)

	for _, path := range paths {
		name := filepath.Base(path)
		tokens := tokenize(name)
		e.tokenCache[path] = tokenize(path)
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

// PreparedQuery holds a search query split into tokens once, with the fuzzy
// correction candidates resolved once per token. Scoring many targets then
// repeats neither tokenization nor tree search.
//
// PreparedQuery 保存一次拆分完成的搜索查询，每个词的模糊纠错候选也只解析一次。
// 对大量目标评分时不再重复分词和树搜索。
type PreparedQuery struct {
	tokens  []string
	runeLen int
	fuzzy   []map[string]bool
}

// Prepare splits the query into tokens and resolves the fuzzy candidates of
// each token against the index.
//
// Prepare 把查询拆成词，并针对索引解析每个词的模糊候选。
func (e *Engine) Prepare(query string) *PreparedQuery {
	tokens := tokenize(query)
	q := &PreparedQuery{tokens: tokens, runeLen: utf8.RuneCountInString(query)}
	if e.bkTree == nil || len(tokens) == 0 {
		return q
	}
	q.fuzzy = make([]map[string]bool, len(tokens))
	for i, token := range tokens {
		set := make(map[string]bool)
		for _, candidate := range e.bkTree.search(token, 2) {
			if candidate != token {
				set[candidate] = true
			}
		}
		q.fuzzy[i] = set
	}
	return q
}

// cachedTokens returns the tokenized target, computing and caching it on
// first use.
//
// cachedTokens 返回目标的分词结果，首次使用时计算并缓存。
func (e *Engine) cachedTokens(target string) []string {
	if tokens, ok := e.tokenCache[target]; ok {
		return tokens
	}
	tokens := tokenize(target)
	if e.tokenCache == nil {
		e.tokenCache = make(map[string][]string)
	}
	e.tokenCache[target] = tokens
	return tokens
}

// ScorePrepared scores one target against a prepared query. At least one
// token must match. Multi-token queries get a coverage bonus.
//
// ScorePrepared 对单个目标按预备好的查询评分。至少需要一个词匹配。
// 多词查询获得覆盖度奖励。
func (e *Engine) ScorePrepared(q *PreparedQuery, target string) float64 {
	if len(q.tokens) == 0 {
		return 1.0
	}
	targetTokens := e.cachedTokens(target)
	docLen := len(targetTokens)

	totalScore := 0.0
	matchedCount := 0
	for i, qt := range q.tokens {
		score := e.scoreToken(q, i, qt, target, targetTokens, docLen)
		if score > 0 {
			totalScore += score
			matchedCount++
		}
	}
	if matchedCount == 0 {
		return 0
	}
	coverage := float64(matchedCount) / float64(len(q.tokens))
	return totalScore * (0.6 + 0.4*coverage)
}

// scoreToken scores one query token through the matcher stages: exact token,
// token prefix, substring of the base name, sequential character match and
// fuzzy correction. The fuzzy stage picks the best matching candidate.
//
// scoreToken 按级联为单个查询词评分：精确匹配、词前缀、基础名子串、
// 字符顺序匹配，最后是模糊纠错。模糊级取匹配候选中的最高分。
func (e *Engine) scoreToken(q *PreparedQuery, idx int, queryToken, target string, targetTokens []string, docLen int) float64 {
	if slices.Contains(targetTokens, queryToken) {
		return e.bm25Score(queryToken, countIn(queryToken, targetTokens), docLen)
	}
	for _, tt := range targetTokens {
		if strings.HasPrefix(tt, queryToken) {
			return e.bm25Score(queryToken, 1, docLen) * 0.9
		}
	}
	baseName := filepath.Base(target)
	if strings.Contains(strings.ToLower(baseName), queryToken) {
		return e.bm25Score(queryToken, 1, docLen) * 0.6
	}
	seqWeight := 0.7
	if q.runeLen <= 3 {
		seqWeight = 0.8
	}
	if seqMatch(queryToken, baseName) {
		return e.bm25Score(queryToken, 1, docLen) * seqWeight
	}
	if q.fuzzy == nil {
		return 0
	}
	best := 0.0
	for _, tt := range targetTokens {
		if tt == queryToken || !q.fuzzy[idx][tt] {
			continue
		}
		if score := e.bm25Score(tt, 1, docLen) * 0.5; score > best {
			best = score
		}
	}
	return best
}

// Match scores a target string against a query. It prepares the query for a
// single call; loops over many targets should call Prepare once and then
// ScorePrepared per target.
//
// Match 对目标字符串按查询评分，为单次调用预备查询。
// 对大量目标的循环应只调用一次 Prepare，再对每个目标调用 ScorePrepared。
func (e *Engine) Match(query, target string) float64 {
	return e.ScorePrepared(e.Prepare(query), target)
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
