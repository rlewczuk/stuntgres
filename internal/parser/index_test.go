package parser

import (
	"errors"
	"reflect"
	"strings"
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
	want := []IndexKey{
		{Column: index.Keys[0].Column, Pos: index.Keys[0].Column.Pos, Desc: true, DirectionExplicit: true, Nulls: NullsLast},
		{Column: index.Keys[1].Column, Pos: index.Keys[1].Column.Pos, DirectionExplicit: true, Nulls: NullsFirst},
		{Column: index.Keys[2].Column, Pos: index.Keys[2].Column.Pos, Nulls: NullsDefault},
	}
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
		{"CREATE INDEX CONCURRENTLY i ON t(n)", "concurrent indexes are unsupported", "14"},
		{"CREATE INDEX i ON t(n) WHERE n>0", "partial indexes are unsupported", "24"},
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

func TestUniqueIndexPolicySyntax(t *testing.T) {
	for _, query := range []string{
		"CREATE UNIQUE INDEX ON t(a)",
		"CREATE UNIQUE INDEX u ON t(a DESC NULLS FIRST,b) INCLUDE(p) NULLS NOT DISTINCT",
		"CREATE UNIQUE INDEX IF NOT EXISTS u ON t(a) NULLS DISTINCT",
		"CREATE INDEX u ON t(a) NULLS NOT DISTINCT",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		index := statements[0].(*CreateIndex)
		if index.Unique != strings.HasPrefix(query, "CREATE UNIQUE") || index.NullsNotDistinct != strings.HasSuffix(query, "NULLS NOT DISTINCT") {
			t.Fatal(query, index)
		}
	}
}

func TestExpressionIndexSyntax(t *testing.T) {
	for _, q := range []string{"CREATE INDEX ix ON t(lower(v))", "CREATE INDEX ix ON t(pg_catalog.lower(v))", "CREATE INDEX ix ON t(pg_catalog.random())", "CREATE INDEX ix ON t((n+1))", "CREATE INDEX ix ON t((count(*)))", "CREATE INDEX ix ON t(((SELECT n FROM t)))", "CREATE INDEX ix ON t((1+(SELECT n FROM t)))", "CREATE INDEX ix ON t(((VALUES (1))))"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		key := statements[0].(*CreateIndex).Keys[0]
		if key.Expression == nil || key.Column.Text != "" {
			t.Fatal(q, key)
		}
	}
}

func TestIndexClausesAST(t *testing.T) {
	statements, err := Parse(`CREATE INDEX "é" ON public.t(s COLLATE pg_catalog."C" pg_catalog.text_pattern_ops DESC NULLS FIRST,n int4_ops(foo=-1,ASC=+1)) INCLUDE((n),lower(s),s COLLATE missing,n missing_ops,n ASC,n NULLS LAST,pg_catalog.lower(s),"pg_catalog"."lower"(missing)); CREATE TABLE include(include INT,tablespace TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	d := statements[0].(*CreateIndex)
	if len(d.Keys) != 2 || len(d.Include) != 8 || !d.Keys[0].Desc || !d.Keys[0].DirectionExplicit || d.Keys[0].Nulls != NullsFirst || d.Keys[0].Collation.Parts[1].Text != "C" || d.Keys[0].Opclass.Parts[1].Text != "text_pattern_ops" {
		t.Fatal(d)
	}
	if len(d.Keys[1].Options) != 2 || d.Keys[1].Options[0].Name.Text != "foo" || d.Keys[1].Options[0].Value != "-1" || d.Keys[1].Options[1].Name.Text != "asc" || d.Keys[1].Options[1].Value != "+1" {
		t.Fatal(d.Keys[1])
	}
	if d.Include[0].Expression == nil || d.Include[1].Expression == nil || d.Include[2].Collation == nil || d.Include[3].Opclass == nil || !d.Include[4].DirectionExplicit || d.Include[4].Desc || d.Include[5].Nulls != NullsLast || d.Include[6].Expression == nil || d.Include[7].Expression == nil {
		t.Fatal(d.Include)
	}
	if d.Keys[0].Pos.Character == 0 || d.Include[0].Pos.Character == 0 || len(statements) != 2 {
		t.Fatal("lost shape, complete input or source positions", d)
	}
}

func TestIndexClausesConditionalShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind ConditionalKind
	}{{"coalesce", Coalesce}, {"nullif", NullIf}, {"greatest", Greatest}, {"least", Least}} {
		q := "CREATE INDEX ix ON t(" + tc.name + "(n,n) int4_ops) INCLUDE(" + tc.name + "(missing,missing))"
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		d := statements[0].(*CreateIndex)
		for _, raw := range []IndexKey{d.Keys[0], d.Include[0]} {
			expr, ok := raw.Expression.(ConditionalExpr)
			if !ok || expr.Kind != tc.kind || len(expr.Args) != 2 || expr.Pos.Character == 0 || raw.Column.Text != "" {
				t.Fatal("SQL-special index syntax lost shared conditional AST", q, raw)
			}
		}
	}
	statements, err := Parse(`CREATE INDEX ix ON t("coalesce"(n,n)) INCLUDE(pg_catalog.coalesce(missing,missing))`)
	if err != nil {
		t.Fatal(err)
	}
	d := statements[0].(*CreateIndex)
	if _, ok := d.Keys[0].Expression.(FunctionExpr); !ok {
		t.Fatal("quoted ordinary function became SQL-special syntax", d.Keys[0])
	}
	if _, ok := d.Include[0].Expression.(FunctionExpr); !ok {
		t.Fatal("qualified ordinary function became SQL-special syntax", d.Include[0])
	}
}
