package enginetests_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDomainDefaultCompositeFieldDependenciesInternal(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE TYPE public.dd_pair AS(a integer,b integer,c integer)",
		"CREATE DOMAIN public.dd AS integer DEFAULT (ROW(7,8,9)::public.dd_pair).a",
		"CREATE DOMAIN public.dd_child AS public.dd",
		"CREATE DOMAIN public.dd_b AS integer DEFAULT (ROW(17,18,19)::public.dd_pair).b",
		"CREATE TABLE public.dd_rows(id integer,own public.dd,child public.dd_child,keep integer)",
		"CREATE VIEW public.dd_view AS SELECT id,own,child,keep FROM public.dd_rows",
		"CREATE TABLE public.dd_survivor(id integer,v public.dd_b)",
		"INSERT INTO public.dd_rows(id,keep) VALUES(1,101)",
		"INSERT INTO public.dd_survivor(id) VALUES(1)",
	} {
		enginetest.RoleExecute(t, e, op, query)
	}
	before := e.CatalogRead()
	pair := domainUnitType(t, before, "dd_pair")
	source := before.MustRelation(pair.OwningRelation)
	own := domainUnitType(t, before, "dd")
	child := domainUnitType(t, before, "dd_child")
	survivor := domainUnitType(t, before, "dd_b")
	references := func(view catalog.Read, d *catalog.Type, want int32) {
		t.Helper()
		found := 0
		for _, ref := range d.DomainDefault.Dependencies {
			if ref.Address.ClassID != ids.RelationClassID || ref.Address.ObjectID != source.Address().ObjectID {
				continue
			}
			found++
			if ref.Address.SubID != want || ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() {
				t.Fatal("foreign or positional field dependency", ref, want)
			}
		}
		if found != 1 {
			t.Fatal("missing or duplicate stable selected attribute", d.Name(), found)
		}
	}
	references(before, own, source.Attributes[0])
	references(before, child, source.Attributes[0])
	references(before, survivor, source.Attributes[1])
	original := survivor.DomainDefault.Expression
	originalRefs := slices.Clone(survivor.DomainDefault.Dependencies)
	originalText, err := (stored.Deparser{View: before}).SQL(op.Context(), original, stored.DeparseParent{})
	if err != nil || !strings.Contains(originalText, "ROW(17, 18, 19)") {
		t.Fatal(originalText, err)
	}
	originalField, ok := original.(plan.FieldExpr)
	if !ok {
		t.Fatal("default lost its genuine selected field", original)
	}
	originalRow, ok := originalField.Base.(plan.RowExpr)
	if !ok || originalRow.FieldProjection != nil {
		t.Fatal("fresh constructor allocated retained associations", originalField.Base)
	}
	initialContext := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Catalog: before}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	initialValue, initialErr := originalRow.Eval(&initialContext)
	if initialErr != nil || initialValue.CompositeLen() != 3 {
		t.Fatal("normal row changed its declared shape", initialValue, initialErr)
	}
	for i, want := range []int64{17, 18, 19} {
		got, valueErr := initialValue.CompositeField(i).Integer()
		if valueErr != nil || got != want {
			t.Fatal("normal row changed written values", i, got, valueErr)
		}
	}
	_, err = e.Execute(op, enginetest.Statement(t, "ALTER TYPE public.dd_pair DROP ATTRIBUTE a RESTRICT"), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || e.CatalogRead().State() != before.State() {
		t.Fatal("RESTRICT changed source or missed dependency", err)
	}
	enginetest.RoleExecute(t, e, op, "ALTER TYPE public.dd_pair DROP ATTRIBUTE a CASCADE")
	after := e.CatalogRead()
	if after.Get(own.Address()) != nil || after.Get(child.Address()) != nil {
		t.Fatal("dependent domains survived")
	}
	mapped := after.MustType(survivor.Address())
	references(after, mapped, source.Attributes[1])
	// A second real shape migration must preserve original child identity again.
	enginetest.RoleExecute(t, e, op, "ALTER TYPE public.dd_pair DROP ATTRIBUTE c RESTRICT")
	after = e.CatalogRead()
	mapped = after.MustType(survivor.Address())
	references(after, mapped, source.Attributes[1])
	if err := stored.ValidateDomainDefault(after, mapped); err != nil {
		t.Fatal(err)
	}
	if err := table.ValidateGenerationPlan(after, nil, mapped.DomainDefault.Expression); err != nil {
		t.Fatal(err)
	}
	var selected plan.FieldExpr
	found := false
	plan.WalkExpression(mapped.DomainDefault.Expression, func(node expr.Expr) {
		if field, ok := node.(plan.FieldExpr); ok && field.Attribute == source.Attributes[1] {
			selected = field
			found = true
		}
	})
	row, constructor := selected.Base.(plan.RowExpr)
	if !found || !constructor || selected.Index != 1 || len(row.Fields) != 3 || len(row.FieldProjection) != 3 || row.FieldProjection[0].Slot != -1 || row.FieldProjection[1].Slot != 0 || row.FieldProjection[2].Slot != -1 {
		t.Fatal("lost original written child association", selected, row)
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Catalog: after}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	value, err := selected.Eval(&evaluation)
	if err != nil {
		t.Fatal(err)
	}
	number, numberErr := value.Integer()
	if value.IsNull() || numberErr != nil || number != 18 {
		t.Fatal("live b selected wrong original child", value, numberErr)
	}
	value, err = row.Eval(&evaluation)
	if err != nil || value.CompositeLen() != 1 {
		t.Fatal("whole row lost current live shape", value, err)
	}
	number, numberErr = value.CompositeField(0).Integer()
	if numberErr != nil || number != 18 {
		t.Fatal("whole row selected wrong child", number, numberErr)
	}
	// Genuine selected-child demand must remain allocation-free with respect to
	// the row payload and must never evaluate retired siblings.
	fast := selected
	fastRow := row
	fastRow.Fields = slices.Clone(row.Fields)
	fastRow.Fields[0], fastRow.Fields[2] = plan.LiteralExpr{}, plan.LiteralExpr{}
	fast.Base = fastRow
	value, err = fast.Eval(&evaluation)
	if err != nil {
		t.Fatal(err)
	}
	number, numberErr = value.Integer()
	if numberErr != nil || number != 18 {
		t.Fatal("selected field demanded retired siblings", value, numberErr)
	}
	text, err := (stored.Deparser{View: after}).SQL(op.Context(), mapped.DomainDefault.Expression, stored.DeparseParent{})
	if err != nil || text != originalText {
		t.Fatal("source metadata rewrote written constructor", text, originalText, err)
	}
	nativeText, err := (stored.ScalarRenderer{CurrentRowShape: true}).SQL(op.Context(), after, mapped.DomainDefault.Expression)
	if err != nil || !strings.Contains(nativeText, "ROW(18)") || strings.Contains(nativeText, "17") || strings.Contains(nativeText, "19") {
		t.Fatal("native replay retained retired constructor arity", nativeText, err)
	}
	again, _, err := stored.RetagTypes(after, mapped.DomainDefault.Expression, func(typ *sqltype.Descriptor) *sqltype.Descriptor {
		current, ok := after.LookupType(typ.ID())
		if ok {
			return current
		}
		return typ
	})
	if err != nil || !analyze.SameGroupExpression(again, mapped.DomainDefault.Expression) {
		t.Fatal("repeated remap lost association", err)
	}
	if pair.SQLType.CompositeLen() != 3 || before.Get(survivor.Address()) != survivor || !slices.Equal(originalRefs, survivor.DomainDefault.Dependencies) || !analyze.SameGroupExpression(survivor.DomainDefault.Expression, original) {
		t.Fatal("retag mutated immutable predecessor")
	}
	oldText, err := (stored.Deparser{View: before}).SQL(op.Context(), original, stored.DeparseParent{})
	if err != nil || oldText != originalText {
		t.Fatal("predecessor metadata changed", oldText, err)
	}
	forged := row
	forged.FieldProjection = slices.Clone(row.FieldProjection)
	forged.FieldProjection[0] = forged.FieldProjection[1]
	if err := table.ValidateGenerationPlan(after, nil, forged); err == nil {
		t.Fatal("duplicate retained attribute admitted")
	}
	foreign := engine.New().CatalogRead()
	if err := stored.ValidateDomainDefault(foreign, mapped); err == nil {
		t.Fatal("foreign default/attribute authority admitted")
	}
	enginetest.RoleExecute(t, e, op, "INSERT INTO public.dd_survivor(id) VALUES(2)")
	result := enginetest.RoleExecute(t, e, op, "SELECT id,v FROM public.dd_survivor ORDER BY id")
	defer result.Rows.Close()
	cursor := enginetest.IndependentFinalRows(result.Rows)
	defer cursor.Close()
	values := make([]sqltype.Value, len(cursor.Schema()))
	for _, wantID := range []int64{1, 2} {
		present, err := cursor.Next(op.Context(), values)
		if err != nil || !present || len(values) != 2 {
			t.Fatal("future default row missing", wantID, present, err)
		}
		id, err := values[0].Integer()
		if err != nil || id != wantID || values[1].Type() != mapped.SQLType {
			t.Fatal("future default lost row/domain identity", id, values[1].Type(), err)
		}
		base, err := values[1].BaseValue()
		if err != nil {
			t.Fatal(err)
		}
		number, err := base.Integer()
		if err != nil || number != 18 {
			t.Fatal("future default demand failed", number, err)
		}
	}
	if present, err := cursor.Next(op.Context(), values); err != nil || present {
		t.Fatal("unexpected additional survivor row", present, err)
	}
}

func domainUnitType(t *testing.T, view catalog.Read, name string) *catalog.Type {
	t.Helper()
	addresses := view.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
	if len(addresses) != 1 {
		t.Fatal(name, addresses)
	}
	return view.MustType(addresses[0])
}
