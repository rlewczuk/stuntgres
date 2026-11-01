package functions

// The positional extent scan is adapted from PostgreSQL REL_18_6,
// contrib/pg_trgm/trgm_op.c (make_positional_trgm, calc_word_similarity and
// iterate_word_similarity). Extraction specializes its released UTF8/C profile.
//
// Portions Copyright (c) 1996-2025, PostgreSQL Global Development Group
// Portions Copyright (c) 1994, The Regents of the University of California
//
// Permission to use, copy, modify, and distribute this software and its
// documentation for any purpose, without fee, and without a written agreement
// is hereby granted, provided that the above copyright notice and this
// paragraph and the following two paragraphs appear in all copies.
//
// IN NO EVENT SHALL THE UNIVERSITY OF CALIFORNIA BE LIABLE TO ANY PARTY FOR
// DIRECT, INDIRECT, SPECIAL, INCIDENTAL, OR CONSEQUENTIAL DAMAGES, INCLUDING
// LOST PROFITS, ARISING OUT OF THE USE OF THIS SOFTWARE AND ITS
// DOCUMENTATION, EVEN IF THE UNIVERSITY OF CALIFORNIA HAS BEEN ADVISED OF THE
// POSSIBILITY OF SUCH DAMAGE.
//
// THE UNIVERSITY OF CALIFORNIA SPECIFICALLY DISCLAIMS ANY WARRANTIES,
// INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY
// AND FITNESS FOR A PARTICULAR PURPOSE. THE SOFTWARE PROVIDED HEREUNDER IS
// ON AN "AS IS" BASIS, AND THE UNIVERSITY OF CALIFORNIA HAS NO OBLIGATIONS TO
// PROVIDE MAINTENANCE, SUPPORT, UPDATES, ENHANCEMENTS, OR MODIFICATIONS.

import (
	"context"
	"slices"
)

const (
	pgTrgmLeft uint8 = 1 << iota
	pgTrgmRight
)

func pgTrgmWord(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// pgTrgmExtract retains each occurrence in source order. Bounds, when demanded,
// mark full-word edges; arbitrary extents must never acquire new padding.
func pgTrgmExtract(ctx context.Context, text string, strict bool) ([]uint32, []uint8, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	var keys []uint32
	var bounds []uint8
	for i := 0; i < len(text); {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
		}
		if !pgTrgmWord(text[i]) {
			i++
			continue
		}
		if keys == nil {
			// A word contributes its byte length plus one trigram. A
			// separating byte pays for every word after the first.
			keys = make([]uint32, 0, len(text)+1)
			if strict {
				bounds = make([]uint8, 0, len(text)+1)
			}
		}
		start := len(keys)
		rolling := uint32(' ')<<8 | uint32(' ')
		for i < len(text) && pgTrgmWord(text[i]) {
			if i&1023 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, nil, err
				}
			}
			c := text[i]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			rolling = (rolling<<8 | uint32(c)) & 0xffffff
			keys = append(keys, rolling)
			if strict {
				bounds = append(bounds, 0)
			}
			i++
		}
		keys = append(keys, (rolling<<8|uint32(' '))&0xffffff)
		if strict {
			bounds = append(bounds, pgTrgmRight)
			bounds[start] |= pgTrgmLeft
		}
	}
	return keys, bounds, nil
}

// PgTrgmTrigrams returns owned sorted distinct packed ASCII trigrams. Each key
// contains its three bytes in lexicographic, most-significant-byte-first order.
func PgTrgmTrigrams(ctx context.Context, text string) ([]uint32, error) {
	keys, _, err := pgTrgmExtract(ctx, text, false)
	if err != nil {
		return nil, err
	}
	slices.Sort(keys)
	return slices.Compact(keys), nil
}

func pgTrgmScore(matches, first, second int) float32 {
	if matches == 0 {
		return 0
	}
	return float32(matches) / float32(first+second-matches)
}

