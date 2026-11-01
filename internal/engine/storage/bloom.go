package storage

import (
	"context"
	"fmt"

	"stuntgres/internal/sqltype"
)

// BloomOptions is effective build intent, separate from lexical reloptions.
type BloomOptions struct {
	Words int
	Bits  [32]int
}

type BloomBinding struct {
	Usage    sqltype.Usage
	Class    sqltype.IndexClass
	Provider sqltype.IndexHash
	Slot     int // -1 for an expression, never a simple-column query probe.
}

type BloomEntry struct {
	Signature []uint16 // Immutable owned backing, shared only by unchanged versions.
}

type BloomRoot struct {
	Entries    []BloomEntry // Same ordinal as IndexRoot.Entries.
	Bindings   []BloomBinding
	Options    BloomOptions
	Storage    *Relation
	Generation uint64
	Prefix     int
	Ends       *DeletionRoot
}

// AddBloomValue uses the release's local Park–Miller stream. The caller owns
// mask; this operation never retains scratch or mutates a published signature.
func AddBloomValue(ctx context.Context, mask []uint16, options BloomOptions, column int, provider *sqltype.IndexHash, value sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if column < 0 || column >= len(options.Bits) || len(mask) != options.Words || options.Words < 1 || options.Words > 256 {
		return fmt.Errorf("stuntgres: invalid bloom signature shape")
	}
	if value.IsNull() {
		return nil
	}
	hash, err := provider.Hash(ctx, value)
	if err != nil {
		return err
	}
	state := uint32(column)%0x7ffffffe + 1
	next := func() uint32 {
		state = uint32(uint64(state) * 16807 % 0x7fffffff)
		return state - 1
	}
	state = (uint32(hash)^next())%0x7ffffffe + 1
	for j := range options.Bits[column] {
		if j&127 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		bit := int(next() % uint32(options.Words*16))
		mask[bit/16] |= uint16(1) << uint(bit%16)
	}
	return ctx.Err()
}

func BloomContains(signature, mask []uint16) bool {
	if len(signature) != len(mask) {
		return false
	}
	for i, word := range mask {
		if signature[i]&word != word {
			return false
		}
	}
	return true
}
