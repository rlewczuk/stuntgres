package fts

import (
	"context"
	"stuntgres/internal/sqlstate"
)

// VectorBuilder owns invocation-private lexemes and positions; Finish transfers
// that backing into one immutable canonical vector without text re-input.
type VectorBuilder struct{ entries []lexeme }

func (b *VectorBuilder) Add(word string, number int) {
	if number > 16383 {
		number = 16383
	}
	b.entries = append(b.entries, lexeme{word: word, positions: []position{{number: uint16(number)}}})
}
func (b *VectorBuilder) Finish(ctx context.Context) (*Vector, error) {
	entries := b.entries
	if entries == nil {
		entries = []lexeme{}
	}
	b.entries = nil
	vector, err := canonicalVector(ctx, entries)
	return vector, sqlstate.HardInputError(err)
}
