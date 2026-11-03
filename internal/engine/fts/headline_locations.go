package fts

import "context"

type headlineLocationLink struct {
	value matchResult
	next  int
}
type headlineLocationList struct{ first, last int }

func headlineLeaf(ctx context.Context, words []headlineWord, index int, positional bool) (matchResult, error) {
	var result matchResult
	for i, word := range words {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return matchResult{}, err
			}
		}
		if word.item != index {
			continue
		}
		if !positional {
			return matchTruth(true), nil
		}
		if len(result.ends) == 0 || result.ends[len(result.ends)-1] < word.position {
			result.ends = append(result.ends, word.position)
		}
	}
	if len(result.ends) > 0 {
		result.state = matchYes
	}
	return result, ctx.Err()
}

// headlineLocations is the iterative TS_execute_locations counterpart: AND
// concatenates streams, OR distributes unions without shifting their ends,
// NOT contributes no positive stream, and phrases retain their own widths.
func (q *Query) headlineLocations(ctx context.Context, words []headlineWord) ([]matchResult, error) {
	if q.Empty() {
		return nil, ctx.Err()
	}
	positional := make([]matchResult, len(q.nodes))
	truth := make([]bool, len(q.nodes))
	lists := make([]headlineLocationList, len(q.nodes))
	for i := range lists {
		lists[i] = headlineLocationList{-1, -1}
	}
	var links []headlineLocationLink
	add := func(list *headlineLocationList, value matchResult) {
		index := len(links)
		links = append(links, headlineLocationLink{value, -1})
		if list.last < 0 {
			list.first = index
		} else {
			links[list.last].next = index
		}
		list.last = index
	}
	for i, n := range q.nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if n.op == 'v' {
			value, err := headlineLeaf(ctx, words, i, true)
			if err != nil {
				return nil, err
			}
			positional[i] = value
			truth[i] = value.state == matchYes
			if truth[i] {
				add(&lists[i], value)
			}
			continue
		}
		if n.op == '!' {
			value := positional[n.right]
			if value.state == matchNo {
				value.state, value.complement = matchYes, true
			} else {
				value.complement = !value.complement
				if !value.complement && value.len() == 0 {
					value.state = matchNo
				}
			}
			positional[i] = value
			truth[i] = !truth[n.right]
			continue
		}
		value, err := matchCombine(ctx, n.op, int(n.distance), positional[n.left], positional[n.right])
		if err != nil {
			return nil, err
		}
		positional[i] = value
		left, right := lists[n.left], lists[n.right]
		switch n.op {
		case '<':
			truth[i] = value.state == matchYes
			if truth[i] && !value.complement {
				add(&lists[i], value)
			}
		case '&':
			truth[i] = truth[n.left] && truth[n.right]
			if truth[i] {
				if left.first < 0 {
					lists[i] = right
				} else if right.first < 0 {
					lists[i] = left
				} else {
					links[left.last].next = right.first
					lists[i] = headlineLocationList{left.first, right.last}
				}
			}
		case '|':
			truth[i] = truth[n.left] || truth[n.right]
			if !truth[i] {
				continue
			}
			if left.first < 0 {
				lists[i] = right
				continue
			}
			if right.first < 0 {
				lists[i] = left
				continue
			}
			for l := left.first; l >= 0; l = links[l].next {
				for r := right.first; r >= 0; r = links[r].next {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					a, b := links[l].value, links[r].value
					width := max(a.width, b.width)
					a.width, b.width = 0, 0
					merged, err := matchCombine(ctx, '|', 0, a, b)
					if err != nil {
						return nil, err
					}
					merged.width = width
					add(&lists[i], merged)
				}
			}
		}
	}
	if !truth[q.root] {
		return nil, ctx.Err()
	}
	var result []matchResult
	for link := lists[q.root].first; link >= 0; link = links[link].next {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result = append(result, links[link].value)
	}
	return result, ctx.Err()
}

func headlineCover(ctx context.Context, q *Query, words []headlineWord, locations []matchResult, next *int) (int, int, bool, error) {
	pos := *next
	for {
		if err := ctx.Err(); err != nil {
			return 0, 0, false, err
		}
		end := -1
		for _, location := range locations {
			first := -1
			for i := range location.len() {
				if err := ctx.Err(); err != nil {
					return 0, 0, false, err
				}
				if location.at(i) >= pos {
					first = location.at(i)
					break
				}
			}
			if first < 0 {
				return 0, 0, false, nil
			}
			end = max(end, first)
		}
		if end < 0 {
			return 0, 0, false, nil
		}
		begin := int(^uint(0)>>1) - 1
		for _, location := range locations {
			last := -1
			for i := location.len() - 1; i >= 0; i-- {
				if err := ctx.Err(); err != nil {
					return 0, 0, false, err
				}
				if location.at(i)-location.width <= end {
					last = location.at(i) - location.width
					break
				}
			}
			begin = min(begin, last)
		}
		begin = max(begin, pos)
		if begin <= end {
			first, last := -1, -1
			for i, word := range words {
				if err := ctx.Err(); err != nil {
					return 0, 0, false, err
				}
				if word.item < 0 {
					continue
				}
				if first < 0 && word.position >= begin {
					first = i
				}
				if word.position <= end {
					last = i
				} else {
					break
				}
			}
			if first >= 0 && last >= first {
				matches, err := q.matchWithLeaf(ctx, func(ctx context.Context, index int, _ queryNode, positional bool) (matchResult, error) {
					return headlineLeaf(ctx, words[first:last+1], index, positional)
				})
				if err != nil {
					return 0, 0, false, err
				}
				if matches {
					*next = begin + 1
					return first, last, true, nil
				}
			}
		}
		pos = begin + 1
	}
}
