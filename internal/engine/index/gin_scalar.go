package index

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// GinScalarCandidates selects exact/range dictionary postings from both genuine
// portions. A sole nonempty result remains borrowed; two results are combined
// by exact physical identity. Visibility and SQL recheck remain source-owned.
func GinScalarCandidates(ctx context.Context, root *storage.GinRoot, ordinal int, query sqltype.Value, strategy int, input sqltype.InputEnvironment) ([]storage.GinPosting, error) {
	if root == nil || ordinal < 0 || ordinal >= len(root.Columns) || len(root.Pending) != 0 && len(root.Pending) != len(root.Columns) {
		return nil, fmt.Errorf("stuntgres: invalid GIN scalar probe ordinal")
	}
	main, err := ginScalarCandidates(ctx, root.Columns[ordinal], query, strategy, input)
	if err != nil || len(root.Pending) == 0 {
		return main, err
	}
	if !root.Pending[ordinal].Binding.Same(root.Columns[ordinal].Binding) {
		return nil, fmt.Errorf("stuntgres: foreign GIN scalar pending binding")
	}
	pending, err := ginScalarCandidates(ctx, root.Pending[ordinal], query, strategy, input)
	if err != nil {
		return nil, err
	}
	if len(main) == 0 {
		return pending, nil
	}
	if len(pending) == 0 {
		return main, nil
	}
	combined, err := storage.CombineGinPostings(ctx, main, pending, false)
	if err != nil {
		return nil, err
	}
	return combined, nil
}

func ginScalarCandidates(ctx context.Context, column storage.GinColumn, query sqltype.Value, strategy int, input sqltype.InputEnvironment) ([]storage.GinPosting, error) {
	if !column.Binding.IsScalar() || query.Type() != column.Binding.Usage.Type {
		return nil, fmt.Errorf("stuntgres: foreign GIN scalar probe")
	}
	if strategy < 1 || strategy > 5 {
		return nil, fmt.Errorf("stuntgres: invalid GIN scalar strategy")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query.IsNull() {
		return nil, nil
	}
	token := sqltype.GinToken{Element: query}
	if strategy == 3 {
		postings, err := column.Lookup(ctx, token, input)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return postings, nil
	}
	// Ordinary typed tokens precede every placeholder category. Find that
	// boundary logarithmically, without inspecting heap or NonNull membership.
	lo, hi := 0, len(column.Tokens)
	for lo < hi {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mid := lo + (hi-lo)/2
		if column.Tokens[mid].Token.Kind == sqltype.GinOrdinary {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	ordinaryEnd := lo
	// <= and > need the upper bound; < and >= need the lower bound.
	upper := strategy == 2 || strategy == 5
	lo, hi = 0, ordinaryEnd
	for lo < hi {
		mid := lo + (hi-lo)/2
		cmp, err := column.Binding.Compare(ctx, column.Tokens[mid].Token, token, input)
		if err != nil {
			return nil, err
		}
		if cmp < 0 || upper && cmp == 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	start, end := 0, lo
	if strategy == 4 || strategy == 5 {
		start, end = lo, ordinaryEnd
	}
	return storage.UnionGinPostingLists(ctx, column.Tokens[start:end])
}
