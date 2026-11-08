package index

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// GinJSONBCandidates consumes posting lookups only. Recheck always belongs to
// the existing scalar jsonb/jsonpath predicate; every returned set is a sound
// superset of the SQL matches.
func GinJSONBCandidates(ctx context.Context, root *storage.GinRoot, ordinal int, strategy int, query sqltype.Value, input sqltype.InputEnvironment) ([]storage.GinPosting, error) {
	if root == nil || ordinal < 0 || ordinal >= len(root.Columns) {
		return nil, fmt.Errorf("stuntgres: invalid GIN probe ordinal")
	}
	main, err := ginJSONBCandidates(ctx, root.Columns[ordinal], strategy, query, input)
	if err != nil || len(root.Pending) == 0 {
		return main, err
	}
	pending, err := ginJSONBCandidates(ctx, root.Pending[ordinal], strategy, query, input)
	if err != nil {
		return nil, err
	}
	return storage.CombineGinPostings(ctx, main, pending, false)
}

func ginJSONBCandidates(ctx context.Context, column storage.GinColumn, strategy int, query sqltype.Value, input sqltype.InputEnvironment) ([]storage.GinPosting, error) {
	if !column.Binding.IsJSONB() {
		return nil, fmt.Errorf("stuntgres: foreign GIN jsonb probe")
	}
	var tokens []sqltype.GinToken
	allNonNull, err := column.Binding.ExtractJSONBQueryWithEnvironment(ctx, int16(strategy), query, func(token sqltype.GinToken) error {
		tokens = append(tokens, token)
		return nil
	}, input)
	if err != nil {
		return nil, err
	}
	if allNonNull {
		return column.NonNull, ctx.Err()
	}
	if len(tokens) == 0 {
		return nil, ctx.Err()
	}
	// Strategies 7 (@>) and 11 (?&) require every key; 9 (?) and 10 (?|) need
	// any key. 15/16 never reach here: they conservatively return ALL.
	intersect := strategy == 7 || strategy == 11
	var result []storage.GinPosting
	for i, token := range tokens {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		postings, err := column.Lookup(ctx, token, input)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			result = postings
			continue
		}
		result, err = storage.CombineGinPostings(ctx, result, postings, intersect)
		if err != nil {
			return nil, err
		}
	}
	return result, ctx.Err()
}