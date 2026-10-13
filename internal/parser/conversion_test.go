package parser

import (
	"strings"
	"testing"
)

func TestConversionParserContract(t *testing.T) {
	statements, err := Parse(`CREATE DEFAULT CONVERSION s."Quoted" FOR 'u_t-f8' TO 'ISO-8859-1' FROM pg_catalog.utf8_to_iso8859_1; ALTER CONVERSION s."Quoted" OWNER TO CURRENT_ROLE; ALTER CONVERSION s."Quoted" SET SCHEMA next; ALTER CONVERSION next."Quoted" RENAME TO conversion; DROP CONVERSION IF EXISTS next.conversion,public.other CASCADE`)
	if err != nil || len(statements) != 5 {
		t.Fatal(statements, err)
	}
	create := statements[0].(*CreateConversion)
	if create.Name.Parts[1].Text != "Quoted" || create.Source != "u_t-f8" || create.Destination != "ISO-8859-1" || !create.Default || len(create.Function.Parts) != 2 || create.Function.Parts[1].Text != "utf8_to_iso8859_1" {
		t.Fatal("lost CREATE literal/identity", create)
	}
	owner := statements[1].(*AlterConversion)
	if owner.Action != "owner" || !owner.OwnerIdentity || owner.Target.Text != "current_role" {
		t.Fatal("lost role identity operand", owner)
	}
	drop := statements[4].(*DropConversion)
	if !drop.IfExists || drop.Mode != DropCascade || len(drop.Names) != 2 {
		t.Fatal("lost DROP list/mode", drop)
	}
	long := "UTF8" + strings.Repeat("-", 60)
	statements, err = Parse(`CREATE CONVERSION conversion FOR '` + long + `' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1`)
	if err != nil || statements[0].(*CreateConversion).Source != long {
		t.Fatal("Sconst clipped as NAME", statements, err)
	}
	for _, query := range []string{
		`CREATE CONVERSION c FOR UTF8 TO 'LATIN1' FROM f`, `CREATE CONVERSION c FOR $1 TO 'LATIN1' FROM f`,
		`CREATE OR REPLACE CONVERSION c FOR 'UTF8' TO 'LATIN1' FROM f`, `CREATE CONVERSION IF NOT EXISTS c FOR 'UTF8' TO 'LATIN1' FROM f`,
		`ALTER CONVERSION IF EXISTS c RENAME TO d`, `ALTER CONVERSION c SET DEFAULT`, `DROP DEFAULT CONVERSION c`,
		`CREATE CONVERSION c FOR 'UTF8' TO 'LATIN1' FROM f; extra`,
		`CREATE TABLE typed(v arbitrary conversion)`,
	} {
		if parsed, err := Parse(query); err == nil || parsed != nil {
			t.Fatal("unsupported grammar/prefix admitted", query, parsed, err)
		}
	}
	if _, err := Parse(`CREATE TABLE conversion(conversion INTEGER); SELECT conversion FROM conversion`); err != nil {
		t.Fatal("unreserved conversion name routing changed", err)
	}
	statements, err = Parse(`CREATE TABLE typed(v conversion); SELECT conversion FROM conversion`)
	if err != nil || statements[0].(*CreateTable).Elements[0].Column.TypeName.Name != "conversion" {
		t.Fatal("promoted unreserved type-name identity changed", statements, err)
	}
}
