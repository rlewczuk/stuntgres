package index

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func BindBrinIndex(view catalog.Read, parent *catalog.Relation, d *catalog.IndexDefinition) ([]storage.BrinBinding, error) {
	if d.Method != catalog.BrinMethod || d.Unique || d.IsExclusion() || len(d.Keys) == 0 || len(d.Include) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid BRIN definition")
	}
	bindings := make([]storage.BrinBinding, len(d.Keys))
	for i, key := range d.Keys {
		usage, err := KeyUsage(parent, key)
		if err != nil {
			return nil, err
		}
		class := key.Class
		if class.Name == "" {
			var exists bool
			class, exists, err = view.DefaultIndexClass(d.MethodIdentity(), usage.Type)
			if err != nil {
				return nil, err
			}
			if !exists {
				return nil, DefaultClassError(usage.Type.DiagnosticName(), familyMethodName(view, d.MethodIdentity()))
			}
		}
		if class.MethodOID() != uint32(d.MethodIdentity()) || key.Desc || key.NullsFirst {
			return nil, fmt.Errorf("stuntgres: invalid BRIN key identity")
		}
		class, err = routine.BindClassSupport(view, class)
		if err != nil {
			return nil, err
		}
		comparison, err := sqltype.BindBrinComparison(usage, class)
		if err != nil {
			return nil, err
		}
		binding := storage.BrinBinding{Usage: usage, Class: class, Comparison: comparison, Derived: key.Derived}
		if key.Derived != nil {
			if key.Derived.Definition == nil || key.Derived.Execution == nil {
				return nil, fmt.Errorf("stuntgres: missing BRIN expression")
			}
			binding.Slot, binding.Expression = -1, key.Derived.Execution
		} else {
			binding.Slot = slices.Index(parent.Attributes, key.Attribute)
			if binding.Slot < 0 {
				return nil, fmt.Errorf("stuntgres: missing BRIN attribute")
			}
		}
		bindings[i] = binding
	}
	return bindings, nil
}

func brinKeyProgram(bindings []storage.BrinBinding, d *catalog.IndexDefinition) Ordering {
	o := Ordering{Slots: make([]int, len(bindings)), Keys: d.Keys}
	for i, b := range bindings {
		o.Slots[i] = b.Slot
		if b.Expression != nil {
			if o.Derived == nil {
				o.Derived = make([]expr.Expr, len(bindings))
			}
			o.Derived[i] = b.Expression
		}
	}
	return o
}

func BrinOptions(index *catalog.Relation) (width int, autosummarize bool) {
	width = 128
	for _, o := range index.StorageOptions.Base {
		switch o.Name {
		case "pages_per_range":
			width = int(o.Integer)
		case "autosummarize":
			autosummarize = o.Boolean
		}
	}
	return
}

// BuildRelationIndexRoot is the descriptor-aware root dispatcher: BRIN needs
// reloption build intent in addition to the shared immutable key definition.
func BuildRelationIndexRoot(evaluation *exec.Context, parent, index *catalog.Relation, store *storage.Relation, capture *BuildCapture) (*storage.IndexRoot, error) {
	if index.SQLIndex.Method == catalog.BrinMethod {
		return BuildBrinIndexRoot(evaluation, parent, index, store, true, capture)
	}
	return BuildIndexRoot(evaluation, parent, index.SQLIndex, store, capture)
}

