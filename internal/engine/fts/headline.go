package fts

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/util"
)

type headlineAssociation struct {
	position int
	lexeme   string
	next     int
}
type headlineTokenAssociations struct{ first, last int }

// HeadlineDocument contains invocation-owned raw tokens and normalization
// associations. No writable slice or dictionary/configuration authority escapes.
type HeadlineDocument struct {
	tokens       []DocumentToken
	links        []headlineTokenAssociations
	associations []headlineAssociation
}
type HeadlineBuilder struct{ document HeadlineDocument }

// SetTokens transfers a freshly parsed token slice; callers must not mutate it.
func (b *HeadlineBuilder) SetTokens(tokens []DocumentToken) {
	b.document.tokens = tokens
	b.document.links = make([]headlineTokenAssociations, len(tokens))
	for i := range b.document.links {
		b.document.links[i] = headlineTokenAssociations{-1, -1}
	}
}
func (b *HeadlineBuilder) Associate(ctx context.Context, start, end int, lexical LexicalPosition) error {
	for token := start; token < end; token++ {
		for _, word := range lexical.Lexemes {
			if err := ctx.Err(); err != nil {
				return err
			}
			index := len(b.document.associations)
			b.document.associations = append(b.document.associations, headlineAssociation{min(16383, lexical.Position), word.Text, -1})
			link := &b.document.links[token]
			if link.last < 0 {
				link.first = index
			} else {
				b.document.associations[link.last].next = index
			}
			link.last = index
		}
	}
	return ctx.Err()
}
func (b *HeadlineBuilder) Finish(ctx context.Context) (*HeadlineDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	document := b.document
	b.document = HeadlineDocument{}
	return &document, nil
}

type headlineWord struct {
	text                                  string
	kind, position, item                  int
	repeated, selected, in, skip, replace bool
}

func (d *HeadlineDocument) words(ctx context.Context, q *Query) ([]headlineWord, error) {
	words := make([]headlineWord, 0, len(d.tokens))
	for i, token := range d.tokens {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(token.Text) >= 2047 {
			continue
		}
		words = append(words, headlineWord{text: token.Text, kind: int(token.ID), item: -1})
		for link := d.links[i].first; link >= 0; link = d.associations[link].next {
			association := d.associations[link]
			base := len(words) - 1
			words[base].position = association.position
			for node := len(q.nodes) - 1; node >= 0; node-- {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				n := q.nodes[node]
				if n.op != 'v' || association.lexeme != n.word && (!n.prefix || !strings.HasPrefix(association.lexeme, n.word)) {
					continue
				}
				if words[base].item < 0 {
					words[base].item = node
				} else {
					repeated := words[base]
					repeated.item, repeated.repeated = node, true
					words = append(words, repeated)
				}
			}
		}
	}
	return words, ctx.Err()
}

// Headline selects against normalized associations but renders original bytes.
// It intentionally neither escapes HTML nor sanitizes source text or markers.
func (d *HeadlineDocument) Headline(ctx context.Context, q *Query, options HeadlineOptions) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if d == nil || !q.Valid() {
		return "", fmt.Errorf("invalid headline operands")
	}
	words, err := d.words(ctx, q)
	if err != nil {
		return "", err
	}
	locations, err := q.headlineLocations(ctx, words)
	if err != nil {
		return "", err
	}
	if options.MaxFragments == 0 {
		err = markHeadlineWords(ctx, q, words, locations, options)
	} else {
		err = markHeadlineFragments(ctx, q, words, locations, options)
	}
	if err != nil {
		return "", err
	}
	for _, marker := range []struct{ name, value string }{{"StartSel", options.StartSel}, {"StopSel", options.StopSel}, {"FragmentDelimiter", options.FragmentDelimiter}} {
		if len(marker.value) > 32767 {
			return "", failure("22023", fmt.Sprintf("value for \"%s\" is too long", marker.name))
		}
	}
	return renderHeadlineWords(ctx, words, options)
}

// renderHeadlineWords emits already selected original-byte tokens. Selection
// and rendering share invocation-owned words without copying their storage.
func renderHeadlineWords(ctx context.Context, words []headlineWord, options HeadlineOptions) (string, error) {
	var output strings.Builder
	inside, first := false, true
	pausePending := true
	for _, word := range words {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if !word.in {
			if !word.repeated {
				inside = false
			}
			continue
		}
		if !inside {
			if !first {
				output.WriteString(options.FragmentDelimiter)
			}
			inside, first = true, false
		}
		if word.replace {
			output.WriteByte(' ')
		} else if !word.skip {
			if word.selected {
				output.WriteString(options.StartSel)
			}
			output.WriteString(word.text)
			if word.selected {
				output.WriteString(options.StopSel)
			}
		}
		if pausePending && output.Len() != 0 {
			pausePending = false
			if err := util.ExecutionCheckpoint(ctx, "text_search.headline.render"); err != nil {
				return "", err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return output.String(), nil
}
