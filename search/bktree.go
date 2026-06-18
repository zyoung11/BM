package search

import "unicode/utf8"

type bkNode struct {
	term     string
	children map[int]*bkNode
}

func editDistance(a, b string) int {
	ar := make([]rune, 0, utf8.RuneCountInString(a))
	br := make([]rune, 0, utf8.RuneCountInString(b))
	for _, r := range a {
		ar = append(ar, r)
	}
	for _, r := range b {
		br = append(br, r)
	}
	la, lb := len(ar), len(br)
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
		dp[i][0] = i
	}
	for j := range dp[0] {
		dp[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			dp[i][j] = minInt(minInt(dp[i-1][j]+1, dp[i][j-1]+1), dp[i-1][j-1]+cost)
		}
	}
	return dp[la][lb]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (n *bkNode) insert(term string) {
	d := editDistance(n.term, term)
	if d == 0 {
		return
	}
	if n.children == nil {
		n.children = make(map[int]*bkNode)
	}
	if child, ok := n.children[d]; ok {
		child.insert(term)
	} else {
		n.children[d] = &bkNode{term: term}
	}
}

func (n *bkNode) search(query string, maxDist int) []string {
	d := editDistance(query, n.term)
	var results []string
	if d <= maxDist {
		results = append(results, n.term)
	}
	for dist, child := range n.children {
		if dist >= d-maxDist && dist <= d+maxDist {
			results = append(results, child.search(query, maxDist)...)
		}
	}
	return results
}
