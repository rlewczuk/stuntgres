package plan

import (
	"stuntgres/internal/engine/expr"
)

// MapRow maps a row's executable fields with child, copying the field list
// only when a field changes.
func MapRow(row RowExpr, child func(expr.Expr) (expr.Expr, bool, error)) (RowExpr, bool, error) {
	var fields []expr.Expr
	for i, old := range row.Fields {
		next, changed, err := child(old)
		if err != nil {
			return row, false, err
		}
		if changed {
			if fields == nil {
				fields = append([]expr.Expr(nil), row.Fields...)
			}
			fields[i] = next
		}
	}
	if fields == nil {
		return row, false, nil
	}
	row.Fields = fields
	return row, true, nil
}

// MapRowAnalysis maps a row's fields and its composite expansions. Analysis
// associations preserve one raw occurrence across expanded slots.
// Catalog/type mapping also reowns these retained associations; runtime demand
// and grouping rewrites deliberately map only the executable fields.
func MapRowAnalysis(row RowExpr, child func(expr.Expr) (expr.Expr, bool, error)) (RowExpr, bool, error) {
	next, changed, err := MapRow(row, child)
	if err != nil {
		return row, false, err
	}
	var expansions map[int]CompositeExpansionBinding
	for source, expansion := range row.Expansions {
		base, replaced, err := child(expansion.Base)
		if err != nil {
			return row, false, err
		}
		var fields []expr.Expr
		for i, old := range expansion.Fields {
			field, mapped, err := child(old)
			if err != nil {
				return row, false, err
			}
			if mapped {
				if fields == nil {
					fields = append([]expr.Expr(nil), expansion.Fields...)
				}
				fields[i] = field
			}
		}
		if !replaced && fields == nil {
			continue
		}
		if expansions == nil {
			expansions = make(map[int]CompositeExpansionBinding, len(row.Expansions))
			for key, value := range row.Expansions {
				expansions[key] = value
			}
		}
		expansion.Base = base
		if fields != nil {
			expansion.Fields = fields
		}
		expansions[source] = expansion
	}
	if expansions != nil {
		next.Expansions = expansions
		changed = true
	}
	return next, changed, nil
}

// MapRowComparisonExpansions maps a row comparison's expansions. Reuse the
// constructor's copy-on-change ownership mapping for analysis-only
// expansion associations; executable pairs are mapped by their existing owners.
func MapRowComparisonExpansions(expansions map[int]CompositeExpansionBinding, child func(expr.Expr) (expr.Expr, bool, error)) (map[int]CompositeExpansionBinding, bool, error) {
	row, changed, err := MapRowAnalysis(RowExpr{Expansions: expansions}, child)
	return row.Expansions, changed, err
}

// MapScalarArrayChildren maps the operands of an ANY/ALL comparison. Copy
// only changed list storage; the analyzed tree remains immutable.
func MapScalarArrayChildren(n ScalarArrayComparisonExpr, child func(expr.Expr) (expr.Expr, bool, error)) (ScalarArrayComparisonExpr, bool, error) {
	left, changed, err := child(n.Left)
	if err != nil {
		return n, false, err
	}
	n.Left = left
	if n.Array != nil {
		array, replaced, err := child(n.Array)
		if err != nil {
			return n, false, err
		}
		n.Array = array
		changed = changed || replaced
	}
	var items []expr.Expr
	for i, old := range n.Items {
		next, replaced, err := child(old)
		if err != nil {
			return n, false, err
		}
		if replaced && items == nil {
			items = append([]expr.Expr(nil), n.Items...)
		}
		if items != nil {
			items[i] = next
		}
		changed = changed || replaced
	}
	if items != nil {
		n.Items = items
	}
	return n, changed, nil
}
