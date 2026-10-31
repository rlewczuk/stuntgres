package parser

import (
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestSecurityLabelGrammar(t *testing.T) {
	for _, tc := range []struct { target, kind string }{
		{"TABLE public.t", "table"}, {"COLUMN public.t.v", "column"}, {"COLUMN v", "column"},
		{"COLUMN db.s.t.v", "column"}, {"TABLE db.s.t", "table"},
		{"FUNCTION db.s.f(integer)", "function"}, {"AGGREGATE db.s.a(*)", "aggregate"},
		{"AGGREGATE public.a(*)", "aggregate"}, {"AGGREGATE a(IN n integer)", "aggregate"},
		{"AGGREGATE a(ORDER BY integer)", "aggregate"}, {"AGGREGATE a(integer ORDER BY integer)", "aggregate"},
		{"DATABASE missing", "database"}, {"DOMAIN public.d", "domain"}, {"DOMAIN integer[]", "domain"},
		{"EVENT TRIGGER missing", "event trigger"}, {"FOREIGN TABLE public.t", "foreign table"},
		{"FUNCTION public.f", "function"}, {"FUNCTION f()", "function"},
		{"FUNCTION f(IN n integer, OUT z text, INOUT a integer, VARIADIC rest text[])", "function"},
		{"FUNCTION f(missing.ty)", "function"}, {"LARGE OBJECT -1.2e3", "large object"},
		{"MATERIALIZED VIEW public.v", "materialized view"}, {"LANGUAGE x", "language"},
		{"PROCEDURAL LANGUAGE x", "language"}, {"PROCEDURE p", "procedure"}, {"PROCEDURE p(integer)", "procedure"},
		{"PUBLICATION x", "publication"}, {"ROLE x", "role"}, {"ROUTINE r", "routine"}, {"ROUTINE r(integer)", "routine"},
		{"SCHEMA x", "schema"}, {"SEQUENCE s", "sequence"}, {"SUBSCRIPTION x", "subscription"}, {"TABLESPACE x", "tablespace"},
		{"TYPE public.ty", "type"}, {"TYPE numeric(4,2)[]", "type"}, {"VIEW v", "view"}, {"INDEX i", "index"},
		{"TYPE db.s.ty[]", "type"}, {"TYPE int4(1)", "type"}, {"DOMAIN bool(1)", "domain"},
		{"FUNCTION f(db.s.ty[], int4(1))", "function"}, {"AGGREGATE a(db.s.ty ORDER BY bool(1))", "aggregate"},
		{"TYPE int4(abs(1))", "type"}, {"TYPE int4(1=1)", "type"}, {"TYPE int4((SELECT 1))", "type"},
		{"TYPE int4($1)", "type"}, {"TYPE db.s.ty(abs(1),1=1)", "type"}, {"TYPE bit(abs(1))", "type"},
		{"TYPE int4(1::int4(abs(2)))", "type"},
		{"TYPE SETOF integer", "type"}, {"DOMAIN SETOF integer[]", "domain"},
		{"FUNCTION f(SETOF integer, IN n SETOF text)", "function"},
		{"FUNCTION f(t.v%TYPE, SETOF db.t.v%TYPE)", "function"}, {"AGGREGATE a(t.v%TYPE)", "aggregate"},
		{"TYPE int4(setof)", "type"}, {"TABLE setof", "table"}, {`TYPE "setof"`, "type"},
		{"TYPE json", "type"}, {`TYPE "json"(1)`, "type"}, {"TYPE public.json(1)", "type"},
		{"TYPE pg_catalog.json(1)", "type"}, {`TYPE "pg_catalog"."json"(1)`, "type"},
		{"TYPE nchar", "type"}, {"TYPE national char", "type"}, {"TYPE national character", "type"},
		{"TYPE nchar(3)", "type"}, {"TYPE national char(3)", "type"}, {"TYPE national character(3)", "type"},
		{"TYPE nchar varying", "type"}, {"TYPE national char varying", "type"}, {"TYPE national character varying", "type"},
		{"TYPE nchar varying(3)", "type"}, {"TYPE national char varying(3)", "type"}, {"TYPE national character varying(3)", "type"},
		{"TYPE nchar(0)", "type"}, {"TYPE nchar varying(0)", "type"}, {"DOMAIN national character varying(3)[]", "domain"},
		{"FUNCTION f(nchar,national char varying(3))", "function"}, {"PROCEDURE p(national character)", "procedure"},
		{"AGGREGATE a(nchar)", "aggregate"}, {"TYPE SETOF national character[]", "type"},
		{`TYPE "nchar"(abs(1))`, "type"}, {"TYPE s.nchar(abs(1))", "type"}, {"TYPE pg_catalog.nchar(1)", "type"},
		{`TYPE "nchar".ty(1)`, "type"}, {`TYPE "nchar".ty`, "type"},
		{"TYPE s.nchar(1)", "type"}, {`TYPE s."nchar"(1)`, "type"},
		{"TYPE s.nchar", "type"}, {`TYPE s."nchar"`, "type"},
		{`TYPE "national"(1)`, "type"}, {`TYPE "national".ty(1)`, "type"}, {"TYPE s.national(1)", "type"},
		{"TABLE nchar", "table"}, {"TABLE national", "table"}, {"TABLE national.nchar", "table"},
		{"TYPE int4(CAST(1 AS nchar))", "type"}, {"TYPE int4(1::national char varying(3))", "type"},
		{"COLLATION c", "collation"}, {"CONVERSION c", "conversion"}, {"STATISTICS s", "statistics"},
		{"TEXT SEARCH PARSER p", "text search parser"}, {"TEXT SEARCH DICTIONARY d", "text search dictionary"},
		{"TEXT SEARCH TEMPLATE t", "text search template"}, {"TEXT SEARCH CONFIGURATION c", "text search configuration"},
		{"ACCESS METHOD m", "access method"}, {"EXTENSION e", "extension"},
		{"FOREIGN DATA WRAPPER f", "foreign-data wrapper"}, {"SERVER s", "server"},
	} {
		q := "SECURITY LABEL ON " + tc.target + " IS NULL; -- complete input"
		stmts, err := Parse(q)
		if err != nil || len(stmts) != 1 { t.Fatalf("%s: %v %#v", q, err, stmts) }
		s, ok := stmts[0].(*SecurityLabel)
		if !ok || s.Kind != tc.kind || !s.Null || s.Text != "" || s.ProviderSpecified || s.Pos != (Position{Offset: 0, Character: 1}) {
			t.Fatalf("%s: %#v", q, s)
		}
		visited := false
		if err := VisitQueryExpressions(s, func(Expr) error { visited = true; return nil }); err != nil || visited { t.Fatal("expression-free leaf", err) }
		if len(s.Name.Parts) != 0 && s.Name.Parts[0].Pos.Character <= 18 { t.Fatalf("target component position: %s %#v", q, s.Name) }
	}
	stmts, err := Parse(`/*雪*/ SECURITY LABEL FOR 'Provider' ON COLUMN "s.dot"."T"."v" IS E'quote\'\n雪'`)
	if err != nil { t.Fatal(err) }
	s := stmts[0].(*SecurityLabel)
	if s.Pos.Offset != 8 || s.Pos.Character != 7 || s.Provider.Pos.Offset != 27 || s.Name.Parts[0].Text != "s.dot" || s.Name.Parts[1].Text != "T" || s.Name.Parts[2].Text != "v" || s.Text != "quote'\n雪" || s.Null || s.LabelPos.Character == 0 { t.Fatalf("%#v", s) }
	if _, err := Parse(`SECURITY LABEL ON TABLE other IS ''; CREATE TABLE security(label INT); SELECT label FROM security`); err != nil { t.Fatal(err) }
	if s.Provider.Text != "Provider" || s.Name.Parts[1].Text != "T" || s.Text != "quote'\n雪" { t.Fatal("parse scratch retained", s) }
	stmts, err = Parse(`SECURITY LABEL ON TABLE t IS ''`)
	if err != nil || stmts[0].(*SecurityLabel).Null { t.Fatal(stmts, err) }
	stmts, err = Parse(`SECURITY LABEL ON FUNCTION db.s.f(IN n integer, OUT z text, INOUT a integer, VARIADIC rest text[]) IS NULL`)
	if err != nil { t.Fatal(err) }
	r := stmts[0].(*SecurityLabel).Routine
	if len(r.Name.Parts) != 3 || r.Name.Parts[0].Text != "db" || r.Name.Parts[2].Text != "f" || r.Name.Parts[0].Pos.Character != 28 || !r.SignatureSpecified || len(r.Arguments) != 4 { t.Fatalf("%#v", r) }
	for i, mode := range []string{"in", "out", "inout", "variadic"} {
		if r.Arguments[i].Mode != mode || r.Arguments[i].Pos.Character == 0 || r.Arguments[i].Type.Pos.Character == 0 { t.Fatalf("%#v", r.Arguments[i]) }
	}
	if r.Arguments[0].Name.Text != "n" || len(r.Arguments[3].Type.ArrayBounds) != 1 { t.Fatalf("%#v", r.Arguments) }
	stmts, err = Parse(`SECURITY LABEL ON LARGE OBJECT -1.2e3 IS NULL`)
	if err != nil || stmts[0].(*SecurityLabel).Number.Text != "-1.2e3" || stmts[0].(*SecurityLabel).Number.Pos.Character != 32 { t.Fatal(stmts, err) }
	stmts, err = Parse(`SECURITY LABEL ON FUNCTION f(SETOF db.t.v%TYPE) IS NULL`)
	if err != nil { t.Fatal(err) }
	copyType := stmts[0].(*SecurityLabel).Routine.Arguments[0].Type
	if !copyType.Setof || !copyType.PctType || len(copyType.Parts) != 3 || copyType.Parts[2].Text != "v" { t.Fatalf("%#v", copyType) }
	for _, q := range []string{
		`SECURITY LABEL ON TYPE int4(1) IS NULL; SECURITY LABEL ON DOMAIN bool(1) IS ''`,
		`CREATE TABLE before_label(v INT); SECURITY LABEL ON TYPE db.s.ty(1)[] IS NULL`,
		`SECURITY LABEL ON TYPE int4(1) IS NULL; CREATE TABLE after_label(v INT)`,
		`SECURITY LABEL ON TYPE int4(1::int4(abs(2))) IS NULL; SECURITY LABEL ON TYPE int4($1) IS NULL`,
		`SECURITY LABEL FOR setof ON TYPE SETOF integer IS NULL; SELECT setof FROM setof`,
		`SECURITY LABEL ON TYPE national character varying(3)[] IS NULL; SELECT national,nchar FROM national`,
		`SELECT national,nchar FROM national; SECURITY LABEL ON TYPE nchar IS NULL`,
	} {
		if stmts, err := Parse(q); err != nil || len(stmts) != 2 { t.Fatal(q, stmts, err) }
	}
	for _, q := range []string{
		`SECURITY LABEL ON TYPE int4(1) IS NULL; CREATE TABLE t(v int4(1))`,
		`CREATE TABLE t(v int4(1)); SECURITY LABEL ON TYPE int4(1) IS NULL`,
		`CREATE TABLE t(v int4(1))`,
	} {
		stmts, err := Parse(q)
		var d *sqlstate.Error
		if len(stmts) != 0 || !errors.As(err, &d) || d.Code != "0A000" || d.Message != "type modifiers are unsupported" { t.Fatal("syntax mode leaked", q, stmts, err) }
	}
	for _, ordinary := range []string{"CREATE TABLE t(v national character)", "CREATE TABLE t(v nchar varying)"} {
		for _, query := range []string{ordinary, "SECURITY LABEL ON TYPE nchar IS NULL; " + ordinary, ordinary + "; SECURITY LABEL ON TYPE national char IS NULL"} {
			statements, err := Parse(query)
			var diagnostic *sqlstate.Error
			if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" { t.Fatalf("ordinary character grammar widened: %s: statements=%#v error=%v", query, statements, err) }
		}
	}
	for _, q := range []string{
		`SECURITY LABEL ON TYPE int4(1+) IS NULL`,
		`SECURITY LABEL ON TYPE int4(abs(,1)) IS NULL`,
	} {
		stmts, err := Parse(q)
		var d *sqlstate.Error
		if len(stmts) != 0 || !errors.As(err, &d) || d.Code != "42601" { t.Fatal(q, stmts, err) }
	}
	for _, mode := range []string{"OUT", "INOUT"} {
		stmts, err := Parse("SECURITY LABEL ON AGGREGATE a(" + mode + " n integer) IS NULL")
		var d *sqlstate.Error
		if len(stmts) != 0 || !errors.As(err, &d) || d.Code != "0A000" || d.Message != "aggregates cannot have output arguments" || d.Position != "31" { t.Fatal(mode, stmts, err) }
	}
}

func TestSecurityLabelProviderNames(t *testing.T) {
	for _, tc := range []struct { sql, text string }{
		{"MissingProvider", "missingprovider"}, {`"MissingProvider"`, "MissingProvider"}, {"'MissingProvider'", "MissingProvider"},
		{"''", ""}, {`'a"b'`, `a"b`}, {"'a''b'", "a'b"}, {`E'a\nb'`, "a\nb"}, {"'雪'", "雪"},
		{"between", "between"}, {"authorization", "authorization"}, {"is", "is"}, {"label", "label"}, {"security", "security"},
		{"setof", "setof"},
		{"nchar", "nchar"}, {"national", "national"},
		{"'" + strings.Repeat("X", 80) + "'", strings.Repeat("X", 80)},
	} {
		q := "SECURITY LABEL FOR " + tc.sql + " ON TABLE t IS NULL"
		stmts, err := Parse(q)
		if err != nil { t.Fatal(q, err) }
		s := stmts[0].(*SecurityLabel)
		if !s.ProviderSpecified || s.Provider.Text != tc.text || s.Provider.Pos.Offset != 19 { t.Fatalf("%s: %#v", q, s) }
	}
}

func TestSecurityLabelSyntaxErrors(t *testing.T) {
	for _, tc := range []struct { query, token, position string }{
		{`SECURITY LABEL ON TYPE json(1) IS NULL`, "(", "28"},
		{`SECURITY LABEL ON TYPE JSON(abs(1)) IS NULL`, "(", "28"},
		{`SECURITY LABEL ON TYPE int4(CAST(1 AS json(1))) IS NULL`, "(", "43"},
		{`SECURITY LABEL ON TYPE nchar(1+1) IS NULL`, "+", "31"},
		{`SECURITY LABEL ON TYPE nchar(abs(1)) IS NULL`, "abs", "30"},
		{`SECURITY LABEL ON TYPE nchar varying(1,2) IS NULL`, ",", "39"},
		{`SECURITY LABEL ON TYPE national IS NULL`, "IS", "33"},
		{`SECURITY LABEL ON TYPE national character(1+1) IS NULL`, "+", "44"},
		{`SECURITY LABEL ON TYPE national.ty(1) IS NULL`, ".", "32"},
		{`SECURITY LABEL ON TYPE nchar.ty(1) IS NULL`, ".", "29"},
		{`SECURITY LABEL ON TYPE nchar.ty IS NULL`, ".", "29"},
	} {
		statements, err := Parse(tc.query)
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: tc.position}
		if len(statements) != 0 || !errors.As(err, &diagnostic) || *diagnostic != *want { t.Fatalf("%s: statements=%#v error=%#v want=%#v", tc.query, statements, diagnostic, want) }
	}
	for _, q := range []string{
		`SECURITY LABEL ON TABLE t IS $1`, `SECURITY LABEL ON TABLE t IS 1`, `SECURITY LABEL ON TABLE t IS x`,
		`SECURITY LABEL ON TABLE t IS 'x' || 'y'`, `SECURITY LABEL ON TABLE t IS`, `SECURITY LABEL ON TABLE t IS NULL junk`,
		`SECURITY LABEL FOR ON TABLE t IS NULL`, `SECURITY LABEL ON AGGREGATE a(integer IS NULL`,
		`SECURITY LABEL ON COLUMN t. IS NULL`, `SECURITY "label" ON TABLE t IS NULL`,
		`SECURITY LABEL ON TABLE t IS NULL; SELECT )`, `SECURITY LABEL FOR select ON TABLE t IS NULL`,
		`PREPARE sl AS SECURITY LABEL ON TABLE t IS NULL`, `EXPLAIN SECURITY LABEL ON TABLE t IS NULL`,
		`SECURITY LABEL ON TYPE integer(1) IS NULL`, `SECURITY LABEL ON DOMAIN boolean(1) IS NULL`,
		`SECURITY LABEL ON FUNCTION f(t.v%) IS NULL`,
		`SECURITY LABEL ON TYPE t.v%TYPE IS NULL`,
	} {
		stmts, err := Parse(q)
		var d *sqlstate.Error
		if len(stmts) != 0 || !errors.As(err, &d) || d.Code != "42601" || d.Position == "" { t.Fatalf("%s: %#v %v", q, stmts, err) }
	}
}

