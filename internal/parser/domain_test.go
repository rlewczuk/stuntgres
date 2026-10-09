package parser

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDomainRawDeclarationsAndPositions(t *testing.T) {
	q := `CREATE DOMAIN "Σ"."Amount" AS numeric(5,2) NULL`
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	d, ok := statements[0].(*CreateDomain)
	if !ok {
		t.Fatalf("%T", statements[0])
	}
	wantPosition := utf8.RuneCountInString(q[:strings.Index(q, "numeric")]) + 1
	if len(d.Name.Parts) != 2 || d.Name.Parts[0].Text != "Σ" || d.Name.Parts[1].Text != "Amount" || q[d.Name.Parts[1].Pos.Offset] != '"' || d.Base.Name != "numeric" || d.Base.Pos.Character != wantPosition || d.Base.Pos.Offset != strings.Index(q, "numeric") || len(d.Base.Modifiers) != 2 || *d.Base.Modifiers[0].Integer != 5 || *d.Base.Modifiers[1].Integer != 2 || len(d.Constraints) != 1 || d.Constraints[0].NotNull {
		t.Fatal(d)
	}
	for _, tc := range []struct {
		q          string
		dimensions []int32
		parts      int
	}{
		{"CREATE DOMAIN public.names AS name[]", []int32{0}, 1},
		{"CREATE DOMAIN public.names name[9][]", []int32{9, 0}, 1},
		{"CREATE DOMAIN public.alias AS public.names", nil, 2},
	} {
		statements, err := Parse(tc.q)
		if err != nil {
			t.Fatal(tc.q, err)
		}
		d := statements[0].(*CreateDomain)
		if !reflect.DeepEqual(d.Base.ArrayBounds, tc.dimensions) || (d.Base.Schema != nil) != (tc.parts == 2) {
			t.Fatal(tc, d.Base)
		}
	}
	statements, err = Parse("DROP DOMAIN IF EXISTS public.a,public.b CASCADE")
	if err != nil {
		t.Fatal(err)
	}
	drop := statements[0].(*DropDomain)
	if !drop.IfExists || drop.Mode != DropCascade || len(drop.Names) != 2 || drop.Names[1].Parts[1].Text != "b" {
		t.Fatal(drop)
	}
	statements, err = Parse("DROP DOMAIN public.a RESTRICT")
	if err != nil || statements[0].(*DropDomain).Mode != DropRestrict {
		t.Fatal(statements, err)
	}
	for _, q := range []string{"CREATE TABLE domain(domain INTEGER)", "SELECT domain FROM domain", "SET application_name TO domain"} {
		if _, err := Parse(q); err != nil {
			t.Fatal("DOMAIN became reserved", q, err)
		}
	}
}

func TestDomainConstraintASTAndCompleteSuffixes(t *testing.T) {
	for _, tc := range []struct {
		q     string
		check func(*CreateDomain) bool
	}{
		{"CREATE DOMAIN public.a AS integer NOT NULL", func(d *CreateDomain) bool { return len(d.Constraints) == 1 && d.Constraints[0].NotNull }},
		{"CREATE DOMAIN public.a AS integer DEFAULT 7", func(d *CreateDomain) bool { return len(d.Constraints) == 1 && d.Constraints[0].Default != nil }},
		{"CREATE DOMAIN public.a AS integer CHECK (VALUE > 0)", func(d *CreateDomain) bool { return len(d.Constraints) == 1 && d.Constraints[0].Check != nil }},
	} {
		ss, err := Parse(tc.q)
		if err != nil || len(ss) != 1 || !tc.check(ss[0].(*CreateDomain)) {
			t.Fatal(tc.q, ss, err)
		}
	}
	for _, q := range []string{"CREATE DOMAIN public.a AS integer(3)", "CREATE DOMAIN public.a AS integer NULL nonsense", "DROP DOMAIN public.a CASCADE nonsense", "ALTER DOMAIN public.a SET NOT NULL", "CREATE DOMAIN public.a AS integer; DROP DOMAIN public.a CASCADE nonsense"} {
		ss, err := Parse(q)
		if err == nil || ss != nil {
			t.Fatal("unsupported suffix accepted", q, ss, err)
		}
	}
}

func TestDomainUserTypeModifierSyntaxReachesResolver(t *testing.T) {
	for _, name := range []string{"custom", `"Custom Domain"`, "public.custom", `public."Custom Domain"`, "public.uuid", "public.bytea", "domain"} {
		q := "CREATE DOMAIN public.a AS " + name + "(3,4)"
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		base := statements[0].(*CreateDomain).Base
		if len(base.Modifiers) != 2 || *base.Modifiers[0].Integer != 3 || *base.Modifiers[1].Integer != 4 || base.Pos.Offset != strings.Index(q, name) {
			t.Fatal(q, base)
		}
	}
}

func TestDomainNameArrayProducerAssignmentSyntax(t *testing.T) {
	for _, call := range []string{"current_schemas(false)", "pg_catalog.current_schemas(false)"} {
		q := "INSERT INTO domain_s.arr VALUES(102," + call + ")"
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		insert := statements[0].(*Insert)
		if len(insert.Rows) != 1 || len(insert.Rows[0]) != 2 {
			t.Fatal(q, insert)
		}
		producer, ok := insert.Rows[0][1].(FunctionExpr)
		if !ok || len(producer.Args) != 1 || producer.Name.Parts[len(producer.Name.Parts)-1].Text != "current_schemas" {
			t.Fatal(q, insert.Rows[0][1])
		}
		argument, ok := producer.Args[0].(LiteralExpr)
		if !ok || argument.Value.Kind != BooleanValue || argument.Value.Boolean {
			t.Fatal(q, producer.Args)
		}
	}
}
