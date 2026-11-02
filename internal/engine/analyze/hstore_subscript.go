package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// hstoreSubscriptKey binds hstore 1.8 subscripts (hstore_subscript_handler):
// exactly one non-slice subscript, assignment-coerced to text. Like
// PostgreSQL's handler it records no dependency of its own; the container's
// type already owns the handler.
func (b *Binder) hstoreSubscriptKey(typ *sqltype.Descriptor, raw []parser.ArraySubscript) ([]plan.BoundArraySubscript, error) {
	view := b.BindingCatalog()
	installed, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
	if !ok || installed.SQLType != typ {
		return nil, fmt.Errorf("stuntgres: foreign hstore subscript type")
	}
	handler, ok, err := catalog.InstalledTypeSubscript(view, installed)
	if err != nil {
		return nil, err
	}
	if !ok || extension.HstoreProviderOf(handler) != extension.StockHstore {
		return nil, fmt.Errorf("stuntgres: missing hstore subscript handler")
	}
	if err := extension.StockHstore.ValidateProcedure(view, handler); err != nil {
		return nil, err
	}
	if len(raw) != 1 || raw[0].Slice {
		return nil, expr.ErrorAt("0A000", "hstore allows only one subscript", parser.Position{})
	}
	key, err := b.bindSubscriptOperand(raw[0].Upper, sqltype.Text, "hstore subscript must have type text")
	if err != nil {
		return nil, err
	}
	return []plan.BoundArraySubscript{{Upper: key}}, nil
}

// bindHstoreSubscript fetches the text value of one key of an hstore.
func (b *Binder) bindHstoreSubscript(raw parser.SubscriptExpr, base expr.Expr, typ *sqltype.Descriptor) (expr.Expr, error) {
	subs, err := b.hstoreSubscriptKey(typ, raw.Subscripts)
	if err != nil {
		return nil, err
	}
	properties := expr.MergeProperties(base.Properties(), subs[0].Upper.Properties())
	info, err := b.CollatedInfo(b.Info(b.Type(sqltype.Text).Usage(), raw.Pos, properties), base)
	if err != nil {
		return nil, err
	}
	return plan.SubscriptExpr{Info: info, Base: base, Subscripts: subs, Handler: plan.HstoreSubscripts}, nil
}

// bindHstoreTarget assigns one text key of an hstore column, re-applying the
// column's domain to the updated container.
func (b *Binder) bindHstoreTarget(column catalog.Column, usage sqltype.Usage, raw []parser.ArraySubscript, pos parser.Position) (*plan.ArrayAssignmentTarget, error) {
	subs, err := b.hstoreSubscriptKey(usage.Type, raw)
	if err != nil {
		return nil, err
	}
	reapply, err := sqltype.PlanCoercion(usage, column.Usage, sqltype.Assignment)
	if err != nil {
		return nil, err
	}
	return &plan.ArrayAssignmentTarget{Node: plan.SubscriptExpr{Subscripts: subs, Handler: plan.HstoreSubscripts}, Container: column.Usage, Destination: b.Type(sqltype.Text).Usage(), Reapply: reapply, Name: column.Name, Pos: pos}, nil
}