// PgTrgmSimilarity uses released float4 Jaccard arithmetic, including zero for
// either empty trigram set (and therefore for empty versus empty).
func PgTrgmSimilarity(ctx context.Context, left, right string) (float32, error) {
	first, err := PgTrgmTrigrams(ctx, left)
	if err != nil {
		return 0, err
	}
	second, err := PgTrgmTrigrams(ctx, right)
	if err != nil {
		return 0, err
	}
	matches := 0
	for i, j := 0, 0; i < len(first) && j < len(second); {
		if (i+j)&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		switch {
		case first[i] < second[j]:
			i++
		case first[i] > second[j]:
			j++
		default:
			matches++
			i++
			j++
		}
	}
	return pgTrgmScore(matches, len(first), len(second)), nil
}

type pgTrgmPosition struct {
	key   uint32
	index int // -1 identifies the first argument's distinct set.
}

// PgTrgmWordSimilarity searches continuous extents of the second positional
// stream. Strict restricts both edges to word boundaries, not to one word.
func PgTrgmWordSimilarity(ctx context.Context, left, right string, strict bool) (float32, error) {
	return pgTrgmWordSimilarity(ctx, left, right, strict, false, 0)
}

// Check-only mode returns a qualifying float4 score as soon as a legal extent
// reaches the float64 threshold; otherwise it returns the full maximum.
func pgTrgmWordSimilarity(ctx context.Context, left, right string, strict, checkOnly bool, threshold float64) (float32, error) {
	first, err := PgTrgmTrigrams(ctx, left)
	if err != nil {
		return 0, err
	}
	second, bounds, err := pgTrgmExtract(ctx, right, strict)
	if err != nil {
		return 0, err
	}
	if len(first) == 0 || len(second) == 0 {
		return 0, nil
	}
	positions := make([]pgTrgmPosition, len(first)+len(second))
	for i, key := range first {
		positions[i] = pgTrgmPosition{key: key, index: -1}
	}
	for i, key := range second {
		positions[len(first)+i] = pgTrgmPosition{key: key, index: i}
	}
	slices.SortFunc(positions, func(a, b pgTrgmPosition) int {
		if a.key < b.key {
			return -1
		}
		if a.key > b.key {
			return 1
		}
		return a.index - b.index
	})
	indexes := make([]int, len(second))
	found := make([]bool, len(positions))
	unique := 0
	for i, position := range positions {
		if i > 0 && position.key != positions[i-1].key {
			unique++
		}
		if position.index < 0 {
			found[unique] = true
		} else {
			indexes[position.index] = unique
		}
	}
	last := make([]int, unique+1)
	for i := range last {
		last[i] = -1
	}
	lower, distinct, matches := -1, 0, 0
	if strict {
		lower = 0
	}
	var maximum float32
	for upper, index := range indexes {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if lower >= 0 || found[index] {
			if last[index] < 0 {
				distinct++
				if found[index] {
					matches++
				}
			}
			last[index] = upper
		}
		if strict && bounds[upper]&pgTrgmRight == 0 || !strict && !found[index] {
			continue
		}
		if lower < 0 {
			lower, distinct = upper, 1
		}
		current := pgTrgmScore(matches, len(first), distinct)
		trialMatches, trialDistinct, previous := matches, distinct, lower
		for trial := lower; trial <= upper; trial++ {
			if trial&1023 == 0 {
				if err := ctx.Err(); err != nil {
					return 0, err
				}
			}
			if !strict || bounds[trial]&pgTrgmLeft != 0 {
				if score := pgTrgmScore(trialMatches, len(first), trialDistinct); score > current {
					current = score
					distinct, matches, lower = trialDistinct, trialMatches, trial
				}
				if checkOnly && float64(current) >= threshold {
					return current, nil
				}
			}
			index := indexes[trial]
			if last[index] == trial {
				trialDistinct--
				if found[index] {
					trialMatches--
				}
			}
		}
		if current > maximum {
			maximum = current
		}
		for previous < lower {
			index := indexes[previous]
			if last[index] == previous {
				last[index] = -1
			}
			previous++
		}
	}
	return maximum, nil
}
