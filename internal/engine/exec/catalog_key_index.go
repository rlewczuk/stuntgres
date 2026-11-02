package exec

import (
	"cmp"
	"context"
	"slices"
	"sync/atomic"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// CatalogKeyIndex maps the oid cells of one shared, immutable catalog capture
// to their row positions. A column is built on first use, without a lock:
// concurrent statements may build the same column, and the first published
// copy wins. Published columns are never written again.
type CatalogKeyIndex struct {
	rows    [][]sqltype.Value
	columns []atomic.Pointer[catalogKeyColumn]
}

// catalogKeyColumn has a nil typ unless every cell of the column, NULLs
// included, carries the same concrete, non-domain oid descriptor.
type catalogKeyColumn struct {
	typ     *sqltype.Descriptor
	entries []catalogKeyEntry // Sorted by key, then by ascending position.
}

type catalogKeyEntry struct {
	key uint32
	pos int32
}

// NewCatalogKeyIndex indexes rows, which the caller shares read-only.
func NewCatalogKeyIndex(rows [][]sqltype.Value) *CatalogKeyIndex {
	width := 0
	if len(rows) != 0 {
		width = len(rows[0])
	}
	return &CatalogKeyIndex{rows: rows, columns: make([]atomic.Pointer[catalogKeyColumn], width)}
}

// covers reports whether rows is the exact row slice this index was built for.
func (x *CatalogKeyIndex) covers(rows [][]sqltype.Value) bool {
	return x != nil && len(rows) != 0 && len(rows) == len(x.rows) && &rows[0] == &x.rows[0]
}

func (x *CatalogKeyIndex) column(slot int) *catalogKeyColumn {
	if slot < 0 || slot >= len(x.columns) {
		return nil
	}
	if c := x.columns[slot].Load(); c != nil {
		return c
	}
	c := buildCatalogKeyColumn(x.rows, slot)
	if !x.columns[slot].CompareAndSwap(nil, c) {
		return x.columns[slot].Load()
	}
	return c
}

func buildCatalogKeyColumn(rows [][]sqltype.Value, slot int) *catalogKeyColumn {
	var typ *sqltype.Descriptor
	entries := make([]catalogKeyEntry, 0, len(rows))
	for i, row := range rows {
		if slot >= len(row) {
			return &catalogKeyColumn{}
		}
		v := row[slot]
		t := v.Type()
		if t == nil || t.ID() != sqltype.Oid || t.IsDomain() || !t.Concrete() || typ != nil && t != typ {
			return &catalogKeyColumn{}
		}
		typ = t
		if v.IsNull() {
			continue
		}
		key, err := v.OID()
		if err != nil {
			return &catalogKeyColumn{}
		}
		entries = append(entries, catalogKeyEntry{key: key, pos: int32(i)})
	}
	slices.SortFunc(entries, func(a, b catalogKeyEntry) int {
		return cmp.Or(cmp.Compare(a.key, b.key), cmp.Compare(a.pos, b.pos))
	})
	return &catalogKeyColumn{typ: typ, entries: entries}
}

func (c *catalogKeyColumn) span(key uint32) (int, int) {
	lo, _ := slices.BinarySearchFunc(c.entries, key, func(e catalogKeyEntry, key uint32) int { return cmp.Compare(e.key, key) })
	hi := lo
	for hi < len(c.entries) && c.entries[hi].key == key {
		hi++
	}
	return lo, hi
}

func (c *catalogKeyColumn) positions(key uint32) []int {
	lo, hi := c.span(key)
	positions := make([]int, 0, hi-lo)
	for _, e := range c.entries[lo:hi] {
		positions = append(positions, int(e.pos))
	}
	return positions
}

// catalogKeyCandidates narrows a WHERE over a shared catalog capture to the
// rows one of its oid equalities selects, in row order. The WHERE is one
// equality between a column and a statement constant, or an AND of such
// equalities; at least one compares an indexed oid column, and the one with the
// fewest rows wins. The produce loop still evaluates the whole predicate on
// each candidate. AND evaluates both operands, so every skipped row must make
// each conjunct false or NULL without error: each one compares a cell of the
// column's exact type with a constant of that type under the type's own
// equality, and the chosen key differs from the skipped row's cell. Any doubt,
// including a failing probe, returns nil and keeps the full scan, which then
// raises the probe's error in place.
func catalogKeyCandidates(ctx context.Context, state *State, n *plan.FilterNode, child *Stage) *candidateSelection {
	if _, ok := n.Child.(*plan.SourceNode); !ok {
		return nil
	}
	source := child.Source
	if source == nil || !source.CatalogKeys.covers(source.Anonymous) || child.Produce != nil || child.Selection != nil || child.Permutation != nil || child.scanRange {
		return nil
	}
	if source.Visible != nil || source.RLS != nil || source.RlsDenied || source.PartialMask != nil || source.Window || source.Materialized || source.Final != nil || source.Input != nil || source.Inheritance != nil || source.Singleton {
		return nil
	}
	conjuncts, ok := catalogKeyConjuncts(nil, n.Predicate)
	if !ok {
		return nil
	}
	evaluation := state.Evaluation(ctx)
	var keys *catalogKeyColumn
	var key uint32
	best, empty := 0, false
	for _, conjunct := range conjuncts {
		column, probe, ok := catalogKeyEquality(&evaluation, source, conjunct)
		if !ok {
			return nil
		}
		usage := column.ResultUsage()
		indexed := source.CatalogKeys.column(column.Slot)
		if indexed == nil || indexed.typ == nil || indexed.typ != usage.Type {
			indexed = nil
			if !catalogKeyPlainEquality(usage.Type) {
				return nil
			}
		}
		value, err := probe.Eval(&evaluation)
		if err != nil || value.Type() != usage.Type {
			return nil
		}
		if value.IsNull() {
			empty = true
			continue
		}
		if indexed == nil {
			continue
		}
		id, err := value.OID()
		if err != nil {
			return nil
		}
		if lo, hi := indexed.span(id); keys == nil || hi-lo < best {
			keys, key, best = indexed, id, hi-lo
		}
	}
	if empty {
		return &candidateSelection{Occurrences: []int{}}
	}
	if keys == nil {
		return nil
	}
	return &candidateSelection{Occurrences: keys.positions(key)}
}

// catalogKeyConjuncts appends the operands of an AND chain to out; false means
// an operand owns a query, whose evaluation the chain must keep.
func catalogKeyConjuncts(out []expr.Expr, e expr.Expr) ([]expr.Expr, bool) {
	logical, ok := e.(plan.LogicalExpr)
	if !ok || logical.Op != parser.LogicalAnd {
		return append(out, e), true
	}
	if logical.QueryRight || logical.Right == nil {
		return nil, false
	}
	if out, ok = catalogKeyConjuncts(out, logical.Left); !ok {
		return nil, false
	}
	return catalogKeyConjuncts(out, logical.Right)
}

// catalogKeyEquality matches column = constant over source's own rows, where
// the constant has the column's usage and the operator is that type's equality.
func catalogKeyEquality(evaluation *Context, source *Source, e expr.Expr) (plan.ColumnExpr, expr.Expr, bool) {
	predicate, ok := e.(plan.ComparisonExpr)
	if !ok || predicate.Properties().Volatility == expr.Volatile || predicate.Operator != "=" || predicate.Namespace != "" && predicate.Namespace != "pg_catalog" {
		return plan.ColumnExpr{}, nil, false
	}
	column, ok := predicate.Left.(plan.ColumnExpr)
	probe := predicate.Right
	if !ok {
		column, ok = predicate.Right.(plan.ColumnExpr)
		probe = predicate.Left
	}
	if !ok || column.Virtual != nil || column.MergeSource || column.Scope != evaluation.Scope || column.Frame != evaluation.Frame || column.Origin.Domain() != source.Ref.Domain() || column.Origin.Epoch != source.Ref.Epoch || column.Origin.Address.ClassID != source.Ref.Address.ClassID || column.Origin.Address.ObjectID != source.Ref.Address.ObjectID {
		return plan.ColumnExpr{}, nil, false
	}
	usage := column.ResultUsage()
	if !statementConstant(probe) || probe.ResultUsage() != usage || !predicate.Predicate.EqualityFor(usage) {
		return plan.ColumnExpr{}, nil, false
	}
	return column, probe, true
}

// catalogKeyPlainEquality admits the scalar catalog column types whose
// equality between two values of the type cannot fail.
func catalogKeyPlainEquality(t *sqltype.Descriptor) bool {
	if t == nil || t.IsDomain() || !t.Concrete() {
		return false
	}
	switch t.ID() {
	case sqltype.Bool, sqltype.Char, sqltype.Name, sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Text, sqltype.Oid:
		return true
	}
	return false
}

// statementConstant admits a literal or parameter, optionally under casts that
// neither read rows nor check domains and are not volatile.
func statementConstant(e expr.Expr) bool {
	for {
		switch c := e.(type) {
		case plan.LiteralExpr, plan.ParameterExpr:
			return true
		case plan.CoercionExpr:
			p := c.Properties()
			if c.Plan.ChecksDomain() || p.Volatility == expr.Volatile || p.RowDependent || p.StageDependent || p.DomainDependent || p.RuntimeDomainConversion {
				return false
			}
			e = c.Child
		default:
			return false
		}
	}
}
