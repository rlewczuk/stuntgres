package functions

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqlstate"
)

// ThesaurusNormalizer is a captured immutable scalar dictionary, not a resolver.
type ThesaurusNormalizer interface {
	Lexize(context.Context, string) (fts.LexicalResult, error)
}

type thesaurusNode struct {
	next        map[string]*thesaurusNode
	replacement []expr.PhraseLexeme
}

// ThesaurusDictionary retains only compiled resource data and scalar execution.
// Its trie is immutable after successful compilation; cursors are private.
type ThesaurusDictionary struct {
	root       thesaurusNode
	normalizer ThesaurusNormalizer
	resource   string
}

const thesaurusStopword = "\x00"

func CompileThesaurusDictionary(ctx context.Context, source *ThesaurusRules, normalizer ThesaurusNormalizer) (*ThesaurusDictionary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d := &ThesaurusDictionary{normalizer: normalizer, resource: source.resource, root: thesaurusNode{next: make(map[string]*thesaurusNode)}}
	for ordinal, parsed := range source.rules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rule := ordinal + 1
		node := &d.root
		for _, word := range parsed.sample {
			key := thesaurusStopword
			if word != "?" {
				words, err := d.normalize(ctx, word, "sample", rule)
				if err != nil {
					return nil, err
				}
				// Scalar providers expose all alternatives through their typed result. A
				// simple subdictionary has exactly one canonical word per sample token.
				key = thesaurusKey(words)
			}
			if node.next == nil {
				node.next = make(map[string]*thesaurusNode)
			}
			next := node.next[key]
			if next == nil {
				next = &thesaurusNode{}
				node.next[key] = next
			}
			node = next
		}
		replacement := make([]expr.PhraseLexeme, 0, len(parsed.substitute))
		for _, word := range parsed.substitute {
			var words []fts.Lexeme
			if word.literal {
				words = []fts.Lexeme{{Text: word.text}}
			} else {
				var err error
				words, err = d.normalize(ctx, word.text, "substitute", rule)
				if err != nil {
					return nil, err
				}
			}
			for i, normalized := range words {
				replacement = append(replacement, expr.PhraseLexeme{Text: normalized.Text, Prefix: normalized.Prefix, Variant: normalized.Variant, AddPosition: len(replacement) > 0 && i == 0})
			}
		}
		node.replacement = replacement
	}
	return d, ctx.Err()
}

func (d *ThesaurusDictionary) normalize(ctx context.Context, word, side string, rule int) ([]fts.Lexeme, error) {
	value, err := d.normalizer.Lexize(ctx, word)
	if err != nil {
		return nil, err
	}
	if !value.Recognized {
		return nil, dictionaryError("F0000", fmt.Sprintf(`thesaurus %s word "%s" isn't recognized by subdictionary (rule %d)`, side, word, rule))
	}
	if len(value.Lexemes) == 0 {
		e := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "F0000", Message: fmt.Sprintf(`thesaurus %s word "%s" is a stop word (rule %d)`, side, word, rule)}
		if side == "sample" {
			e.Hint = `Use "?" to represent a stop word within a sample phrase.`
		}
		return nil, e
	}
	return value.Lexemes, nil
}

func (*ThesaurusDictionary) Filtering() bool { return false }
func (d *ThesaurusDictionary) Configured(resource string) bool {
	return d != nil && d.resource == resource
}
func (d *ThesaurusDictionary) NewMatcher() expr.PhraseMatcher {
	return &thesaurusMatcher{dictionary: d, node: &d.root}
}
func (d *ThesaurusDictionary) Lexize(ctx context.Context, text string) (fts.LexicalResult, error) {
	matcher := d.NewMatcher()
	words, _, err := matcher.Step(ctx, text, false)
	if err != nil {
		return fts.LexicalResult{}, err
	}
	if words == nil {
		return fts.LexicalResult{}, ctx.Err()
	}
	lexemes := make([]fts.Lexeme, len(words))
	for i, word := range words {
		lexemes[i] = fts.Lexeme{Text: word.Text, Prefix: word.Prefix, Variant: word.Variant}
	}
	return fts.LexicalResult{Recognized: true, Lexemes: lexemes}, ctx.Err()
}

type thesaurusMatcher struct {
	dictionary *ThesaurusDictionary
	node       *thesaurusNode
}

func (m *thesaurusMatcher) Reset() { m.node = &m.dictionary.root }
func (m *thesaurusMatcher) Step(ctx context.Context, input string, end bool) ([]expr.PhraseLexeme, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if end || m.node == nil {
		return nil, false, nil
	}
	value, err := m.dictionary.normalizer.Lexize(ctx, input)
	if err != nil {
		return nil, false, err
	}
	if !value.Recognized {
		m.node = nil
		return nil, false, nil
	}
	key := thesaurusKey(value.Lexemes)
	m.node = m.node.next[key]
	if m.node == nil {
		return nil, false, nil
	}
	return m.node.replacement, len(m.node.next) > 0, nil
}

func thesaurusKey(words []fts.Lexeme) string {
	if len(words) == 0 {
		return thesaurusStopword
	}
	if len(words) == 1 {
		return words[0].Text
	}
	var key strings.Builder
	for i, word := range words {
		if i > 0 {
			key.WriteByte(1)
		}
		key.WriteString(word.Text)
	}
	return key.String()
}
