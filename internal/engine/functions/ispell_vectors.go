package functions

// Raw flag identities and merge demand follow PostgreSQL REL_18_6
// src/backend/tsearch/spell.c.
// Portions Copyright (c) 1996-2026, PostgreSQL Global Development Group.
// Portions Copyright (c) 1994, Regents of the University of California.
// PostgreSQL License: https://www.postgresql.org/about/licence/

import (
	"context"
	"strings"
)

// These identities and decode caches belong only to Finish. Original nonalias
// vectors are interned; alias slots and every nonempty merge keep distinct IDs,
// even when their text is equal. Published nodes retain only final membership.
type ispellFlagVector struct {
	raw     string
	flags   []string
	err     error
	bits    uint8
	decoded bool
}

type ispellFlagVectors struct {
	compiler *IspellCompiler
	values   []ispellFlagVector
}

func (b *IspellCompiler) flagVectors(ctx context.Context) (*ispellFlagVectors, error) {
	vectors := &ispellFlagVectors{compiler: b}
	if b.aliases != nil {
		vectors.values = make([]ispellFlagVector, len(b.aliases))
		for i, raw := range b.aliases {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			vectors.values[i].raw = raw
		}
		for i := range b.words {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			id, err := b.dictionaryAliasID(b.words[i].flags)
			if err != nil {
				return nil, err
			}
			b.words[i].flagID = id
		}
	} else {
		if err := ispellSort(ctx, b.words, func(a, c ispellWord) int {
			return ispellCompare(ctx, a.flags, c.flags)
		}); err != nil {
			return nil, err
		}
		for i := range b.words {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			raw := b.words[i].flags
			if len(vectors.values) == 0 || ispellCompare(ctx, vectors.values[len(vectors.values)-1].raw, raw) != 0 {
				vectors.values = append(vectors.values, ispellFlagVector{raw: raw})
			}
			b.words[i].flagID = len(vectors.values) - 1
		}
	}
	// Equal-word ordering is not a public contract. Stable grouping preserves
	// the preceding raw-vector ordering, or imported alias ordering.
	if err := ispellSort(ctx, b.words, func(a, c ispellWord) int {
		return ispellCompare(ctx, a.text, c.text)
	}); err != nil {
		return nil, err
	}
	return vectors, ctx.Err()
}

func (v *ispellFlagVectors) decode(ctx context.Context, id int) (*ispellFlagVector, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value := &v.values[id]
	if !value.decoded {
		flags, err := v.compiler.flags(ctx, value.raw)
		if canceled := ctx.Err(); canceled != nil {
			return nil, canceled
		}
		var bits uint8
		if len(v.compiler.compound) != 0 && err == nil {
			for _, flag := range flags {
				if canceled := ctx.Err(); canceled != nil {
					return nil, canceled
				}
				bits |= v.compiler.compound[flag]
			}
		}
		value.bits = bits & (ispellAll | ispellOnly)
		value.flags, value.err, value.decoded = flags, err, true
	}
	return value, nil
}

func (v *ispellFlagVectors) compound(ctx context.Context, id int) (uint8, error) {
	if len(v.compiler.compound) == 0 {
		return 0, ctx.Err()
	}
	value, err := v.decode(ctx, id)
	if err != nil {
		return 0, err
	}
	return value.bits, value.err
}

func (v *ispellFlagVectors) merge(ctx context.Context, first, second int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	a, b := v.values[first].raw, v.values[second].raw
	if a == "" {
		return second, nil
	}
	if b == "" {
		return first, nil
	}
	var raw strings.Builder
	size := len(a) + len(b)
	if v.compiler.mode == "num" {
		size++
	}
	raw.Grow(size)
	for len(a) > 0 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n := min(len(a), 4096)
		raw.WriteString(a[:n])
		a = a[n:]
	}
	if v.compiler.mode == "num" {
		raw.WriteByte(',')
	}
	for len(b) > 0 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n := min(len(b), 4096)
		raw.WriteString(b[:n])
		b = b[n:]
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	v.values = append(v.values, ispellFlagVector{raw: raw.String()})
	return len(v.values) - 1, nil
}

// Compound-marked affixes demand the entire ordered table, including unused
// original aliases and intermediate merges, not just final terminal vectors.
// Like IsAffixFlagInUse, a complete prefix match wins before a malformed tail.
func (v *ispellFlagVectors) inUse(ctx context.Context, flag string) (bool, error) {
	if flag == "" {
		return true, ctx.Err()
	}
	for id := range v.values {
		value, err := v.decode(ctx, id)
		if err != nil {
			return false, err
		}
		for _, candidate := range value.flags {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if candidate == flag {
				return true, nil
			}
		}
		if value.err != nil {
			return false, value.err
		}
	}
	return false, ctx.Err()
}