func BuildBrinIndexRoot(evaluation *exec.Context, parent, index *catalog.Relation, store *storage.Relation, materialize bool, capture *BuildCapture) (*storage.IndexRoot, error) {
	bindings, err := BindBrinIndex(evaluation.Catalog, parent, index.SQLIndex)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, fmt.Errorf("stuntgres: missing BRIN storage")
	}
	width, _ := BrinOptions(index)
	if width < 1 || width > 131072 {
		return nil, fmt.Errorf("stuntgres: invalid BRIN build width")
	}
	var root *storage.BrinRoot
	if materialize {
		root = &storage.BrinRoot{EffectivePagesPerRange: width, Bindings: bindings, Storage: store.IndexIdentity(), Generation: store.Generation, Prefix: len(store.Versions), Ends: store.Ends, Ranges: make([]storage.BrinRange, (len(store.Versions)+width-1)/width)}
	}
	o := brinKeyProgram(bindings, index.SQLIndex)
	var preparation ExecutionPreparation
	for position, row := range store.Versions {
		if err := evaluation.Context.Err(); err != nil {
			return nil, err
		}
		ordinal := position / width
		if materialize && !root.Ranges[ordinal].Summarized {
			root.Ranges[ordinal] = storage.BrinRange{Summarized: true, Keys: make([]storage.BrinSummary, len(bindings))}
		}
		if !visibility.LiveVersion(row, ids.CommitSeq(^uint64(0)), store.Ends, nil) {
			continue
		}
		matches, err := preparation.PredicateMatches(evaluation, parent, index.SQLIndex, row.Values, &o, nil)
		if err != nil {
			return nil, err
		}
		if capture != nil {
			capture.Outputs[position] = storage.IndexOutput{Index: capture.Index, Member: matches}
		}
		if !matches {
			continue
		}
		if err := preparation.PrepareKeys(evaluation, parent, index.SQLIndex, &o, nil); err != nil {
			return nil, err
		}
		var keys []sqltype.Value
		if capture != nil {
			keys = make([]sqltype.Value, len(bindings))
			capture.Outputs[position].Keys = keys
		}
		for i, b := range bindings {
			var program expr.Expr
			if o.Derived != nil {
				program = o.Derived[i]
			}
			value, err := extractIndexKey(b.Slot, program, row.Values, evaluation)
			if err != nil {
				return nil, err
			}
			if capture != nil {
				keys[i] = value
			}
			if materialize {
				if err := extendBrinSummary(evaluation, &root.Ranges[ordinal].Keys[i], b, value); err != nil {
					return nil, err
				}
			}
		}
	}
	if !materialize {
		return nil, evaluation.Context.Err()
	}
	return &storage.IndexRoot{Brin: root}, evaluation.Context.Err()
}

func extendBrinSummary(evaluation *exec.Context, summary *storage.BrinSummary, b storage.BrinBinding, value sqltype.Value) error {
	if value.IsNull() {
		summary.HasNull = true
		return evaluation.Context.Err()
	}
	if !summary.HasNonNull {
		summary.Min, summary.Max, summary.HasNonNull = value, value, true
		return evaluation.Context.Err()
	}
	c, err := b.Comparison.Compare(evaluation.Context, value, summary.Min, evaluation.Input)
	if err != nil {
		return err
	}
	if c < 0 {
		summary.Min = value
	}
	c, err = b.Comparison.Compare(evaluation.Context, value, summary.Max, evaluation.Input)
	if err != nil {
		return err
	}
	if c > 0 {
		summary.Max = value
	}
	return nil
}

// BrinConsistent is a lossy range-candidate operation. Missing summaries always
// match; callers must recheck ordinary SQL predicates on selected visible rows.
func BrinConsistent(ctx context.Context, r *storage.BrinRoot, ordinal, key int, strategy string, query sqltype.Value, input sqltype.InputEnvironment) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if r == nil || ordinal < 0 || key < 0 || key >= len(r.Bindings) {
		return false, fmt.Errorf("stuntgres: invalid BRIN consistency input")
	}
	if ordinal >= len(r.Ranges) || !r.Ranges[ordinal].Summarized {
		return true, nil
	}
	s := r.Ranges[ordinal].Keys[key]
	if strategy == "IS NULL" {
		return s.HasNull, nil
	}
	if strategy == "IS NOT NULL" {
		return s.HasNonNull, nil
	}
	switch strategy {
	case "<", "<=", "=", ">=", ">":
	default:
		return true, nil
	}
	if query.IsNull() || !s.HasNonNull {
		return false, nil
	}
	b := r.Bindings[key]
	if strategy == "<" || strategy == "<=" || strategy == "=" {
		c, err := b.Comparison.Compare(ctx, s.Min, query, input)
		if err != nil {
			return false, err
		}
		if strategy == "<" {
			return c < 0, nil
		}
		if strategy == "<=" {
			return c <= 0, nil
		}
		if c > 0 {
			return false, nil
		}
	}
	c, err := b.Comparison.Compare(ctx, s.Max, query, input)
	if err != nil {
		return false, err
	}
	if strategy == ">" {
		return c > 0, nil
	}
	return c >= 0, nil
}
