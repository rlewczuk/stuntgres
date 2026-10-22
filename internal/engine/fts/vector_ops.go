package fts

import (
	"context"
	"fmt"
	"strings"
)

func (v *Vector) Concat(ctx context.Context, other *Vector) (*Vector, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !v.Valid() || !other.Valid() {
		return nil, fmt.Errorf("invalid tsvector operands")
	}
	if len(v.lexemes) == 0 {
		return other, ctx.Err()
	}
	if len(other.lexemes) == 0 {
		return v, ctx.Err()
	}
	var offset uint16
	for _, entry := range v.lexemes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if n := len(entry.positions); n > 0 && entry.positions[n-1].number > offset {
			offset = entry.positions[n-1].number
		}
	}
	// Count distinct merged lexemes first, so result ownership has exact capacity.
	count := 0
	for i, j := 0, 0; i < len(v.lexemes) || j < len(other.lexemes); count++ {
		if count&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		switch {
		case i == len(v.lexemes):
			j++
		case j == len(other.lexemes):
			i++
		default:
			order := strings.Compare(v.lexemes[i].word, other.lexemes[j].word)
			if order <= 0 {
				i++
			}
			if order >= 0 {
				j++
			}
		}
	}
	entries := make([]lexeme, count)
	size := 0
	for i, j, k := 0, 0, 0; k < count; k++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var entry lexeme
		var err error
		switch {
		case j == len(other.lexemes) || i < len(v.lexemes) && v.lexemes[i].word < other.lexemes[j].word:
			entry = v.lexemes[i]
			i++
		case i == len(v.lexemes) || other.lexemes[j].word < v.lexemes[i].word:
			entry = other.lexemes[j]
			j++
			entry.positions, err = concatPositions(ctx, nil, entry.positions, offset)
		default:
			entry = v.lexemes[i]
			entry.positions, err = concatPositions(ctx, entry.positions, other.lexemes[j].positions, offset)
			i++
			j++
		}
		if err != nil {
			return nil, err
		}
		size += len(entry.word)
		if len(entry.positions) > 0 {
			size = (size + 1) &^ 1
			size += 2 + 2*len(entry.positions)
		}
		entries[k] = entry
	}
	if size > maxStringOffset {
		return nil, vectorSize(size)
	}
	return renderVector(ctx, entries)
}

// Positions are already sorted and bounded to256. Left positions win; the
// first newly saturated right position wins, unlike input duplicate merging.
func concatPositions(ctx context.Context, left, right []position, offset uint16) ([]position, error) {
	if len(right) == 0 || len(left) == 256 || len(left) > 0 && left[len(left)-1].number == 16383 {
		return left, ctx.Err()
	}
	if len(left) == 0 && offset == 0 {
		return right, ctx.Err()
	}
	var scratch [256]position
	n := copy(scratch[:], left)
	for _, p := range right {
		number := uint32(offset) + uint32(p.number)
		if number > 16383 {
			number = 16383
		}
		p.number = uint16(number)
		scratch[n] = p
		n++
		if n == 256 || p.number == 16383 {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	positions := make([]position, n)
	copy(positions, scratch[:n])
	return positions, ctx.Err()
}
