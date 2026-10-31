package routines_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSQLRoutineDeclaredRecordPlan(t *testing.T) {
	e := engine.New()
	read := e.CatalogRead()
	record, _ := read.LookupType(2249)
	integer, _ := read.LookupType(sqltype.Int4)
	text, _ := read.LookupType(sqltype.Text)
	one, _ := sqltype.Integer(integer, 1)
	columns := []catalog.Column{{Name: "a", Usage: integer.Usage()}, {Name: "b", Usage: text.Usage()}}
	binding := analyze.Binding{Query: &plan.Query{Root: &plan.ProjectNode{QuerySchema: columns}}}
	auth := core.AuthorizationView{View: access.View{Catalog: read}}
	conversion, err := core.ValidateSQLRoutineTerminal(&parser.Select{}, binding, record.Usage(), auth, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := conversion.Apply(context.Background(), []sqltype.Value{one, sqltype.String(text, "x")}, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	actual := value.ActualRecord().Type()
	if !value.Type().IsDeferredRecord() || actual.CompositeLen() != 2 || actual.CompositeMember(0).Name != "a" || actual.CompositeMember(1).Name != "b" {
		t.Fatal("declaration did not produce an owned, labelled record", value)
	}
	if record.IsDeferredRecord() || record.IsComposite() {
		t.Fatal("planning changed the catalog declaration")
	}
	foreign, _ := engine.New().CatalogRead().LookupType(2249)
	if _, err := core.ValidateSQLRoutineTerminal(&parser.Select{}, binding, foreign.Usage(), auth, nil); err == nil {
		t.Fatal("foreign declaration acquired executable record authority")
	}
	if _, err := conversion.Apply(context.Background(), []sqltype.Value{one}, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}); err == nil {
		t.Fatal("packed record accepted a truncated row")
	}
}