func TestSecurityLabelParser(t *testing.T) {
 for _, target := range []string{"ROLE", "DATABASE", "TABLESPACE", "SUBSCRIPTION", "TABLE"} {
  for _, tc := range []struct{ clause, provider string; present bool }{
   {"", "", false}, {" FOR MiSsInG", "missing", true}, {" FOR \"MiSsInG\"", "MiSsInG", true},
   {" FOR 'MiSsInG'", "MiSsInG", true}, {" FOR ''", "", true}, {" FOR 'quo''te'", "quo'te", true},
   {` FOR "quo""te"`, `quo"te`, true}, {` FOR E'back\\slash'`, `back\slash`, true},
  } {
   for _, label := range []string{"'value'", "NULL"} {
    q := "SECURITY LABEL"+tc.clause+" ON "+target+" target IS "+label
    stmts, err := Parse(q)
    if err != nil || len(stmts)!=1 { t.Fatal(q, stmts, err) }
    s, ok := stmts[0].(*SecurityLabel)
    if !ok || s.Kind!=strings.ToLower(target) || len(s.Name.Parts)!=1 || s.Name.Parts[0].Text!="target" || s.ProviderSpecified!=tc.present || tc.present && s.Provider.Text!=tc.provider || s.Null!=(label=="NULL") || !s.Null && s.Text!="value" || s.Pos.Character!=1 { t.Fatal(q,s) }
   }
  }
 }
 for _, q := range []string{"SECURITY LABEL ON ROLE r IS 'x' trailing", "SECURITY LABEL FOR ON ROLE r IS 'x'", "SECURITY LABEL ON ROLE r IS $1", "SECURITY LABEL ON ROLE r IS", "SECURITY other ON ROLE r IS NULL"} {
  if _, err := Parse(q); err==nil { t.Fatal("accepted malformed syntax",q) }
 }
 for _, q := range []string{"CREATE TABLE label(label text)", "CREATE FUNCTION label() RETURNS int LANGUAGE SQL SECURITY DEFINER AS 'SELECT 1'", "ALTER TABLE label ENABLE ROW LEVEL SECURITY"} {
  if _, err := Parse(q); err!=nil { t.Fatal("existing security/label syntax",q,err) }
 }
}

