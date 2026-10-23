package analyze

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (b *Binder) plLocal(names []string, pos parser.Position) (expr.Expr, bool, error) {
	if b.PL == nil {
		return nil, false, nil
	}
	f := b.PL
	for count := len(names); count > 0; count-- {
		slot, ok := f.Program.Lookup(names[:count], f.Scopes)
		if !ok {
			continue
		}
		variable := f.Program.Variables[slot]
		value := f.Values[slot]
		if value.Type() == nil {
			d := sqlError("55000", fmt.Sprintf("record %q is not assigned yet", variable.Name)).(*sqlstate.Error)
			d.Detail = "The tuple structure of a not-yet-assigned record is indeterminate."
			return nil, true, d
		}
		usage := f.Usage(slot)
		if variable.Record {
			usage = value.Type().Usage()
		}
		var node expr.Expr = plan.PLLocalExpr{Info: b.Info(usage, pos, expr.Properties{ParameterDependent: true}), Value: value}
		for _, field := range names[count:] {
			typ := node.ResultUsage().Type.BaseType()
			found := false
			if typ.IsComposite() {
				for i := range typ.CompositeLen() {
					if typ.CompositeMember(i).Name == field {
						found = true
						break
					}
				}
			}
			if !found {
				return nil, true, sqlError("42703", fmt.Sprintf("record %q has no field %q", variable.Name, field))
			}
			var err error
			node, err = b.SelectCompositeField(node, field, pos)
			if err != nil {
				return nil, true, err
			}
		}
		return node, true, nil
	}
	return nil, false, nil
}

func (b *Binder) plParameter(raw parser.ParameterExpr) (expr.Expr, error) {
	if raw.Number <= 0 || raw.Number > b.PL.Program.FormalCount {
		return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", raw.Number), raw.Pos)
	}
	node, _, err := b.plLocal([]string{"$" + strconv.Itoa(raw.Number)}, raw.Pos)
	return node, err
}

func (b *Binder) Column(raw parser.ColumnExpr) (expr.Expr, error) {
	if b.PL == nil {
		return b.column(raw)
	}
	names := make([]string, len(raw.Name.Parts))
	for i, p := range raw.Name.Parts {
		names[i] = p.Text
	}
	local, found, localErr := b.plLocal(names, raw.Pos)
	column, err := b.column(raw)
	if !found {
		return column, err
	}
	if err == nil {
		d := expr.ErrorAt("42702", fmt.Sprintf("column reference %q is ambiguous", names[len(names)-1]), raw.Pos).(*sqlstate.Error)
		d.Detail = "It could refer to either a PL/pgSQL variable or a table column."
		return nil, d
	}
	if diagnostic, ok := err.(*sqlstate.Error); !ok || diagnostic.Code != "42703" && diagnostic.Code != "42P01" {
		return nil, err
	}
	return local, localErr
}
