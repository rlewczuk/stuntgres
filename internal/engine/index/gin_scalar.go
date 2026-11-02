package index

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// GinScalarCandidates selects only the exact/range dictionary posting lists.
// It is not a heap scan or SQL ordering path; visibility and any conservative
// recheck stay with the ordinary scalar predicate and its owning source.
func GinScalarCandidates(ctx context.Context, column storage.GinColumn, query sqltype.Value, strategy int, input sqltype.InputEnvironment) ([]storage.GinPosting, error) {
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
