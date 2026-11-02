package storage

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// GinPosting identifies the exact simultaneously published heap version/slot.
type GinPosting struct {
	Row      ids.RowID
	Version  ids.VersionID
	Position int
}
type GinPostingList struct {
	Token    sqltype.GinToken
	Postings []GinPosting
}
type GinColumn struct {
	Binding sqltype.GinBinding
	Tokens  []GinPostingList
	NonNull []GinPosting
}
type GinRoot struct{ Columns []GinColumn }
type ginTuple struct {
	token   sqltype.GinToken
	posting GinPosting
}

func compareGinPosting(a, b GinPosting) int {
	if a.Row < b.Row {
		return -1
	}
	if a.Row > b.Row {
		return 1
	}
	if a.Version < b.Version {
		return -1
	}
	if a.Version > b.Version {
		return 1
	}
	if a.Position < b.Position {
		return -1
	}
	if a.Position > b.Position {
		return 1
	}
	return 0
}

func sortGinTuples(ctx context.Context, tuples []ginTuple, binding sqltype.GinBinding, input sqltype.InputEnvironment) error {
	scratch := make([]ginTuple, len(tuples))
	for width := 1; width < len(tuples); width *= 2 {
		for start := 0; start < len(tuples); start += 2 * width {
			mid, end := min(start+width, len(tuples)), min(start+2*width, len(tuples))
			i, j := start, mid
			for k := start; k < end; k++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				take := j == end
				if !take && i < mid {
					c, err := binding.Compare(ctx, tuples[i].token, tuples[j].token, input)
					if err != nil {
						return err
					}
					take = c < 0 || c == 0 && compareGinPosting(tuples[i].posting, tuples[j].posting) <= 0
				}
				if take {
					scratch[k] = tuples[i]
					i++
				} else {
					scratch[k] = tuples[j]
					j++
				}
			}
		}
		copy(tuples, scratch)
	}
	return ctx.Err()
}

