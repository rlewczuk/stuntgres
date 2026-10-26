package exec

import (
	"cmp"
	"context"
	"slices"
	"sync/atomic"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
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

func (c *catalogKeyColumn) positions(key uint32) []int {
	i, _ := slices.BinarySearchFunc(c.entries, key, func(e catalogKeyEntry, key uint32) int { return cmp.Compare(e.key, key) })
	positions := []int{}
	for ; i < len(c.entries) && c.entries[i].key == key; i++ {
		positions = append(positions, int(c.entries[i].pos))
	}
	return positions
}

// catalogKeyCandidates narrows a WHERE that is one oid equality between a
// column of a shared catalog capture and a statement constant to the rows
// holding that oid, in row order. The produce loop still evaluates the whole
// predicate on each candidate. Every skipped row would have compared false
// without error: its cell has the column's exact type, which equals both
// operand types. Any doubt, including a failing probe, returns nil and keeps
// the full scan, which then raises the probe's error in place.
func catalogKeyCandidates(ctx context.Context, state *State, n *plan.FilterNode, child *Stage) *hashCandidateSelection {
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
	predicate, ok := n.Predicate.(plan.ComparisonExpr)
	if !ok || predicate.Properties().Volatility == expr.Volatile || predicate.Operator != "=" || predicate.Namespace != "" && predicate.Namespace != "pg_catalog" {
		return nil
	}
	column, ok := predicate.Left.(plan.ColumnExpr)
	probe := predicate.Right
	if !ok {
		column, ok = predicate.Right.(plan.ColumnExpr)
		probe = predicate.Left
	}
	evaluation := state.Evaluation(ctx)
	if !ok || column.Virtual != nil || column.MergeSource || column.Scope != evaluation.Scope || column.Frame != evaluation.Frame || column.Origin.Domain() != source.Ref.Domain() || column.Origin.Epoch != source.Ref.Epoch || column.Origin.Address.ClassID != source.Ref.Address.ClassID || column.Origin.Address.ObjectID != source.Ref.Address.ObjectID {
		return nil
	}
	usage := column.ResultUsage()
	if !statementConstant(probe) || probe.ResultUsage() != usage || !predicate.Predicate.EqualityFor(usage) {
		return nil
	}
	keys := source.CatalogKeys.column(column.Slot)
	if keys == nil || keys.typ == nil || keys.typ != usage.Type {
		return nil
	}
	value, err := probe.Eval(&evaluation)
	if err != nil || value.Type() != usage.Type {
		return nil
	}
	if value.IsNull() {
		return &hashCandidateSelection{Occurrences: []int{}}
	}
	key, err := value.OID()
	if err != nil {
		return nil
	}
	return &hashCandidateSelection{Occurrences: keys.positions(key)}
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
