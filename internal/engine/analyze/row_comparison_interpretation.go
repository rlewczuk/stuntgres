package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// REL_18_6 pg_amop.dat's standard B-tree operators all have the interpretation
// named by their spelling. Families need not match between row columns: the
// intersection in parse_expr.c:make_row_comparison_op is over strategies, not
// family OIDs. Inequality obtains its interpretation from its equality negator.
// This is released operator membership, not a scalar-provider/orderability test.
func rowBTreeFormalPair(left, right sqltype.ID) bool {
	integer := func(id sqltype.ID) bool { return id == sqltype.Int2 || id == sqltype.Int4 || id == sqltype.Int8 }
	floating := func(id sqltype.ID) bool { return id == sqltype.Float4 || id == sqltype.Float8 }
	datetime := func(id sqltype.ID) bool {
		return id == sqltype.Date || id == sqltype.Timestamp || id == sqltype.Timestamptz
	}
	if integer(left) && integer(right) || floating(left) && floating(right) || datetime(left) && datetime(right) {
		return true
	}
	if left == sqltype.Name && right == sqltype.Text || left == sqltype.Text && right == sqltype.Name {
		return true
	}
	if left != right {
		return false
	}
	switch left {
	case sqltype.Bool, sqltype.Bytea, sqltype.Char, sqltype.Name, sqltype.Text,
		sqltype.BPChar, sqltype.Numeric, sqltype.Money, sqltype.Oid, sqltype.Tid,
		sqltype.Xid8, sqltype.Time, sqltype.Timetz, sqltype.Interval, sqltype.Bit,
		sqltype.VarBit, sqltype.UUID, sqltype.Inet, sqltype.JSONB,
		2277, // ANYARRAY: array_ops
		2249, // RECORD: record_ops, not physical record_image_ops
		3500, // ANYENUM: enum_ops
		3831, // ANYRANGE: range_ops
		4537: // ANYMULTIRANGE: multirange_ops
		return true
	}
	return false
}

func (b *Binder) checkRowComparisonInterpretation(raw parser.OperatorExpr, pairs []plan.ComparisonExpr) error {
	if len(pairs) < 2 {
		return nil // The released one-field exception returns the scalar operator.
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if name == "!=" {
		name = "<>"
	}
	for _, pair := range pairs {
		left, right := pair.Left.ResultUsage().Type.BaseType(), pair.Right.ResultUsage().Type.BaseType()
		declaration, _, err := b.comparisonRoutineDeclaration(name, left, right)
		if err != nil {
			return err
		}
		if left.IsCube() && left == right {
			if _, err := b.bindCubeOrdering(left.Usage()); err != nil {
				return err
			}
			continue
		}
		formalLeft, formalRight := comparisonFormalID(left), comparisonFormalID(right)
		selected := routine.OperatorRoutine(name, ids.OID(formalLeft), ids.OID(formalRight))
		if declaration != nil && declaration.Builtin == selected && rowBTreeFormalPair(formalLeft, formalRight) {
			continue
		}
		err = expr.ErrorAt("0A000", "could not determine interpretation of row comparison operator "+name, raw.Pos)
		err.(*sqlstate.Error).Hint = "Row comparison operators must be associated with btree operator families."
		return err
	}
	return nil
}
