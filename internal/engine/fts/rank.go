package fts

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// Rank consumes immutable decoded operands. Scratch and cancellation belong to
// this invocation; the returned REAL follows the release's intermediate float
// stores, not a single final float64 narrowing. density selects cover density.
func (v *Vector) Rank(ctx context.Context, q *Query, weights [4]float32, normalization int32, density bool) (float32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !v.Valid() || !q.Valid() {
		return 0, fmt.Errorf("invalid FTS rank operands")
	}
	if len(v.lexemes) == 0 || q.Empty() {
		return 0, ctx.Err()
	}
	if density {
		return v.rankCoverDensity(ctx, q, weights, normalization)
	}
	var inline [32]int
	items := inline[:0]
	// Collect the released right-first arena order before qsort_arg's
	// observable equality swaps and first-equal deduplication.
	for i := len(q.nodes) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if q.nodes[i].op == 'v' {
			items = append(items, i)
		}
	}
	if err := rankSortOperands(ctx, items, q.nodes); err != nil {
		return 0, err
	}
	count := 0
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if count == 0 || q.nodes[items[count-1]].word != q.nodes[item].word {
			items[count] = item
			count++
		}
	}
	items = items[:count]
	var result float32
	root := q.nodes[q.root].op
	if len(items) > 1 && (root == '&' || root == '<') {
		result = -1
		type remembered struct {
			positions []position
			stripped  bool
		}
		prior := make([]remembered, len(items))
		stripped := [1]position{{number: 16383}}
		steps := 0
		for i, item := range items {
			start, end, err := v.rankEntries(ctx, q.nodes[item])
			if err != nil {
				return 0, err
			}
			for _, entry := range v.lexemes[start:end] {
				current := remembered{positions: entry.positions}
				if len(current.positions) == 0 {
					current.positions, current.stripped = stripped[:], true
				}
				prior[i] = current
				for _, previous := range prior[:i] {
					for _, a := range current.positions {
						for _, b := range previous.positions {
							if steps&255 == 0 {
								if err := ctx.Err(); err != nil {
									return 0, err
								}
							}
							steps++
							distance := int(a.number) - int(b.number)
							if distance < 0 {
								distance = -distance
							}
							if distance == 0 {
								if !current.stripped && !previous.stripped {
									continue
								}
								distance = 16384
							}
							attenuation := float32(1e-30)
							if distance <= 100 {
								attenuation = float32(1 / (1.005 + .05*math.Exp(float64(float32(distance))/1.5-2)))
							}
							product := float32(weights[a.weight] * weights[b.weight])
							product = float32(product * attenuation)
							pair := float32(math.Sqrt(float64(product)))
							if result < 0 {
								result = pair
							} else {
								result = float32(1 - (1-float64(result))*(1-float64(pair)))
							}
						}
					}
				}
			}
		}
		if result < 0 {
			result = 1e-20
		}
	} else {
		for _, item := range items {
			start, end, err := v.rankEntries(ctx, q.nodes[item])
			if err != nil {
				return 0, err
			}
			for _, entry := range v.lexemes[start:end] {
				positions := entry.positions
				stripped := [1]position{{}}
				if len(positions) == 0 {
					positions = stripped[:]
				}
				var sum float32
				maximum := float32(-1)
				maximumIndex := 0
				for j, p := range positions {
					if err := ctx.Err(); err != nil {
						return 0, err
					}
					weight := weights[p.weight]
					sum = float32(sum + float32(weight/float32((j+1)*(j+1))))
					if weight > maximum {
						maximum, maximumIndex = weight, j
					}
				}
				contribution := float32(float32(maximum+sum) - float32(maximum/float32((maximumIndex+1)*(maximumIndex+1))))
				result = float32(float64(result) + float64(contribution)/1.64493406685)
			}
		}
		if len(items) > 0 {
			result = float32(result / float32(len(items)))
		}
	}
	length, unique := 0, len(v.lexemes)
	if normalization&3 != 0 {
		var err error
		length, _, err = v.rankLengths(ctx)
		if err != nil {
			return 0, err
		}
	}
	if normalization&1 != 0 {
		result = float32(float64(result) / (math.Log(float64(length+1)) / math.Log(2)))
	}
	if normalization&2 != 0 && length > 0 {
		result = float32(result / float32(length))
	}
	if normalization&8 != 0 {
		result = float32(result / float32(unique))
	}
	if normalization&16 != 0 {
		result = float32(float64(result) / (math.Log(float64(unique+1)) / math.Log(2)))
	}
	if normalization&32 != 0 {
		result = float32(result / float32(result+1))
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return result, nil
}

func (v *Vector) rankEntries(ctx context.Context, n queryNode) (int, int, error) {
	lo, hi := 0, len(v.lexemes)
	for lo < hi {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		mid := lo + (hi-lo)/2
		if v.lexemes[mid].word < n.word {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	end := lo
	for end < len(v.lexemes) {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		word := v.lexemes[end].word
		if word != n.word && (!n.prefix || !strings.HasPrefix(word, n.word)) {
			break
		}
		end++
		if !n.prefix {
			break
		}
	}
	return lo, end, nil
}

func (v *Vector) rankLengths(ctx context.Context) (int, int, error) {
	length := 0
	for _, entry := range v.lexemes {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		length += max(1, len(entry.positions))
	}
	return length, len(v.lexemes), nil
}
