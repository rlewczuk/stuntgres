package parser

import (
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestBtreeIndexSyntaxAndOptions(t *testing.T) {
	statements, err := Parse(`CREATE INDEX ON t(n); CREATE INDEX IF NOT EXISTS "I" ON public."T" USING btree ("N" DESC NULLS LAST,index ASC NULLS FIRST,n); CREATE TABLE index(index INT)`)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 3 {
		t.Fatal(statements)
	}
	omitted := statements[0].(*CreateIndex)
	if omitted.Name.Text != "" || omitted.Method != "btree" || omitted.Keys[0].Nulls != NullsDefault {
		t.Fatal(omitted)
	}
	index := statements[1].(*CreateIndex)
	if index.Name.Text != "I" || !index.IfNotExists || len(index.Table.Parts) != 2 || index.Table.Parts[1].Text != "T" || index.Method != "btree" {
		t.Fatal(index)
	}
	want := []IndexKey{{Column: index.Keys[0].Column, Desc: true, Nulls: NullsLast}, {Column: index.Keys[1].Column, Nulls: NullsFirst}, {Column: index.Keys[2].Column, Nulls: NullsDefault}}
	if !reflect.DeepEqual(index.Keys, want) || index.Keys[0].Column.Text != "N" || index.Keys[1].Column.Text != "index" {
		t.Fatal(index.Keys, want)
	}
	if index.Pos.Character == 0 || index.Name.Pos.Character == 0 || index.Keys[0].Column.Pos.Character == 0 {
		t.Fatal("lost positions", index)
	}
}
func TestBtreeIndexSyntaxDiagnostics(t *testing.T) {
	for _, tc := range []struct{ query, message, position string }{
		{"CREATE INDEX s.i ON t(n)", `syntax error at or near "."`, "15"},
		{"CREATE INDEX i ON t ()", `syntax error at or near ")"`, "22"},
		{"/*é*/ CREATE INDEX s.i ON t(n)", `syntax error at or near "."`, "21"},
	} {
		_, err := Parse(tc.query)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: tc.position}
		if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) {
			t.Fatalf("%s: got %#v want %#v: %v", tc.query, got, want, err)
		}
	}
}
func TestBtreeIndexExcludedDefinitions(t *testing.T) {
	for _, tc := range []struct{ query, message, position string }{
		{"CREATE UNIQUE INDEX i ON t(n)", "unique indexes are unsupported", "8"},
		{"CREATE INDEX CONCURRENTLY i ON t(n)", "concurrent indexes are unsupported", "14"},
		{"CREATE INDEX i ON t((n+1))", "expression indexes are unsupported", "21"},
		{"CREATE INDEX ix ON t((c+1))", "expression indexes are unsupported", "22"},
		{"CREATE INDEX ix ON t ((c+1))", "expression indexes are unsupported", "23"},
		{"/*é*/ CREATE INDEX ix ON t((c+1))", "expression indexes are unsupported", "28"},
		{"CREATE INDEX i ON t(n) WHERE n>0", "partial indexes are unsupported", "24"},
		{"CREATE INDEX i ON t(s COLLATE \"C\")", "index collations are unsupported", "23"},
		{"CREATE INDEX i ON t(n int4_ops)", "index operator classes are unsupported", "23"},
		{"CREATE INDEX i ON t(n) INCLUDE(s)", "included index columns are unsupported", "24"},
		{"CREATE INDEX i ON t(n) WITH(fillfactor=80)", "index storage parameters are unsupported", "24"},
		{"CREATE INDEX i ON t(n) TABLESPACE pg_default", "index tablespaces are unsupported", "24"},
	} {
		_, err := Parse(tc.query)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: tc.message, Position: tc.position}
		if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) {
			t.Fatalf("%s: got %#v want %#v: %v", tc.query, got, want, err)
		}
	}
}
