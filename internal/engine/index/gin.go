package index

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func BindGinIndex(view catalog.Read, parent *catalog.Relation, d *catalog.IndexDefinition) ([]sqltype.GinBinding, error) {
	if d.Method != catalog.GinMethod || d.Unique || d.IsExclusion() || len(d.Include) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid GIN definition")
	}
	bindings := make([]sqltype.GinBinding, len(d.Keys))
	for i, key := range d.Keys {
		if key.Class.MethodOID() != uint32(d.MethodIdentity()) || key.Desc || key.NullsFirst {
			return nil, fmt.Errorf("stuntgres: invalid GIN key")
		}
		class, err := routine.BindClassSupport(view, key.Class)
		if err != nil {
			return nil, err
		}
		usage, err := KeyUsage(parent, key)
		if err != nil {
			return nil, err
		}
		bindings[i], err = sqltype.BindGinTokens(KeyCollation(usage, key), class)
		if err != nil {
			return nil, err
		}
		if err := validateClassOptions(view, key); err != nil {
			return nil, err
		}
	}
	return bindings, nil
}

func ginRoot(ctx context.Context, view catalog.Read, parent *catalog.Relation, d *catalog.IndexDefinition, entries []storage.IndexEntry, history any, input sqltype.InputEnvironment) (*storage.IndexRoot, error) {
	bindings, err := BindGinIndex(view, parent, d)
	if err != nil {
		return nil, err
	}
	payload, err := storage.BuildGin(ctx, bindings, entries, input)
	if err != nil {
		return nil, err
	}
	root := logicalIndexRoot(d, entries, history)
	root.Gin = payload
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return root, nil
}

func MergeGinIndexRoot(evaluation *exec.Context, parent, index *catalog.Relation, old *storage.IndexRoot, p *RowPublication, reconstruct, materialize bool) (*storage.IndexRoot, error) {
	if old == nil || old.Gin == nil {
		return nil, fmt.Errorf("stuntgres: missing GIN root")
	}
	bindings, err := BindGinIndex(evaluation.Catalog, parent, index.SQLIndex)
	if err != nil {
		return nil, err
	}
	if len(bindings) != len(old.Gin.Columns) {
		return nil, fmt.Errorf("stuntgres: GIN column count changed")
	}
	for i, binding := range bindings {
		if !binding.Same(old.Gin.Columns[i].Binding) {
			return nil, fmt.Errorf("stuntgres: GIN binding changed")
		}
	}
	program, err := BindIndexKeys(evaluation.Catalog, parent, index.SQLIndex)
	if err != nil {
		return nil, err
	}
	added, ended, err := collectIndexDelta(evaluation, parent, index, p, reconstruct, &program)
	if err != nil {
		return nil, err
	}
	changed := len(added) != 0
	if !changed {
		for _, entry := range old.Entries {
			if err := evaluation.Context.Err(); err != nil {
				return nil, err
			}
			if ended[entry.Version] == entry.Row {
				changed = true
				break
			}
		}
	}
	if !changed {
		return nil, evaluation.Context.Err()
	}
	if !materialize {
		return nil, storage.ValidateGinMerge(evaluation.Context, old.Gin, added, ended, evaluation.Input)
	}
	payload, err := storage.MergeGin(evaluation.Context, old.Gin, added, ended, evaluation.Input)
	if err != nil {
		return nil, err
	}
	entries := make([]storage.IndexEntry, 0, len(old.Entries)+len(added))
	for _, entry := range old.Entries {
		if err := evaluation.Context.Err(); err != nil {
			return nil, err
		}
		if ended[entry.Version] != entry.Row {
			entries = append(entries, entry)
		}
	}
	entries = append(entries, added...)
	root := logicalIndexRoot(index.SQLIndex, entries, old.Construction)
	root.Gin = payload
	if err := evaluation.Context.Err(); err != nil {
		return nil, err
	}
	return root, nil
}

// GinArrayCandidates consumes posting lookups only. Recheck always belongs to
// the existing scalar predicate, including shape/bounds/order and NULL truth.
func GinArrayCandidates(ctx context.Context, column storage.GinColumn, query sqltype.Value, strategy int, input sqltype.InputEnvironment) ([]storage.GinPosting, error) {
	if query.Type() != column.Binding.Usage.Type || column.Binding.Class.InputOID() != 2277 {
		return nil, fmt.Errorf("stuntgres: foreign GIN array probe")
	}
	if query.IsNull() {
		return nil, ctx.Err()
	}
	if strategy == 3 || strategy == 4 {
		return column.NonNull, ctx.Err()
	}
	if strategy != 1 && strategy != 2 {
		return nil, fmt.Errorf("stuntgres: invalid GIN array strategy")
	}
	var result []storage.GinPosting
	first := true
	null := false
	err := column.Binding.Extract(ctx, query, func(token sqltype.GinToken) error {
		if token.Kind == sqltype.GinEmptyItem {
			return nil
		}
		if token.Kind == sqltype.GinNullElement {
			null = true
			return nil
		}
		postings, err := column.Lookup(ctx, token, input)
		if err != nil {
			return err
		}
		if first {
			result = postings
			first = false
			return nil
		}
		result, err = storage.CombineGinPostings(ctx, result, postings, strategy == 2)
		return err
	})
	if err != nil {
		return nil, err
	}
	if strategy == 2 && null {
		return nil, ctx.Err()
	}
	if first && strategy == 2 {
		return column.NonNull, ctx.Err()
	}
	return result, ctx.Err()
}

// GinLexemeCandidates is only an exact/prefix dictionary probe. It is not a
// tsquery evaluator; weights, Boolean truth and positions remain scalar-owned.
func GinLexemeCandidates(ctx context.Context, column storage.GinColumn, lexeme string, prefix, allNonNull bool, input sqltype.InputEnvironment) ([]storage.GinPosting, error) {
	if column.Binding.Class.InputOID() != uint32(sqltype.TSVector) {
		return nil, fmt.Errorf("stuntgres: non-vector GIN lexeme probe")
	}
	if allNonNull {
		return column.NonNull, ctx.Err()
	}
	if !prefix {
		return column.Lookup(ctx, sqltype.GinToken{Lexeme: lexeme}, input)
	}
	lo, hi := 0, len(column.Tokens)
	for lo < hi {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mid := lo + (hi-lo)/2
		token := column.Tokens[mid].Token
		if token.Kind == sqltype.GinOrdinary && token.Lexeme < lexeme {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	var out []storage.GinPosting
	for i := lo; i < len(column.Tokens); i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		list := column.Tokens[i]
		if list.Token.Kind != sqltype.GinOrdinary || !strings.HasPrefix(list.Token.Lexeme, lexeme) {
			break
		}
		if out == nil {
			out = list.Postings
			continue
		}
		var err error
		out, err = storage.CombineGinPostings(ctx, out, list.Postings, false)
		if err != nil {
			return nil, err
		}
	}
	return out, ctx.Err()
}