// BuildGin extracts each already-produced key once, then sorts/deduplicates the
// batch. Neither heap expressions nor predicate callbacks are replayed here.
func BuildGin(ctx context.Context, bindings []sqltype.GinBinding, entries []IndexEntry, input sqltype.InputEnvironment) (*GinRoot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root := &GinRoot{Columns: make([]GinColumn, len(bindings))}
	for ordinal, binding := range bindings {
		column := &root.Columns[ordinal]
		column.Binding = binding
		var tuples []ginTuple
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if len(entry.Keys) != len(bindings) {
				return nil, fmt.Errorf("stuntgres: invalid GIN key arity")
			}
			posting := GinPosting{entry.Row, entry.Version, entry.Position}
			value := entry.Keys[ordinal]
			if !value.IsNull() {
				column.NonNull = append(column.NonNull, posting)
			}
			if err := binding.Extract(ctx, value, func(token sqltype.GinToken) error { tuples = append(tuples, ginTuple{token, posting}); return nil }); err != nil {
				return nil, err
			}
		}
		if err := sortGinTuples(ctx, tuples, binding, input); err != nil {
			return nil, err
		}
		if err := sortGinPostings(ctx, column.NonNull); err != nil {
			return nil, err
		}
		for _, tuple := range tuples {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			n := len(column.Tokens)
			c := 1
			if n > 0 {
				var err error
				c, err = binding.Compare(ctx, column.Tokens[n-1].Token, tuple.token, input)
				if err != nil {
					return nil, err
				}
			}
			if c != 0 {
				column.Tokens = append(column.Tokens, GinPostingList{Token: tuple.token})
				n++
			}
			list := &column.Tokens[n-1]
			if len(list.Postings) == 0 || list.Postings[len(list.Postings)-1] != tuple.posting {
				list.Postings = append(list.Postings, tuple.posting)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return root, nil
}

func mergeGinPostings(ctx context.Context, old, added []GinPosting, ended map[ids.RowID]ids.VersionID, materialize bool) ([]GinPosting, error) {
	changed := len(added) > 0
	if !changed {
		for _, p := range old {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if ended[p.Row] == p.Version {
				changed = true
				break
			}
		}
	}
	if !changed {
		return old, ctx.Err()
	}
	if !materialize {
		// Posting identity/order comparisons are infallible. Preserve traversal
		// cancellation without allocating a survivor list for admission alone.
		for range old {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		for range added {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		return nil, ctx.Err()
	}
	out := make([]GinPosting, 0, len(old)+len(added))
	j := 0
	accept := func(p GinPosting) {
		if len(out) == 0 || out[len(out)-1] != p {
			out = append(out, p)
		}
	}
	for _, p := range old {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if ended[p.Row] == p.Version {
			continue
		}
		for j < len(added) && compareGinPosting(added[j], p) < 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			accept(added[j])
			j++
		}
		accept(p)
	}
	for ; j < len(added); j++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		accept(added[j])
	}
	return out, ctx.Err()
}

// MergeGin merges each affected dictionary/list once and shares unchanged
// posting backing. Ended predecessors are exact versions, not SQL row values.
func MergeGin(ctx context.Context, old *GinRoot, added []IndexEntry, ended map[ids.RowID]ids.VersionID, input sqltype.InputEnvironment) (*GinRoot, error) {
	return mergeGin(ctx, old, added, ended, input, true)
}

// ValidateGinMerge performs the same fallible extraction/comparison work as a
// merge, retaining only delta-sized scratch. No installation payload escapes.
func ValidateGinMerge(ctx context.Context, old *GinRoot, added []IndexEntry, ended map[ids.RowID]ids.VersionID, input sqltype.InputEnvironment) error {
	_, err := mergeGin(ctx, old, added, ended, input, false)
	return err
}

func mergeGin(ctx context.Context, old *GinRoot, added []IndexEntry, ended map[ids.RowID]ids.VersionID, input sqltype.InputEnvironment, materialize bool) (*GinRoot, error) {
	if old == nil {
		return nil, fmt.Errorf("stuntgres: missing GIN predecessor")
	}
	bindings := make([]sqltype.GinBinding, len(old.Columns))
	for i := range bindings {
		bindings[i] = old.Columns[i].Binding
	}
	delta, err := BuildGin(ctx, bindings, added, input)
	if err != nil {
		return nil, err
	}
	var out *GinRoot
	if materialize {
		out = &GinRoot{Columns: make([]GinColumn, len(bindings))}
	}
	for i, prior := range old.Columns {
		nonnull, err := mergeGinPostings(ctx, prior.NonNull, delta.Columns[i].NonNull, ended, materialize)
		if err != nil {
			return nil, err
		}
		var column *GinColumn
		if materialize {
			column = &out.Columns[i]
			column.Binding, column.NonNull = prior.Binding, nonnull
		}
		next := delta.Columns[i].Tokens
		j := 0
		for _, list := range prior.Tokens {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			var additions []GinPosting
			for j < len(next) {
				c, e := prior.Binding.Compare(ctx, next[j].Token, list.Token, input)
				if e != nil {
					return nil, e
				}
				if c > 0 {
					break
				}
				if c == 0 {
					additions = next[j].Postings
					j++
					break
				}
				if materialize {
					column.Tokens = append(column.Tokens, next[j])
				}
				j++
			}
			postings, e := mergeGinPostings(ctx, list.Postings, additions, ended, materialize)
			if e != nil {
				return nil, e
			}
			if materialize && len(postings) > 0 {
				column.Tokens = append(column.Tokens, GinPostingList{list.Token, postings})
			}
		}
		if materialize {
			column.Tokens = append(column.Tokens, next[j:]...)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Lookup performs actual binary dictionary lookup; it never scans Entries.
func (c GinColumn) Lookup(ctx context.Context, token sqltype.GinToken, input sqltype.InputEnvironment) ([]GinPosting, error) {
	lo, hi := 0, len(c.Tokens)
	for lo < hi {
		mid := lo + (hi-lo)/2
		order, err := c.Binding.Compare(ctx, c.Tokens[mid].Token, token, input)
		if err != nil {
			return nil, err
		}
		if order < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(c.Tokens) {
		order, err := c.Binding.Compare(ctx, c.Tokens[lo].Token, token, input)
		if err != nil {
			return nil, err
		}
		if order == 0 {
			return c.Tokens[lo].Postings, nil
		}
	}
	return nil, ctx.Err()
}

// TransportGin is a pure structural filter/remap. It never calls type providers,
// key recipes or predicates, and retains untouched token/posting backing.
func TransportGin(ctx context.Context, old *GinRoot, move func(GinPosting) (GinPosting, bool, error)) (*GinRoot, error) {
	if old == nil {
		return nil, fmt.Errorf("stuntgres: missing GIN transport payload")
	}
	out := &GinRoot{Columns: make([]GinColumn, len(old.Columns))}
	changed := false
	mapList := func(prior []GinPosting) ([]GinPosting, error) {
		var next []GinPosting
		for i, p := range prior {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			n, keep, err := move(p)
			if err != nil {
				return nil, err
			}
			if next == nil && (!keep || n != p) {
				next = make([]GinPosting, 0, len(prior))
				next = append(next, prior[:i]...)
			}
			if next != nil && keep {
				next = append(next, n)
			}
		}
		if next == nil {
			return prior, ctx.Err()
		}
		changed = true
		return next, ctx.Err()
	}
	for i, prior := range old.Columns {
		c := &out.Columns[i]
		c.Binding = prior.Binding
		var err error
		c.NonNull, err = mapList(prior.NonNull)
		if err != nil {
			return nil, err
		}
		c.Tokens = make([]GinPostingList, 0, len(prior.Tokens))
		for _, list := range prior.Tokens {
			p, e := mapList(list.Postings)
			if e != nil {
				return nil, e
			}
			if len(p) > 0 {
				c.Tokens = append(c.Tokens, GinPostingList{list.Token, p})
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !changed {
		return old, nil
	}
	return out, nil
}

func sortGinPostings(ctx context.Context, postings []GinPosting) error {
	scratch := make([]GinPosting, len(postings))
	for width := 1; width < len(postings); width *= 2 {
		for start := 0; start < len(postings); start += 2 * width {
			mid, end := min(start+width, len(postings)), min(start+2*width, len(postings))
			i, j := start, mid
			for k := start; k < end; k++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				if j == end || i < mid && compareGinPosting(postings[i], postings[j]) <= 0 {
					scratch[k] = postings[i]
					i++
				} else {
					scratch[k] = postings[j]
					j++
				}
			}
		}
		copy(postings, scratch)
	}
	return ctx.Err()
}

// CombineGinPostings combines sorted immutable candidates without mutating or
// coalescing distinct row versions. Every returned candidate requires recheck.
func CombineGinPostings(ctx context.Context, left, right []GinPosting, intersect bool) ([]GinPosting, error) {
	out := make([]GinPosting, 0, len(left)+len(right))
	i, j := 0, 0
	for i < len(left) || j < len(right) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if i == len(left) {
			if !intersect {
				out = append(out, right[j:]...)
			}
			break
		}
		if j == len(right) {
			if !intersect {
				out = append(out, left[i:]...)
			}
			break
		}
		c := compareGinPosting(left[i], right[j])
		if c == 0 {
			out = append(out, left[i])
			i++
			j++
		} else if c < 0 {
			if !intersect {
				out = append(out, left[i])
			}
			i++
		} else {
			if !intersect {
				out = append(out, right[j])
			}
			j++
		}
	}
	return out, ctx.Err()
}
