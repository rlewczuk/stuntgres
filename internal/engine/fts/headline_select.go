package fts

import (
	"context"

	"stuntgres/internal/util"
)

func headlineInteresting(w headlineWord) bool { return w.item >= 0 && !w.repeated }
func headlineSkip(kind int) bool              { return kind == 5 || kind == 15 || kind == 16 || kind == 17 }
func headlineNonword(kind int) bool           { return kind == 12 || kind == 13 || headlineSkip(kind) }
func headlineBad(words []headlineWord, index, short int) bool {
	if index < 0 || index >= len(words) {
		return true
	}
	w := words[index]
	noEnd := headlineNonword(w.kind) || w.kind == 7 || w.kind == 8 || w.kind == 20 || w.kind == 21 || w.kind == 22 || w.kind == 14 || w.kind == 23
	return (noEnd || len(w.text) <= short) && !headlineInteresting(w)
}
func markHeadlineFragment(ctx context.Context, words []headlineWord, all bool, start, end int) error {
	for i := start; i <= end; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		w := &words[i]
		w.selected = w.item >= 0
		w.replace = !all && w.kind == 13
		w.skip = headlineSkip(w.kind)
		w.in = !w.repeated
	}
	return ctx.Err()
}
func headlineFallback(ctx context.Context, words []headlineWord, options HeadlineOptions) error {
	count, end := 0, -1
	for i := 0; i < len(words) && count < options.MinWords; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !headlineNonword(words[i].kind) {
			count++
		}
		end = i
	}
	return markHeadlineFragment(ctx, words, options.HighlightAll, 0, end)
}
func markHeadlineWords(ctx context.Context, q *Query, words []headlineWord, locations []matchResult, o HeadlineOptions) error {
	if o.HighlightAll {
		return markHeadlineFragment(ctx, words, true, 0, len(words)-1)
	}
	next, bestStart, bestEnd, bestCount := 0, -1, -1, -1
	bestCover := false
	for {
		p, r, found, err := headlineCover(ctx, q, words, locations, &next)
		if err != nil {
			return err
		}
		if !found {
			break
		}
		length, interesting, start, end := 0, 0, p, p
		i := p
		for ; i <= r && length < o.MaxWords; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !headlineNonword(words[i].kind) {
				length++
			}
			if headlineInteresting(words[i]) {
				interesting++
			}
			end = i
		}
		if length < o.MaxWords {
			for i = i - 1; i < len(words) && length < o.MaxWords; i++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				if i > r {
					if !headlineNonword(words[i].kind) {
						length++
					}
					if headlineInteresting(words[i]) {
						interesting++
					}
				}
				end = i
				if headlineBad(words, i, o.ShortWord) {
					continue
				}
				if length >= o.MinWords {
					break
				}
			}
			if length < o.MinWords {
				for i = p - 1; i >= 0; i-- {
					if err := ctx.Err(); err != nil {
						return err
					}
					if !headlineNonword(words[i].kind) {
						length++
					}
					if headlineInteresting(words[i]) {
						interesting++
					}
					if length >= o.MaxWords {
						break
					}
					if headlineBad(words, i, o.ShortWord) {
						continue
					}
					if length >= o.MinWords {
						break
					}
				}
				start = max(i, 0)
			}
		} else {
			if i > r {
				i = r
			}
			for ; length > o.MinWords; i-- {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !headlineBad(words, i, o.ShortWord) {
					break
				}
				if !headlineNonword(words[i].kind) {
					length--
				}
				if headlineInteresting(words[i]) {
					interesting--
				}
				end = i - 1
			}
		}
		whole := start <= p && end >= r
		if whole && !bestCover || whole == bestCover && (interesting > bestCount || interesting == bestCount && !headlineBad(words, end, o.ShortWord) && headlineBad(words, bestEnd, o.ShortWord)) {
			bestStart, bestEnd, bestCount, bestCover = start, end, interesting, whole
		}
	}
	if bestCount < 0 {
		return headlineFallback(ctx, words, o)
	}
	return markHeadlineFragment(ctx, words, false, bestStart, bestEnd)
}

