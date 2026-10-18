package functions

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
)

// PostgreSQL REL_18_0 contrib/unaccent/unaccent.rules, PostgreSQL License.
// https://github.com/postgres/postgres/blob/REL_18_0/contrib/unaccent/unaccent.rules
// Portions Copyright (c) 1996-2025, PostgreSQL Global Development Group.
// Portions Copyright (c) 1994, Regents of the University of California.
// https://www.postgresql.org/about/licence/
//
//go:embed unaccent.rules
var stockUnaccentRules string

// StockUnaccent is the compiled stock unaccent.rules; the engine's installed
// unaccent provider evaluates through it.
var StockUnaccent = CompileUnaccent(stockUnaccentRules)

type unaccentRuleNode struct {
	next        map[byte]*unaccentRuleNode
	replacement string
	terminal    bool
}

// UnaccentMatcher holds immutable compiled rules, not installed dictionary identity.
type UnaccentMatcher struct{ rules *unaccentRuleNode }

// CompileUnaccent compiles PostgreSQL unaccent rules.
func CompileUnaccent(data string) *UnaccentMatcher {
	root := &unaccentRuleNode{}
	for _, line := range strings.Split(data, "\n") {
		if line == "" {
			continue
		}
		source, replacement, _ := strings.Cut(line, "\t")
		if source == "" {
			panic("stuntgres: empty unaccent rule")
		}
		if strings.HasPrefix(replacement, `"`) {
			if len(replacement) < 2 || replacement[len(replacement)-1] != '"' {
				panic("stuntgres: invalid quoted unaccent rule")
			}
			replacement = strings.ReplaceAll(replacement[1:len(replacement)-1], `""`, `"`)
		}
		node := root
		for i := range len(source) {
			if node.next == nil {
				node.next = make(map[byte]*unaccentRuleNode)
			}
			next := node.next[source[i]]
			if next == nil {
				next = &unaccentRuleNode{}
				node.next[source[i]] = next
			}
			node = next
		}
		if node.terminal {
			panic("stuntgres: duplicate unaccent rule")
		}
		node.terminal = true
		node.replacement = replacement
	}
	return &UnaccentMatcher{rules: root}
}

func (p *UnaccentMatcher) init() *unaccentRuleNode { return p.rules }
func (p *UnaccentMatcher) lexize(ctx context.Context, dictionary *unaccentRuleNode, input string) (string, bool, error) {
	if dictionary == nil || dictionary != p.rules {
		return "", false, fmt.Errorf("stuntgres: invalid unaccent dictionary")
	}
	var out strings.Builder
	changed := false
	copied := 0
	nextCheck := 0
	for i := 0; i < len(input); {
		if i >= nextCheck {
			if err := ctx.Err(); err != nil {
				return "", false, err
			}
			nextCheck = i + 256
		}
		node := dictionary
		end := i
		replacement := ""
		for j := i; j < len(input); j++ {
			node = node.next[input[j]]
			if node == nil {
				break
			}
			if node.terminal {
				end = j + 1
				replacement = node.replacement
			}
		}
		if end == i {
			i++
			continue
		}
		if !changed {
			changed = true
			out.Grow(len(input))
		}
		out.WriteString(input[copied:i])
		out.WriteString(replacement)
		copied = end
		i = end
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if !changed {
		return input, false, nil
	}
	out.WriteString(input[copied:])
	return out.String(), true, nil
}

// Text applies the longest matching rule at each byte position.
func (p *UnaccentMatcher) Text(ctx context.Context, input string) (string, error) {
	result, _, err := p.lexize(ctx, p.init(), input)
	return result, err
}
