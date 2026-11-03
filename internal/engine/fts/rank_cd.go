package fts

import (
	"context"
	"math"
)

type rankOccurrence struct {
	position    position
	entry, node int
}
type rankDocumentEntry struct {
	position    position
	first, last int
}

func (v *Vector) rankCoverDensity(ctx context.Context, q *Query, weights [4]float32, normalization int32) (float32, error) {
	document, occurrences, err := v.rankDocument(ctx, q)
	if err != nil || len(document) == 0 {
		return 0, err
	}
	var inverse [4]float64
	for i, w := range weights {
		inverse[i] = 1 / float64(w)
	}
	score, sumDistance, previousMidpoint := float64(0), float64(0), float64(0)
	extents := 0
	err = rankCovers(ctx, q, document, occurrences, func(begin, end int) error {
		var inverseSum float64
		for _, entry := range document[begin : end+1] {
			if err := ctx.Err(); err != nil {
				return err
			}
			inverseSum += inverse[entry.position.weight]
		}
		p, r := int(document[begin].position.number), int(document[end].position.number)
		noise := (r - p) - (end - begin)
		if noise < 0 {
			noise = (end - begin) / 2
		}
		score += (float64(end-begin+1) / inverseSum) / float64(1+noise)
		midpoint := float64(p+r) / 2
		if extents > 0 && midpoint > previousMidpoint {
			sumDistance += 1 / (midpoint - previousMidpoint)
		}
		previousMidpoint = midpoint
		extents++
		return nil
	})
	if err != nil {
		return 0, err
	}
	length, unique := 0, len(v.lexemes)
	if normalization&3 != 0 {
		length, _, err = v.rankLengths(ctx)
		if err != nil {
			return 0, err
		}
	}
	if normalization&1 != 0 {
		score /= math.Log(float64(length + 1))
	}
	if normalization&2 != 0 && length > 0 {
		score /= float64(length)
	}
	if normalization&4 != 0 && extents > 0 && sumDistance > 0 {
		score /= float64(extents) / sumDistance
	}
	if normalization&8 != 0 {
		score /= float64(unique)
	}
	if normalization&16 != 0 {
		score /= math.Log(float64(unique+1)) / math.Log(2)
	}
	if normalization&32 != 0 {
		score /= score + 1
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return float32(score), nil
}

func (v *Vector) rankDocument(ctx context.Context, q *Query) ([]rankDocumentEntry, []rankOccurrence, error) {
	var occurrences []rankOccurrence
	for node, n := range q.nodes {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if n.op != 'v' {
			continue
		}
		start, end, err := v.rankEntries(ctx, n)
		if err != nil {
			return nil, nil, err
		}
		for entry := start; entry < end; entry++ {
			for _, p := range v.lexemes[entry].positions {
				if err := ctx.Err(); err != nil {
					return nil, nil, err
				}
				if n.mask == 0 || n.mask&(1<<p.weight) != 0 {
					occurrences = append(occurrences, rankOccurrence{p, entry, node})
				}
			}
		}
	}
	if len(occurrences) == 0 {
		return nil, nil, ctx.Err()
	}
	if err := stableSort(ctx, occurrences, func(a, b rankOccurrence) int {
		if a.position.number != b.position.number {
			return int(a.position.number) - int(b.position.number)
		}
		if a.position.weight != b.position.weight {
			return int(a.position.weight) - int(b.position.weight)
		}
		return a.entry - b.entry
	}); err != nil {
		return nil, nil, err
	}
	document := make([]rankDocumentEntry, 0, len(occurrences))
	for start := 0; start < len(occurrences); {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		end := start + 1
		for end < len(occurrences) && occurrences[end].entry == occurrences[start].entry && occurrences[end].position == occurrences[start].position {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			end++
		}
		document = append(document, rankDocumentEntry{occurrences[start].position, start, end})
		start = end
	}
	return document, occurrences, ctx.Err()
}

// Visit minimal covers without allocating a retained cover stream. The reducer
// owns its score/extent state; the same iterator admits exact source-stream units.
func rankCovers(ctx context.Context, q *Query, document []rankDocumentEntry, occurrences []rankOccurrence, visit func(begin, end int) error) error {
	positions := make([][]int, len(q.nodes))
	reverse := false
	reset := func(backward bool) error {
		reverse = backward
		for i := range positions {
			if err := ctx.Err(); err != nil {
				return err
			}
			positions[i] = positions[i][:0]
		}
		return nil
	}
	fill := func(entry rankDocumentEntry) error {
		for _, item := range occurrences[entry.first:entry.last] {
			if err := ctx.Err(); err != nil {
				return err
			}
			list := positions[item.node]
			number := int(entry.position.number)
			if len(list) == 0 || list[len(list)-1] != number {
				positions[item.node] = append(list, number)
			}
		}
		return nil
	}
	leaf := func(ctx context.Context, index int, _ queryNode, positional bool) (matchResult, error) {
		if err := ctx.Err(); err != nil {
			return matchResult{}, err
		}
		list := positions[index]
		if len(list) == 0 {
			return matchResult{}, nil
		}
		if !positional {
			return matchTruth(true), nil
		}
		return matchResult{state: matchYes, ends: list, reverse: reverse}, nil
	}
	for next := 0; next < len(document); {
		if err := reset(false); err != nil {
			return err
		}
		end := -1
		for i := next; i < len(document); i++ {
			if err := fill(document[i]); err != nil {
				return err
			}
			matches, err := q.matchWithLeaf(ctx, leaf)
			if err != nil {
				return err
			}
			if matches {
				end = i
				break
			}
		}
		if end < 0 {
			break
		}
		if err := reset(true); err != nil {
			return err
		}
		begin := -1
		for i := end; i >= next; i-- {
			if err := fill(document[i]); err != nil {
				return err
			}
			matches, err := q.matchWithLeaf(ctx, leaf)
			if err != nil {
				return err
			}
			if matches {
				begin = i
				break
			}
		}
		if begin < 0 {
			next++
			continue
		}
		if err := visit(begin, end); err != nil {
			return err
		}
		next = begin + 1
	}
	return ctx.Err()
}