func TestSecurityLabelEmbeddedParser(t *testing.T) {
 provider,labelText:="MiSsInG","label"
 for _,tc:=range []struct{
  command string
  provider,label *string
 }{
  {command:`SECURITY LABEL ON ROLE missing IS NULL`},
  {command:`SECURITY LABEL FOR "MiSsInG" ON ROLE missing IS 'label'`,provider:&provider,label:&labelText},
 } {
  source:="DECLARE security int; BEGIN security := 1; "+tc.command+"; security := 2; END"
  block,err:=ParsePLpgSQL(source,PLReturnPolicy{})
  if err!=nil || block==nil{t.Fatal(source,err)}
  if len(block.Declarations)!=1 || block.Declarations[0].Name.Text!="security" || len(block.Statements)!=3{t.Fatal("procedural declarations/statements",block)}
  command:=block.Statements[1]
  raw:=command.SQL
  if raw==nil || !ValidPLSQLStatement(raw) || raw.Kind!=PLCommandSQL || raw.Text!=tc.command || command.Target!=nil || command.Expression!=nil || command.Into!=nil{t.Fatal("security label not captured as ordinary utility",command)}
  label,ok:=raw.Query.(*SecurityLabel)
  if !ok || label.Kind!="role" || len(label.Name.Parts)!=1 || label.Name.Parts[0].Text!="missing" || label.ProviderSpecified!=(tc.provider!=nil) || tc.provider!=nil && label.Provider.Text!=*tc.provider || label.Null!=(tc.label==nil) || tc.label!=nil && label.Text!=*tc.label{t.Fatal("embedded security label fields",raw.Query)}
  for _,index:=range []int{0,2}{
   assignment:=block.Statements[index]
   target,ok:=assignment.Target.(ColumnExpr)
   if !ok || len(target.Name.Parts)!=1 || target.Name.Parts[0].Text!="security" || assignment.SQL!=nil || assignment.Into!=nil{t.Fatal("security assignment captured as utility",assignment)}
   literal,ok:=assignment.Expression.(LiteralExpr)
   want:=int64(1);text:="1";if index==2{want,text=2,"2"}
   if !ok || literal.Value.Kind!=IntegerValue || literal.Value.Integer!=want || PLExpressionSource(source,assignment.ExpressionStart,assignment.ExpressionEnd)!=text{t.Fatal("security assignment literal changed",assignment)}
  }
 }
}
