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

// Columns is the bulk/main inversion; Pending contains genuine deferred DML
// postings with the same bindings. An empty pending portion has no columns.
type GinRoot struct {
	Columns []GinColumn
	Pending []GinColumn
}
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
			if err := binding.ExtractWithEnvironment(ctx, value, func(token sqltype.GinToken) error { tuples = append(tuples, ginTuple{token, posting}); return nil }, input); err != nil {
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

func mergeGinPostings(ctx context.Context, old, added []GinPosting, ended map[ids.VersionID]ids.RowID, materialize bool) ([]GinPosting, error) {
	changed := len(added) > 0
	if !changed {
		for _, p := range old {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if ended[p.Version] == p.Row {
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
		if ended[p.Version] == p.Row {
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
func MergeGin(ctx context.Context, old *GinRoot, main, pending []IndexEntry, ended map[ids.VersionID]ids.RowID, input sqltype.InputEnvironment) (*GinRoot, error) {
	return mergeGin(ctx, old, main, pending, ended, input, true)
}

// ValidateGinMerge performs the same fallible extraction/comparison work as a
// merge, retaining only delta-sized scratch. No installation payload escapes.
func ValidateGinMerge(ctx context.Context, old *GinRoot, main, pending []IndexEntry, ended map[ids.VersionID]ids.RowID, input sqltype.InputEnvironment) error {
	_, err := mergeGin(ctx, old, main, pending, ended, input, false)
	return err
}

func mergeGin(ctx context.Context, old *GinRoot, main, pending []IndexEntry, ended map[ids.VersionID]ids.RowID, input sqltype.InputEnvironment, materialize bool) (*GinRoot, error) {
	if old == nil {
		return nil, fmt.Errorf("stuntgres: missing GIN predecessor")
	}
	bindings := make([]sqltype.GinBinding, len(old.Columns))
	for i := range bindings {
		bindings[i] = old.Columns[i].Binding
	}
	mainDelta, err := BuildGin(ctx, bindings, main, input)
	if err != nil {
		return nil, err
	}
	pendingDelta, err := BuildGin(ctx, bindings, pending, input)
	if err != nil {
		return nil, err
	}
	columns, err := mergeGinColumns(ctx, old.Columns, mainDelta.Columns, ended, input, materialize)
	if err != nil {
		return nil, err
	}
	priorPending := old.Pending
	if len(priorPending) == 0 && len(pending) != 0 {
		priorPending = make([]GinColumn, len(bindings))
		for i, binding := range bindings {
			priorPending[i].Binding = binding
		}
	}
	var deferred []GinColumn
	if len(priorPending) != 0 {
		deferred, err = mergeGinColumns(ctx, priorPending, pendingDelta.Columns, ended, input, materialize)
		if err != nil {
			return nil, err
		}
	}
	if !materialize {
		return nil, ctx.Err()
	}
	if !ginHasTokens(deferred) {
		deferred = nil
	}
	return &GinRoot{Columns: columns, Pending: deferred}, ctx.Err()
}

// mergeGinColumns consumes already extracted dictionaries. Cleanup and DML use
// the same traversal without reexecuting expressions or extracting keys twice.
func mergeGinColumns(ctx context.Context, old, delta []GinColumn, ended map[ids.VersionID]ids.RowID, input sqltype.InputEnvironment, materialize bool) ([]GinColumn, error) {
	if len(ended) == 0 {
		additions := false
		for _, column := range delta {
			additions = additions || len(column.Tokens) != 0
		}
		if !additions {
			return old, ctx.Err()
		}
	}
	var out []GinColumn
	if materialize {
		out = make([]GinColumn, len(old))
	}
	for i, prior := range old {
		nonnull, err := mergeGinPostings(ctx, prior.NonNull, delta[i].NonNull, ended, materialize)
		if err != nil {
			return nil, err
		}
		var column *GinColumn
		if materialize {
			column = &out[i]
			column.Binding, column.NonNull = prior.Binding, nonnull
		}
		next := delta[i].Tokens
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

// ginIncidences saturates rather than overflowing; the estimate is deliberately
// technical, not a count of PostgreSQL pages or indexed rows.
func ginIncidences(columns []GinColumn) int64 {
	var n int64
	const maximum = int64(^uint64(0) >> 1)
	for _, column := range columns {
		for _, token := range column.Tokens {
			if int64(len(token.Postings)) > maximum-n {
				return maximum
			}
			n += int64(len(token.Postings))
		}
	}
	return n
}

func GinPendingIncidences(root *GinRoot) int64 {
	if root == nil {
		return 0
	}
	return ginIncidences(root.Pending)
}

// GinPageEstimate counts technical units of 128 actual pending token-posting
// incidences; it is deliberately not a physical PostgreSQL page measurement.
func GinPageEstimate(incidences int64) int64 {
	pages := incidences / 128
	if incidences%128 != 0 {
		pages++
	}
	return pages
}

// CleanGin prepares a representation-only immutable transfer. Entries and
// construction provenance belong to IndexRoot and are never rebuilt here.
func CleanGin(ctx context.Context, old *GinRoot, input sqltype.InputEnvironment) (*GinRoot, int64, error) {
	if old == nil {
		return nil, 0, fmt.Errorf("stuntgres: missing GIN predecessor")
	}
	n := GinPendingIncidences(old)
	if n == 0 {
		return old, 0, ctx.Err()
	}
	columns, err := mergeGinColumns(ctx, old.Columns, old.Pending, nil, input, true)
	if err != nil {
		return nil, 0, err
	}
	return &GinRoot{Columns: columns}, n, ctx.Err()
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
	mapList := func(prior []GinPosting) ([]GinPosting, bool, error) {
		var next []GinPosting
		changed := false
		for i, p := range prior {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			n, keep, err := move(p)
			if err != nil {
				return nil, false, err
			}
			if !changed && (!keep || n != p) {
				next = make([]GinPosting, 0, len(prior))
				next = append(next, prior[:i]...)
				changed = true
			}
			if changed && keep {
				next = append(next, n)
			}
		}
		if !changed {
			return prior, false, ctx.Err()
		}
		return next, true, ctx.Err()
	}
	var out *GinRoot
	for portion, columns := range [2][]GinColumn{old.Columns, old.Pending} {
		copied := false
		for i, prior := range columns {
			column := prior
			nonnull, changed, err := mapList(prior.NonNull)
			if err != nil {
				return nil, err
			}
			column.NonNull = nonnull
			tokensChanged := false
			for j, list := range prior.Tokens {
				postings, mapped, err := mapList(list.Postings)
				if err != nil {
					return nil, err
				}
				if mapped && !tokensChanged {
					column.Tokens = make([]GinPostingList, 0, len(prior.Tokens))
					column.Tokens = append(column.Tokens, prior.Tokens[:j]...)
					tokensChanged = true
				}
				if tokensChanged && len(postings) != 0 {
					column.Tokens = append(column.Tokens, GinPostingList{Token: list.Token, Postings: postings})
				}
			}
			if !changed && !tokensChanged {
				continue
			}
			if out == nil {
				out = &GinRoot{Columns: old.Columns, Pending: old.Pending}
			}
			if !copied {
				next := append([]GinColumn(nil), columns...)
				if portion == 0 {
					out.Columns = next
				} else {
					out.Pending = next
				}
				copied = true
			}
			if portion == 0 {
				out.Columns[i] = column
			} else {
				out.Pending[i] = column
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		return old, nil
	}
	if !ginHasTokens(out.Pending) {
		out.Pending = nil
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

// UnionGinPostingLists merges selected sorted immutable candidate lists with a
// typed cursor heap. It borrows a sole list; otherwise output is owned, sorted
// and deduplicated by the full physical Row/Version/Position identity.
// Work is O(P log k), with O(P+k) allocation, never repeated prefix copies.
func UnionGinPostingLists(ctx context.Context, lists []GinPostingList) ([]GinPosting, error) {
	total, count := 0, 0
	var single []GinPosting
	const maxInt = int(^uint(0) >> 1)
	for i := range lists {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		postings := lists[i].Postings
		if len(postings) == 0 {
			continue
		}
		if len(postings) > maxInt-total {
			return nil, fmt.Errorf("stuntgres: GIN posting union capacity overflow")
		}
		total += len(postings)
		count++
		single = postings
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	if count == 1 {
		return single, nil
	}
	heap := make([]ginPostingCursor, 0, count)
	for i := range lists {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if postings := lists[i].Postings; len(postings) != 0 {
			heap = append(heap, ginPostingCursor{postings: postings})
		}
	}
	for i := len(heap)/2 - 1; i >= 0; i-- {
		if err := siftGinPostingCursor(ctx, heap, i); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]GinPosting, 0, total)
	for len(heap) != 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next := heap[0].postings[0]
		if len(out) == 0 || out[len(out)-1] != next {
			out = append(out, next)
		}
		heap[0].postings = heap[0].postings[1:]
		if len(heap[0].postings) == 0 {
			last := len(heap) - 1
			heap[0] = heap[last]
			heap = heap[:last]
		}
		if len(heap) > 1 {
			if err := siftGinPostingCursor(ctx, heap, 0); err != nil {
				return nil, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type ginPostingCursor struct {
	postings []GinPosting
}

func siftGinPostingCursor(ctx context.Context, heap []ginPostingCursor, at int) error {
	cursor := heap[at]
	head := cursor.postings[0]
	for at < len(heap)/2 {
		if err := ctx.Err(); err != nil {
			return err
		}
		child := at*2 + 1
		if child+1 < len(heap) && compareGinPosting(heap[child+1].postings[0], heap[child].postings[0]) < 0 {
			child++
		}
		if compareGinPosting(head, heap[child].postings[0]) <= 0 {
			break
		}
		heap[at] = heap[child]
		at = child
	}
	heap[at] = cursor
	return nil
}

func ginHasTokens(columns []GinColumn) bool {
	for _, column := range columns {
		if len(column.Tokens) != 0 {
			return true
		}
	}
	return false
}
