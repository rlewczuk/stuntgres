package fts

import (
	"context"
	"strings"
	"unicode/utf8"
)

// DocumentToken is a released default-parser category and an unchanged input span.
// Whole URL and hyphenated-word spans intentionally overlap subsequent tokens.
type DocumentToken struct {
	ID int32
	Text string
}

// ParseDocument tokenizes UTF8 text using PostgreSQL 18's default parser in the
// C locale. All returned strings share the immutable input. Cancellation never
// publishes a prefix; document tokens do not inherit tsvector's lexeme limits.
func ParseDocument(ctx context.Context, text string) ([]DocumentToken, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	budget := documentBudget{ctx: ctx}
	var frames [16]documentPosition
	p := documentParser{text: text, budget: &budget, stack: frames[:0]}
	var tokens []DocumentToken
	for p.next() {
		tokens = append(tokens, DocumentToken{ID: p.kind, Text: text[p.start:p.start+p.length]})
	}
	if budget.err != nil {
		return nil, budget.err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return tokens, nil
}

// A single budget is shared with lookahead and survives every rewind/replay.
type documentBudget struct {
	ctx context.Context
	steps uint8
	err error
}

func (b *documentBudget) tick() bool {
	b.steps++
	if b.steps == 0 {
		b.err = b.ctx.Err()
	}
	return b.err == nil
}

type documentPosition struct {
	pos, chars, width int
	length, charLength int
	state documentState
	resume int
}

type documentParser struct {
	text string
	budget *documentBudget
	position documentPosition
	stack []documentPosition
	ignore, wantHost bool
	start, length, charLength int
	kind int32
}

// next preserves the released interpreter ordering: predicate (which may do
// lookahead), special, capture, stack operation, state change, break/rerun/step.
func (p *documentParser) next() bool {
	if p.position.pos >= len(p.text) || p.budget.err != nil {
		return false
	}
	p.start = p.position.pos
	p.position.resume = 0
	for p.position.pos <= len(p.text) {
		if !p.budget.tick() {
			return false
		}
		p.position.width = 0
		if p.position.pos < len(p.text) {
			_, p.position.width = utf8.DecodeRuneInString(p.text[p.position.pos:])
		}
		actions := documentActions[p.position.state]
		i := p.position.resume
		p.position.resume = 0
		for actions[i].predicate != documentAlways && !p.matches(actions[i]) {
			if p.budget.err != nil {
				return false
			}
			i++
		}
		if p.budget.err != nil {
			return false
		}
		a := actions[i]
		p.special(a.special)
		if a.flags&A_BINGO != 0 {
			p.length, p.charLength = p.position.length, p.position.charLength
			p.position.length, p.position.charLength = 0, 0
			p.kind = a.token
		}
		switch {
		case a.flags&A_POP != 0:
			p.position = p.stack[len(p.stack)-1]
			p.stack = p.stack[:len(p.stack)-1]
		case a.flags&A_PUSH != 0:
			p.position.resume = i+1
			p.stack = append(p.stack, p.position)
			p.position.resume = 0
		case a.flags&A_CLEAR != 0:
			p.stack = p.stack[:len(p.stack)-1]
		case a.flags&A_CLRALL != 0:
			p.stack = p.stack[:0]
		case a.flags&A_MERGE != 0:
			saved := p.stack[len(p.stack)-1]
			p.stack = p.stack[:len(p.stack)-1]
			p.position.state, p.position.resume = saved.state, saved.resume
		}
		if a.to != TPS_Null {
			p.position.state = a.to
		}
		if a.flags&A_BINGO != 0 {
			return true
		}
		if p.position.pos >= len(p.text) && a.flags&A_RERUN == 0 {
			return false
		}
		if a.flags&(A_RERUN|A_POP) != 0 {
			continue
		}
		if p.position.width != 0 {
			p.position.pos += p.position.width
			p.position.length += p.position.width
			p.position.chars++
			p.position.charLength++
		}
	}
	return false
}

func (p *documentParser) special(s uint8) {
	switch s {
	case SpecialTags:
		// The special is called at the end of the tag name, before consuming
		// the delimiter; attributes and self-closing tags retain stock rules.
		n := p.position.charLength
		if n == 8 && strings.EqualFold(p.text[p.start:p.position.pos], "</script") {
			p.ignore = false
		} else if n == 7 {
			if strings.EqualFold(p.text[p.start:p.position.pos], "</style") {
				p.ignore = false
			} else if strings.EqualFold(p.text[p.start:p.position.pos], "<script") {
				p.ignore = true
			}
		} else if n == 6 && strings.EqualFold(p.text[p.start:p.position.pos], "<style") {
			p.ignore = true
		}
	case SpecialFURL:
		p.wantHost = true
		p.rewind()
	case SpecialHyphen:
		p.rewind()
	case SpecialVerVersion:
		p.rewind()
		p.position.length, p.position.charLength = 0, 0
	}
}

func (p *documentParser) rewind() {
	p.position.pos -= p.position.length
	p.position.chars -= p.position.charLength
}

// Lookahead reuses the interpreter, not a second recognizer. Nesting is bounded:
// host lookahead sets wantHost (preventing email/URL recursion), and URL-path
// lookahead can only visit URL-path states. Both share input and cancellation.
func (p *documentParser) lookahead(host bool) bool {
	var frames [8]documentPosition
	q := documentParser{text: p.text[p.position.pos:], budget: p.budget, stack: frames[:0], wantHost: host}
	want := int32(6)
	if !host {
		q.stack = append(q.stack, q.position)
		q.position.state = TPS_InURLPathFirst
		want = 18
	}
	if !q.next() || q.kind != want {
		return false
	}
	p.position.pos += q.length
	p.position.chars += q.charLength
	p.position.length += q.length
	p.position.charLength += q.charLength
	p.position.width = q.position.width
	return true
}

func (p *documentParser) matches(a documentAction) bool {
	var c rune
	if p.position.width != 0 {
		// C-locale predicates only distinguish ASCII from non-ASCII; the
		// interpreter already decoded the UTF8 width once for this step.
		c = rune(p.text[p.position.pos])
	}
	alpha := c > 127 || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
	digit := c >= '0' && c <= '9'
	switch a.predicate {
	case p_isEOF:
		return p.position.width == 0
	case p_iseqC:
		return p.position.width == 1 && c == rune(a.c)
	case p_isasclet:
		return c < 128 && alpha
	case p_isalpha:
		return alpha
	case p_isalnum:
		return alpha || digit
	case p_isnotalnum:
		return !alpha && !digit
	case p_isdigit:
		return digit
	case p_isxdigit:
		return digit || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
	case p_isspace:
		return c == ' ' || c >= '\t' && c <= '\r'
	case p_isurlchar:
		return c > 0x20 && c < 0x7f && !strings.ContainsRune("\"<>\\^`{|}", c)
	case p_isspecial:
		// Under UTF8+C every non-ASCII character, including all released
		// zero-width/spacing-combining marks, matched alpha/alnum earlier.
		// NUL is the only remaining zero-display-width ASCII character.
		return c == 0 && p.position.width != 0
	case p_isignore:
		return p.ignore
	case p_isstophost:
		stop := p.wantHost
		p.wantHost = false
		return stop
	case p_ishost:
		return p.lookahead(true)
	case p_isURLPath:
		return p.lookahead(false)
	default:
		return true
	}
}
