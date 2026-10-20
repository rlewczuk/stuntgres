package engine

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDomainDefaultCompositeFieldDependenciesInternal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
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
		roleExecute(t, e, op, query)
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
	originalText, err := (pgRenderer{view: before}).sql(op.Context(), original, pgRenderParent{})
	if err != nil || !strings.Contains(originalText, "ROW(17, 18, 19)") {
		t.Fatal(originalText, err)
	}
	originalField, ok := original.(fieldExpression)
	if !ok {
		t.Fatal("default lost its genuine selected field", original)
	}
	originalRow, ok := originalField.base.(rowExpression)
	if !ok || originalRow.fieldProjection != nil {
		t.Fatal("fresh constructor allocated retained associations", originalField.base)
	}
	initialContext := EvalContext{evalStatement: evalStatement{Context: op.Context(), catalog: before}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
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
	_, err = e.Execute(op, statement(t, "ALTER TYPE public.dd_pair DROP ATTRIBUTE a RESTRICT"), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || e.CatalogRead().State() != before.State() {
		t.Fatal("RESTRICT changed source or missed dependency", err)
	}
	roleExecute(t, e, op, "ALTER TYPE public.dd_pair DROP ATTRIBUTE a CASCADE")
	after := e.CatalogRead()
	if after.Get(own.Address()) != nil || after.Get(child.Address()) != nil {
		t.Fatal("dependent domains survived")
	}
	mapped := after.MustType(survivor.Address())
	references(after, mapped, source.Attributes[1])
	// A second real shape migration must preserve original child identity again.
	roleExecute(t, e, op, "ALTER TYPE public.dd_pair DROP ATTRIBUTE c RESTRICT")
	after = e.CatalogRead()
	mapped = after.MustType(survivor.Address())
	references(after, mapped, source.Attributes[1])
	if err := validateDomainDefault(after, mapped); err != nil {
		t.Fatal(err)
	}
	if err := validateGenerationPlan(after, nil, mapped.DomainDefault.Expression); err != nil {
		t.Fatal(err)
	}
	var selected fieldExpression
	found := false
	walkBoundExpression(mapped.DomainDefault.Expression, func(node expr.Expr) {
		if field, ok := node.(fieldExpression); ok && field.attribute == source.Attributes[1] {
			selected = field
			found = true
		}
	})
	row, constructor := selected.base.(rowExpression)
	if !found || !constructor || selected.index != 1 || len(row.fields) != 3 || len(row.fieldProjection) != 3 || row.fieldProjection[0].slot != -1 || row.fieldProjection[1].slot != 0 || row.fieldProjection[2].slot != -1 {
		t.Fatal("lost original written child association", selected, row)
	}
	evaluation := EvalContext{evalStatement: evalStatement{Context: op.Context(), catalog: after}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
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
	fastRow.fields = slices.Clone(row.fields)
	fastRow.fields[0], fastRow.fields[2] = literalExpression{}, literalExpression{}
	fast.base = fastRow
	value, err = fast.Eval(&evaluation)
	if err != nil {
		t.Fatal(err)
	}
	number, numberErr = value.Integer()
	if numberErr != nil || number != 18 {
		t.Fatal("selected field demanded retired siblings", value, numberErr)
	}
	text, err := (pgRenderer{view: after}).sql(op.Context(), mapped.DomainDefault.Expression, pgRenderParent{})
	if err != nil || text != originalText {
		t.Fatal("source metadata rewrote written constructor", text, originalText, err)
	}
	nativeText, err := (scalarRenderer{currentRowShape: true}).sql(op.Context(), after, mapped.DomainDefault.Expression)
	if err != nil || !strings.Contains(nativeText, "ROW(18)") || strings.Contains(nativeText, "17") || strings.Contains(nativeText, "19") {
		t.Fatal("native replay retained retired constructor arity", nativeText, err)
	}
	again, _, err := retagBoundTypes(after, mapped.DomainDefault.Expression, func(typ *sqltype.Descriptor) *sqltype.Descriptor {
		current, ok := after.LookupType(typ.ID())
		if ok {
			return current
		}
		return typ
	})
	if err != nil || !sameGroupExpression(again, mapped.DomainDefault.Expression) {
		t.Fatal("repeated remap lost association", err)
	}
	if pair.SQLType.CompositeLen() != 3 || before.Get(survivor.Address()) != survivor || !slices.Equal(originalRefs, survivor.DomainDefault.Dependencies) || !sameGroupExpression(survivor.DomainDefault.Expression, original) {
		t.Fatal("retag mutated immutable predecessor")
	}
	oldText, err := (pgRenderer{view: before}).sql(op.Context(), original, pgRenderParent{})
	if err != nil || oldText != originalText {
		t.Fatal("predecessor metadata changed", oldText, err)
	}
	forged := row
	forged.fieldProjection = slices.Clone(row.fieldProjection)
	forged.fieldProjection[0] = forged.fieldProjection[1]
	if err := validateGenerationPlan(after, nil, forged); err == nil {
		t.Fatal("duplicate retained attribute admitted")
	}
	foreign := New().CatalogRead()
	if err := validateDomainDefault(foreign, mapped); err == nil {
		t.Fatal("foreign default/attribute authority admitted")
	}
	roleExecute(t, e, op, "INSERT INTO public.dd_survivor(id) VALUES(2)")
	result := roleExecute(t, e, op, "SELECT id,v FROM public.dd_survivor ORDER BY id")
	defer result.Rows.Close()
	cursor := independentFinalRows(result.Rows)
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
