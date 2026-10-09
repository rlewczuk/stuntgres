package parser

import (
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestIndexMaintenanceSyntax(t *testing.T) {
	statements, err := Parse(`DROP INDEX IF EXISTS public."I",index,index CASCADE; ALTER INDEX IF EXISTS "S"."I" RENAME TO "New"; ALTER INDEX index SET(fillfactor=80.0,deduplicate_items='yes',toast.fillfactor=80); REINDEX (verbose false,concurrently true,tablespace pg_default) INDEX CONCURRENTLY public.index; REINDEX TABLE reindex`)
	if err != nil || len(statements) != 5 {
		t.Fatal(statements, err)
	}
	drop := statements[0].(*DropIndex)
	if !drop.IfExists || drop.Mode != DropCascade || len(drop.Names) != 3 || drop.Names[0].Parts[1].Text != "I" || drop.Names[1].Parts[0].Text != "index" || drop.Concurrent.Character != 0 {
		t.Fatal(drop)
	}
	rename := statements[1].(*AlterIndex)
	if !rename.IfExists || rename.Name.Parts[0].Text != "S" || rename.Target.Text != "New" || rename.Action != "rename" || rename.ActionPos.Character == 0 {
		t.Fatal(rename)
	}
	options := statements[2].(*AlterIndex)
	if options.Action != "set" || len(options.Options) != 3 || options.Options[0].Value.Text != "80.0" || !options.Options[0].HasValue || options.Options[1].Value.Text != "yes" || options.Options[2].Namespace != "toast" || options.Options[2].Name.Text != "fillfactor" {
		t.Fatal(options)
	}
	reindex := statements[3].(*Reindex)
	if reindex.Kind.Text != "index" || len(reindex.Options) != 3 || reindex.Options[0].Value.Text != "false" || reindex.Options[1].Value.Text != "true" || reindex.Options[2].Value.Text != "pg_default" || reindex.Concurrent.Character == 0 || len(reindex.Name.Parts) != 2 {
		t.Fatal(reindex)
	}
	if table := statements[4].(*Reindex); table.Kind.Text != "table" || table.Name.Parts[0].Text != "reindex" || table.Options != nil {
		t.Fatal(table)
	}
}

func TestIndexMaintenanceSemanticControlsRemainSyntax(t *testing.T) {
	for _, q := range []string{
		"ALTER INDEX missing SET(fillfactor=9)", "ALTER INDEX IF EXISTS missing RESET(fillfactor=80)",
		"ALTER INDEX ix SET(deduplicate_items)", "ALTER INDEX ix SET(fillfactor=-1)",
		"ALTER INDEX ix RESET(unknown,fillfactor,fillfactor,toast.fillfactor)",
		"ALTER INDEX ix SET TABLESPACE pg_default", "ALTER INDEX ei ALTER COLUMN 1 SET STATISTICS 10",
		"REINDEX (parallel -1) INDEX ix", "REINDEX (verbose 'yes') TABLE t",
		"REINDEX (concurrently false) INDEX ix", "REINDEX (verbose) INDEX ix",
		"REINDEX SCHEMA public", "REINDEX DATABASE stuntgres_reference", "REINDEX SYSTEM stuntgres_reference",
		"DROP INDEX CONCURRENTLY IF EXISTS ix", "DROP INDEX ix RESTRICT",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal("semantic control rejected during syntax-only preparation", q, statements, err)
		}
	}
	statements, err := Parse("/* é */ REINDEX (unknown) INDEX ix")
	if err != nil {
		t.Fatal(err)
	}
	if pos := statements[0].(*Reindex).Options[0].Name.Pos.Character; pos != 18 {
		t.Fatal("character position is not Unicode-aware", pos)
	}
}

func TestIndexMaintenanceNameEligibility(t *testing.T) {
	q := `CREATE TABLE reindex(index INT,reindex INT);CREATE INDEX index ON reindex(reindex); ALTER INDEX index RENAME TO reindex; DROP INDEX reindex`
	statements, err := Parse(q)
	if err != nil || len(statements) != 4 {
		t.Fatal(statements, err)
	}
	if index := statements[1].(*CreateIndex); index.Name.Text != "index" || index.Table.Parts[0].Text != "reindex" || index.Keys[0].Column.Text != "reindex" {
		t.Fatal(index)
	}
	for _, q := range []string{"SELECT 1::int4 AS reindex", "SELECT 1::reindex"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal("REINDEX lost ordinary name/type eligibility", q, statements, err)
		}
	}
}

func TestIndexMaintenanceWholeInputDiagnostics(t *testing.T) {
	for _, c := range []struct{ q, message, pos string }{
		{"REINDEX (verbose=false) INDEX ix", `syntax error at or near "="`, "17"},
		{"REINDEX (concurrently=false) INDEX ix", `syntax error at or near "="`, "22"},
		{"REINDEX (tablespace=pg_default) INDEX ix", `syntax error at or near "="`, "20"},
		{"REINDEX (parallel=-1) INDEX ix", `syntax error at or near "="`, "18"},
		{"ALTER INDEX ix RENAME TO fresh; SELECT FROM", "syntax error at end of input", "44"},
		{"DROP INDEX ix; SELECT FROM", "syntax error at end of input", "27"},
		{"SELECT 1::int4 reindex", `syntax error at or near "reindex"`, "16"},
		{"SELECT 1::int4 index", `syntax error at or near "index"`, "16"},
	} {
		_, err := Parse(c.q)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: c.message, Position: c.pos}
		if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) {
			t.Fatalf("%s: got %#v, want %#v: %v", c.q, got, want, err)
		}
	}
}
