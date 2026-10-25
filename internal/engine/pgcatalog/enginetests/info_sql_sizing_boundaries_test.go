package enginetests_test

import (
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestInfoSQLSizingNativeBoundaries(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	name := func(text string) parser.ObjectName {
		return parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: text}}}
	}
	create := func(table string, count int) *parser.CreateTable {
		s := &parser.CreateTable{Name: name(table), Elements: make([]parser.TableElement, count)}
		for i := range s.Elements {
			s.Elements[i].Column = &parser.Column{Name: fmt.Sprintf("c%04d", i+1), TypeName: parser.TypeName{Name: "integer"}}
		}
		return s
	}
	lookup := func(table string) *catalog.Relation {
		view := e.CatalogRead()
		addresses := view.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: table})
		if len(addresses) == 0 {
			return nil
		}
		if len(addresses) != 1 {
			t.Fatal("ambiguous fixture relation", addresses)
		}
		return view.MustRelation(addresses[0])
	}
	// Exercise actual analysis, staging and catalog publication from syntax
	// nodes, without a public driver or a second table-host implementation.
	if _, err := e.Execute(op, create("sz_unit_wide", 1600), 0, nil); err != nil {
		t.Fatal(err)
	}
	original := lookup("sz_unit_wide")
	if original == nil || len(original.Columns) != 1600 || original.ColumnMeta.HighWater != 1600 {
		t.Fatal("1600-column publication", original)
	}
	denied := func(s parser.Statement) {
		t.Helper()
		result, err := e.Execute(op, s, 0, nil)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54011", Message: "tables can have at most 1600 columns"}
		if !errors.As(err, &diagnostic) || *diagnostic != want || result.Rows != nil || result.Count != 0 {
			t.Fatal("table column bound diagnostic/result", result, err)
		}
	}
	denied(create("sz_unit_failed", 1601))
	if lookup("sz_unit_failed") != nil || lookup("sz_unit_wide") != original {
		t.Fatal("failed CREATE published metadata")
	}
	add := &parser.AlterTable{Name: name("sz_unit_wide"), Actions: []parser.AlterTableAction{{Action: parser.AddColumn, Definition: &parser.Column{Name: "c1601", TypeName: parser.TypeName{Name: "integer"}}}}}
	denied(add)
	if lookup("sz_unit_wide") != original {
		t.Fatal("failed ADD published metadata")
	}
	drop := &parser.AlterTable{Name: name("sz_unit_wide"), Actions: []parser.AlterTableAction{{Action: parser.DropColumn, Column: parser.Identifier{Text: "c0001"}, Mode: parser.DropRestrict}}}
	if _, err := e.Execute(op, drop, 0, nil); err != nil {
		t.Fatal(err)
	}
	afterDrop := lookup("sz_unit_wide")
	if afterDrop == original || len(afterDrop.Columns) != 1599 || afterDrop.ColumnMeta.HighWater != 1600 || len(original.Columns) != 1600 {
		t.Fatal("DROP high-water/source isolation", afterDrop, original)
	}
	denied(add)
	if lookup("sz_unit_wide") != afterDrop || afterDrop.ColumnMeta.HighWater != 1600 {
		t.Fatal("ADD reused dropped physical attribute")
	}
}
