package fts

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

type position struct {
	number uint16
	weight byte
}
type lexeme struct {
	word      string
	positions []position
}

// Vector is an immutable, canonical set of lexemes and retained positions.
type Vector struct {
	lexemes []lexeme
	text    string
}

func (v *Vector) String() string { return v.text }
func (v *Vector) Valid() bool    { return v != nil && v.lexemes != nil }
func (v *Vector) Equal(ctx context.Context, other *Vector) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !v.Valid() || !other.Valid() {
		return false, nil
	}
	if v == other {
		return true, nil
	}
	if len(v.lexemes) != len(other.lexemes) {
		return false, nil
	}
	for i, a := range v.lexemes {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		b := other.lexemes[i]
		if a.word != b.word || !slices.Equal(a.positions, b.positions) {
			return false, nil
		}
	}
	return true, nil
}

// decimalPosition models pinned 64-bit libc strtol saturation followed by atoi's
// signed32 narrowing, LIMITPOS and WordEntryPos's fourteen-bit field.
func decimalPosition(text string) uint16 {
	var n uint64
	for i := range len(text) {
		digit := uint64(text[i] - '0')
		if n > (math.MaxInt64-digit)/10 {
			n = math.MaxInt64
			break
		}
		n = n*10 + digit
	}
	signed := int32(n)
	if signed >= 16384 {
		return 16383
	}
	return uint16(uint32(signed) & 16383)
}

func (l *lexer) positions() ([]position, error) {
	if l.pos == len(l.text) || l.text[l.pos] != ':' {
		return nil, nil
	}
	l.pos++
	var result []position
	for {
		start := l.pos
		for l.pos < len(l.text) && l.text[l.pos] >= '0' && l.text[l.pos] <= '9' {
			l.pos++
			if err := l.check(); err != nil {
				return nil, err
			}
		}
		if start == l.pos {
			return nil, l.syntax()
		}
		p := position{number: decimalPosition(l.text[start:l.pos])}
		if p.number == 0 {
			return nil, failure("42601", "wrong position info in tsvector: \""+l.text+"\"")
		}
		for l.pos < len(l.text) && !space(l.text[l.pos]) && l.text[l.pos] != ',' {
			c := l.text[l.pos]
			l.pos++
			if err := l.check(); err != nil {
				return nil, err
			}
			if c >= '0' && c <= '9' {
				continue
			}
			if p.weight != 0 {
				return nil, l.syntax()
			}
			switch c {
			case 'a', 'A', '*':
				p.weight = 3
			case 'b', 'B':
				p.weight = 2
			case 'c', 'C':
				p.weight = 1
			case 'd', 'D':
			default:
				return nil, l.syntax()
			}
		}
		result = append(result, p)
		if l.pos == len(l.text) || l.text[l.pos] != ',' {
			return result, nil
		}
		l.pos++
	}
}

func comparePositions(a, b position) int { return int(a.number) - int(b.number) }

func uniquePositions(ctx context.Context, in []position) ([]position, error) {
	if len(in) < 2 {
		return in, ctx.Err()
	}
	if err := stableSort(ctx, in, comparePositions); err != nil {
		return nil, err
	}
	n := 1
	for i, p := range in[1:] {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if p.number != in[n-1].number {
			in[n] = p
			n++
			if n == 256 || p.number == 16383 {
				break
			}
		} else if p.weight > in[n-1].weight {
			in[n-1].weight = p.weight
		}
	}
	return in[:n], ctx.Err()
}

func ParseVector(ctx context.Context, text string) (*Vector, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l := lexer{ctx: ctx, text: text}
	entries := make([]lexeme, 0)
	rawBytes := 0
	for {
		if err := l.skip(); err != nil {
			return nil, err
		}
		if l.pos == len(text) {
			break
		}
		word, err := l.word()
		if err != nil {
			return nil, err
		}
		positions, err := l.positions()
		if err != nil {
			return nil, err
		}
		if len(word) > maxWordBytes {
			return nil, failure("54000", fmt.Sprintf("word is too long (%d bytes, max 2046 bytes)", len(word)))
		}
		if rawBytes > maxStringOffset {
			return nil, vectorSize(rawBytes)
		}
		rawBytes += len(word)
		entries = append(entries, lexeme{word: word, positions: positions})
	}
	return canonicalVector(ctx, entries)
}

func canonicalVector(ctx context.Context, entries []lexeme) (*Vector, error) {
	if err := stableSort(ctx, entries, func(a, b lexeme) int { return strings.Compare(a.word, b.word) }); err != nil {
		return nil, err
	}
	merged := entries[:0]
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(merged) > 0 && merged[len(merged)-1].word == entry.word {
			last := &merged[len(merged)-1]
			last.positions = append(last.positions, entry.positions...)
		} else {
			merged = append(merged, entry)
		}
	}
	size := 0
	for i := range merged {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry := &merged[i]
		positions, err := uniquePositions(ctx, entry.positions)
		if err != nil {
			return nil, err
		}
		entry.positions = positions
		size += len(entry.word)
		if len(entry.positions) > 0 {
			size = (size + 1) &^ 1
			size += 2 + 2*len(entry.positions)
		}
	}
	if size > maxStringOffset {
		return nil, vectorSize(size)
	}
	return renderVector(ctx, merged)
}

func renderVector(ctx context.Context, merged []lexeme) (*Vector, error) {
	var out strings.Builder
	var number [20]byte
	for i, entry := range merged {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if i > 0 {
			out.WriteByte(' ')
		}
		quote(&out, entry.word)
		for j, p := range entry.positions {
			if j == 0 {
				out.WriteByte(':')
			} else {
				out.WriteByte(',')
			}
			out.Write(strconv.AppendUint(number[:0], uint64(p.number), 10))
			if p.weight > 0 {
				out.WriteByte("DCBA"[p.weight])
			}
		}
	}
	return &Vector{lexemes: merged, text: out.String()}, nil
}
func vectorSize(n int) error {
	return failure("54000", fmt.Sprintf("string is too long for tsvector (%d bytes, max 1048575 bytes)", n))
}
