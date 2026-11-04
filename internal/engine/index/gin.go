package index

import (
	"context"
	"fmt"
	"slices"
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
		if comparison, ok := bindings[i].ElementComparison(); ok {
			comparison, err = routine.CaptureComparisonSupports(view, comparison)
			if err != nil {
				return nil, err
			}
			bindings[i], err = bindings[i].WithElementComparison(comparison)
			if err != nil {
				return nil, err
			}
		}
		if err := validateClassOptions(view, key); err != nil {
			return nil, err
		}
	}
	return bindings, nil
}

// RebindImportedGinKeys retains destination support/search captures in the
// private replacement catalog after identity and expression remapping. Source
// captures were deliberately discarded; binding only a rebuilt root would leave
// its owning descriptor with different authority. Retained TEMP consumers are
// grafted afterward and are not part of this imported cohort.
func RebindImportedGinKeys(ctx context.Context, view catalog.Read) error {
	for _, descriptor := range view.Relations() {
		if descriptor.SQLIndex == nil || descriptor.SQLIndex.Method != catalog.GinMethod {
			continue
		}
		var replacement *catalog.Relation
		for ordinal, key := range descriptor.SQLIndex.Keys {
			if err := ctx.Err(); err != nil {
				return err
			}
			class, err := routine.BindClassSupport(view, key.Class)
			if err != nil {
				return err
			}
			if class == key.Class {
				continue
			}
			if replacement == nil {
				copy, definition := *descriptor, *descriptor.SQLIndex
				definition.Keys = slices.Clone(definition.Keys)
				copy.SQLIndex = &definition
				replacement = &copy
			}
			replacement.SQLIndex.Keys[ordinal].Class = class
		}
		if replacement != nil {
			view.State().SetDescriptor(replacement.Address(), replacement)
		}
	}
	return ctx.Err()
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
	// Placement follows the exact accepted version, not today's reloptions.
	// Reconstructed entries without produced authority are bulk/main entries.
	placements := make(map[RowIdentity]bool, len(p.Writes))
	if !reconstruct {
		for _, row := range p.Writes {
			if output, ok := row.IndexOutputs[index.Address()]; ok && OutputMatches(output, index) && output.Member {
				placements[RowIdentity{Row: row.Row, Version: row.Version}] = output.GinPending
			}
		}
	}
	// GIN Entries is unordered: partition the existing delta in place rather
	// than allocate two additional full-key entry vectors.
	cut := 0
	for i, entry := range added {
		if !placements[RowIdentity{Row: entry.Row, Version: entry.Version}] {
			added[cut], added[i] = added[i], added[cut]
			cut++
		}
	}
	main, pending := added[:cut], added[cut:]
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
		return nil, storage.ValidateGinMerge(evaluation.Context, old.Gin, main, pending, ended, evaluation.FunctionInput())
	}
	payload, err := storage.MergeGin(evaluation.Context, old.Gin, main, pending, ended, evaluation.FunctionInput())
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
func GinArrayCandidates(ctx context.Context, root *storage.GinRoot, ordinal int, query sqltype.Value, strategy int, input sqltype.InputEnvironment) ([]storage.GinPosting, error) {
	if root == nil || ordinal < 0 || ordinal >= len(root.Columns) {
		return nil, fmt.Errorf("stuntgres: invalid GIN probe ordinal")
	}
	main, err := ginArrayCandidates(ctx, root.Columns[ordinal], query, strategy, input)
	if err != nil || len(root.Pending) == 0 {
		return main, err
	}
	pending, err := ginArrayCandidates(ctx, root.Pending[ordinal], query, strategy, input)
	if err != nil {
		return nil, err
	}
	return storage.CombineGinPostings(ctx, main, pending, false)
}

func ginArrayCandidates(ctx context.Context, column storage.GinColumn, query sqltype.Value, strategy int, input sqltype.InputEnvironment) ([]storage.GinPosting, error) {
	if query.Type() != column.Binding.Usage.Type || column.Binding.Class.InputOID() != 2277 {
		return nil, fmt.Errorf("stuntgres: foreign GIN array probe")
	}
	if query.IsNull() {
		return nil, ctx.Err()
	}
	if strategy == 3 || strategy == 4 {
		if err := column.Binding.ExtractQueryWithEnvironment(ctx, query, func(sqltype.GinToken) error { return ctx.Err() }, input); err != nil {
			return nil, err
		}
		return column.NonNull, ctx.Err()
	}
	if strategy != 1 && strategy != 2 {
		return nil, fmt.Errorf("stuntgres: invalid GIN array strategy")
	}
	var result []storage.GinPosting
	first := true
	null := false
	err := column.Binding.ExtractQueryWithEnvironment(ctx, query, func(token sqltype.GinToken) error {
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
	}, input)
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
func GinLexemeCandidates(ctx context.Context, root *storage.GinRoot, ordinal int, lexeme string, prefix, allNonNull bool, input sqltype.InputEnvironment) ([]storage.GinPosting, error) {
	if root == nil || ordinal < 0 || ordinal >= len(root.Columns) {
		return nil, fmt.Errorf("stuntgres: invalid GIN probe ordinal")
	}
	main, err := ginLexemeCandidates(ctx, root.Columns[ordinal], lexeme, prefix, allNonNull, input)
	if err != nil || len(root.Pending) == 0 {
		return main, err
	}
	pending, err := ginLexemeCandidates(ctx, root.Pending[ordinal], lexeme, prefix, allNonNull, input)
	if err != nil {
		return nil, err
	}
	return storage.CombineGinPostings(ctx, main, pending, false)
}

func ginLexemeCandidates(ctx context.Context, column storage.GinColumn, lexeme string, prefix, allNonNull bool, input sqltype.InputEnvironment) ([]storage.GinPosting, error) {
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
		comparison, err := column.Binding.Compare(ctx, token, sqltype.GinToken{Lexeme: lexeme}, input)
		if err != nil {
			return nil, err
		}
		if comparison < 0 {
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
