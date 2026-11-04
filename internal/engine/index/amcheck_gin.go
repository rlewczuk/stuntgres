package index

import (
	"context"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// CheckGin validates retained typed dictionaries and their exact posting owners.
// It does not build a replacement tree or replay retained heap expressions.
func CheckGin(evaluation *exec.Context, c AmcheckCapture) error {
	ctx := evaluation.Context
	if err := ctx.Err(); err != nil {
		return err
	}
	view := evaluation.Catalog
	if c.Index == nil || c.Parent == nil || c.Index.SQLIndex == nil || view.Owner() == nil || c.IndexRef.Domain() != view.Owner().Domain || c.ParentRef.Domain() != view.Owner().Domain || c.IndexRef.Epoch != view.Epoch() || c.ParentRef.Epoch != view.Epoch() || c.IndexRef.Address != c.Index.Address() || c.ParentRef.Address != c.Parent.Address() || view.Get(c.IndexRef.Address) != c.Index || view.Get(c.ParentRef.Address) != c.Parent || c.Index.SQLIndex.Parent != c.Parent.Address() {
		return ids.ErrCatalogReplaced
	}
	corrupt := func() error { return c.corrupt("GIN posting invariant violated") }
	if c.Root == nil || c.Root.Gin == nil || c.Root.Hash != nil || c.Root.Brin != nil || c.Root.Bloom != nil {
		return corrupt()
	}
	bindings, err := BindGinIndex(view, c.Parent, c.Index.SQLIndex)
	if err != nil {
		return err
	}
	if len(bindings) != len(c.Root.Gin.Columns) || len(c.Root.Gin.Pending) != 0 && len(c.Root.Gin.Pending) != len(bindings) {
		return corrupt()
	}
	positions := make(map[ids.VersionID]int, len(c.Root.Entries))
	for i, entry := range c.Root.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !c.keyShape(entry.Keys) || entry.Row == 0 || entry.Version == 0 || entry.Position < 0 || entry.Position >= len(c.Store.Versions) {
			return corrupt()
		}
		row := c.Store.Versions[entry.Position]
		if !c.rowShape(row) || row.Row != entry.Row || row.Version != entry.Version {
			return corrupt()
		}
		if _, exists := positions[entry.Version]; exists {
			return corrupt()
		}
		positions[entry.Version] = i
	}
	if err := c.checkMaps(ctx, positions); err != nil {
		return err
	}
	// Placement belongs to an entire entry, including every indexed column.
	// A version in both portions (even under disjoint tokens/columns) is corrupt.
	placements := make([]uint8, len(c.Root.Entries))
	portion := uint8(0)
	owns := func(p storage.GinPosting) bool {
		at, ok := positions[p.Version]
		if !ok {
			return false
		}
		e := c.Root.Entries[at]
		if e.Row != p.Row || e.Position != p.Position || placements[at] != 0 && placements[at] != portion {
			return false
		}
		placements[at] = portion
		return true
	}
	checkPostings := func(postings []storage.GinPosting) error {
		for i, p := range postings {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !owns(p) || i > 0 && amcheckGinPostingCompare(postings[i-1], p) >= 0 {
				return corrupt()
			}
		}
		return nil
	}
	for ordinal, binding := range bindings {
		actualCount := 0
		for side, columns := range [2][]storage.GinColumn{c.Root.Gin.Columns, c.Root.Gin.Pending} {
			if len(columns) == 0 {
				continue
			}
			portion = uint8(side + 1)
			column := &columns[ordinal]
			if !column.Binding.Same(binding) {
				return corrupt()
			}
			for i, list := range column.Tokens {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !amcheckGinTokenShape(binding, list.Token) || len(list.Postings) == 0 {
					return corrupt()
				}
				cmp, err := binding.Compare(ctx, list.Token, list.Token, evaluation.FunctionInput())
				if err != nil {
					return err
				}
				if cmp != 0 {
					return corrupt()
				}
				if i > 0 {
					cmp, err = binding.Compare(ctx, column.Tokens[i-1].Token, list.Token, evaluation.FunctionInput())
					if err != nil {
						return err
					}
					if cmp >= 0 {
						return corrupt()
					}
				}
				if err := checkPostings(list.Postings); err != nil {
					return err
				}
				actualCount += len(list.Postings)
			}
			if err := checkPostings(column.NonNull); err != nil {
				return err
			}
		}
		main := &c.Root.Gin.Columns[ordinal]
		var empty storage.GinColumn
		pending := &empty
		if len(c.Root.Gin.Pending) != 0 {
			pending = &c.Root.Gin.Pending[ordinal]
		}
		stamps := make([]int, len(main.Tokens)+len(pending.Tokens))
		expectedCount, nonNull := 0, 0
		for at, entry := range c.Root.Entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			column, seen := main, stamps[:len(main.Tokens)]
			switch placements[at] {
			case 1:
			case 2:
				column, seen = pending, stamps[len(main.Tokens):]
			default:
				return corrupt()
			}
			p := storage.GinPosting{Row: entry.Row, Version: entry.Version, Position: entry.Position}
			key := entry.Keys[ordinal]
			if !key.IsNull() {
				nonNull++
				found, err := amcheckGinHasPosting(ctx, column.NonNull, p)
				if err != nil {
					return err
				}
				if !found {
					return corrupt()
				}
			}
			if err := amcheckGinExtract(evaluation, binding, key, corrupt, func(token sqltype.GinToken) error {
				lo, hi := 0, len(column.Tokens)
				for lo < hi {
					if err := ctx.Err(); err != nil {
						return err
					}
					mid := lo + (hi-lo)/2
					cmp, err := binding.Compare(ctx, column.Tokens[mid].Token, token, evaluation.FunctionInput())
					if err != nil {
						return err
					}
					if cmp < 0 {
						lo = mid + 1
					} else {
						hi = mid
					}
				}
				if lo == len(column.Tokens) {
					return corrupt()
				}
				cmp, err := binding.Compare(ctx, column.Tokens[lo].Token, token, evaluation.FunctionInput())
				if err != nil {
					return err
				}
				if cmp != 0 {
					return corrupt()
				}
				if seen[lo] == at+1 {
					return nil
				}
				seen[lo] = at + 1
				found, err := amcheckGinHasPosting(ctx, column.Tokens[lo].Postings, p)
				if err != nil {
					return err
				}
				if !found {
					return corrupt()
				}
				expectedCount++
				return nil
			}); err != nil {
				return err
			}
		}
		if expectedCount != actualCount || nonNull != len(main.NonNull)+len(pending.NonNull) {
			return corrupt()
		}
	}
	if err := c.checkPrivateAssociations(ctx); err != nil {
		return err
	}
	successors := make(map[ids.RowID]struct{}, len(c.Writes))
	for _, row := range c.Writes {
		row = c.Store.Headers.Apply(row)
		if err := ctx.Err(); err != nil {
			return err
		}
		if !c.rowShape(row) {
			return corrupt()
		}
		if _, exists := successors[row.Row]; exists {
			return corrupt()
		}
		successors[row.Row] = struct{}{}
		if !visibility.LiveVersion(row, c.Frontier, nil, c.Deletes) {
			continue
		}
		output, produced := row.IndexOutputs[c.Index.Address()]
		if !produced {
			// Every GIN insertion retains its actual key and placement decision;
			// a missing output is never an implicit row-backed extraction lane.
			return corrupt()
		}
		if !OutputMatches(output, c.Index) {
			return ids.ErrCatalogReplaced
		}
		member, actual := output.Member, output.Keys
		if !member && (len(actual) != 0 || output.GinPending) {
			return corrupt()
		}
		if !member {
			continue
		}
		if !c.keyShape(actual) {
			return corrupt()
		}
		for i, key := range actual {
			if err := amcheckGinExtract(evaluation, bindings[i], key, corrupt, func(sqltype.GinToken) error { return ctx.Err() }); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func amcheckGinTokenShape(binding sqltype.GinBinding, token sqltype.GinToken) bool {
	if token.Kind > sqltype.GinNullItem {
		return false
	}
	if binding.IsScalar() {
		if token.Kind == sqltype.GinNullItem {
			return token.Element == (sqltype.Value{}) && token.Lexeme == ""
		}
		return token.Kind == sqltype.GinOrdinary && token.Element.Type() == binding.Usage.Type && !token.Element.IsNull() && token.Lexeme == ""
	}
	// Exact zero checks validate the absence of an element payload, not token
	// equality. Dictionary equality/order always belongs to GinBinding.Compare.
	if token.Kind != sqltype.GinOrdinary {
		return token.Element == (sqltype.Value{}) && token.Lexeme == "" && (token.Kind != sqltype.GinNullElement || binding.Class.InputOID() == 2277)
	}
	if binding.Class.InputOID() == 2277 {
		return token.Lexeme == "" && token.Element.Type() == binding.Usage.Type.BaseType().Element() && !token.Element.IsNull()
	}
	if binding.IsScalar() {
		return token.Lexeme == "" && token.Element.Type() == binding.Usage.Type && !token.Element.IsNull()
	}
	return token.Element == (sqltype.Value{}) && token.Lexeme != ""
}

// Only typed payload failures become corruption. Actual selected callback
// admission errors precede the pure decoder and must retain their diagnostic.
func amcheckGinExtract(evaluation *exec.Context, binding sqltype.GinBinding, key sqltype.Value, corrupt func() error, emit func(sqltype.GinToken) error) error {
	ctx := evaluation.Context
	var input sqltype.InputEnvironment
	if !key.IsNull() {
		// Keep admission diagnostics distinct from typed payload corruption.
		body, err := evaluation.EnterIndexSupport(binding.Class.Support(2), 2)
		if err != nil {
			return err
		}
		extracted, failure := binding.PrepareExtract(ctx, key, input, 2)
		body.Leave()
		if failure != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
			return corrupt()
		}
		return extracted.Emit(ctx, func(token sqltype.GinToken) error {
			if !amcheckGinTokenShape(binding, token) {
				return corrupt()
			}
			return emit(token)
		})
	}
	var emittedError error
	err := binding.Extract(ctx, key, func(token sqltype.GinToken) error {
		if !amcheckGinTokenShape(binding, token) {
			emittedError = corrupt()
			return emittedError
		}
		emittedError = emit(token)
		return emittedError
	})
	if err == nil || emittedError != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return corrupt()
}

func amcheckGinPostingCompare(a, b storage.GinPosting) int {
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
func amcheckGinHasPosting(ctx context.Context, postings []storage.GinPosting, target storage.GinPosting) (bool, error) {
	lo, hi := 0, len(postings)
	for lo < hi {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		mid := lo + (hi-lo)/2
		if amcheckGinPostingCompare(postings[mid], target) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo < len(postings) && postings[lo] == target, ctx.Err()
}