type headlineFragment struct {
	start, end, length, interesting int
	chosen, excluded                bool
}

func nextHeadlineFragment(ctx context.Context, words []headlineWord, start, end, maxWords int) (headlineFragment, error) {
	f := headlineFragment{start: start, end: end}
	for i := start; i <= end; i++ {
		if err := ctx.Err(); err != nil {
			return headlineFragment{}, err
		}
		f.start = i
		if headlineInteresting(words[i]) {
			break
		}
	}
	i := f.start
	for ; i <= f.end && f.length < maxWords; i++ {
		if err := ctx.Err(); err != nil {
			return headlineFragment{}, err
		}
		if !headlineNonword(words[i].kind) {
			f.length++
		}
		if headlineInteresting(words[i]) {
			f.interesting++
		}
	}
	if f.end > i {
		f.end = i
		for i = f.end; i >= f.start; i-- {
			if err := ctx.Err(); err != nil {
				return headlineFragment{}, err
			}
			f.end = i
			if headlineInteresting(words[i]) {
				break
			}
			if !headlineNonword(words[i].kind) {
				f.length--
			}
		}
	}
	return f, nil
}
func markHeadlineFragments(ctx context.Context, q *Query, words []headlineWord, locations []matchResult, o HeadlineOptions) error {
	var fragments []headlineFragment
	next := 0
	for {
		p, r, found, err := headlineCover(ctx, q, words, locations, &next)
		if err != nil {
			return err
		}
		if !found {
			break
		}
		for start := p; start <= r; {
			f, err := nextHeadlineFragment(ctx, words, start, r, o.MaxWords)
			if err != nil {
				return err
			}
			fragments = append(fragments, f)
			start = f.end + 1
		}
	}
	chosen := 0
	for range max(0, o.MaxFragments) {
		best := -1
		for i, f := range fragments {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !f.chosen && !f.excluded && (best < 0 || f.interesting > fragments[best].interesting || f.interesting == fragments[best].interesting && f.length < fragments[best].length) {
				best = i
			}
		}
		if best < 0 {
			break
		}
		f := fragments[best]
		f.chosen = true
		if f.length < o.MaxWords {
			stretch, limit, marker := 0, (o.MaxWords-f.length)/2, f.start
			i := f.start - 1
			for ; i >= 0 && stretch < limit && !words[i].in; i-- {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !headlineNonword(words[i].kind) {
					f.length++
					stretch++
				}
				marker = i
			}
			for i = marker; i < f.start && headlineBad(words, i, o.ShortWord); i++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !headlineNonword(words[i].kind) {
					f.length--
				}
			}
			f.start = i
			marker = f.end
			for i = f.end + 1; i < len(words) && f.length < o.MaxWords && !words[i].in; i++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !headlineNonword(words[i].kind) {
					f.length++
				}
				marker = i
			}
			for i = marker; i > f.end && headlineBad(words, i, o.ShortWord); i-- {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !headlineNonword(words[i].kind) {
					f.length--
				}
			}
			f.end = i
		}
		fragments[best] = f
		if err := markHeadlineFragment(ctx, words, o.HighlightAll, f.start, f.end); err != nil {
			return err
		}
		chosen++
		if chosen == 1 {
			if err := util.ExecutionCheckpoint(ctx, "text_search.headline.select"); err != nil {
				return err
			}
		}
		for i, other := range fragments {
			if err := ctx.Err(); err != nil {
				return err
			}
			if i != best && other.start <= f.end && other.end >= f.start {
				fragments[i].excluded = true
			}
		}
	}
	if chosen == 0 {
		return headlineFallback(ctx, words, o)
	}
	return ctx.Err()
}
