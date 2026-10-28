package parser

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestPartitionDetachAST(t *testing.T) {
	for _, target := range []string{`"schema"."Parent"`, `ONLY "schema"."Parent"`, `IF EXISTS "schema"."Parent"`, `"schema"."Parent" *`} {
		q := `/*é*/ ALTER TABLE ` + target + ` DETACH PARTITION "schema"."Child"`
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		d, ok := statements[0].(*AlterTableDetachPartition)
		if !ok || len(d.Name.Parts) != 2 || d.Name.Parts[1].Text != "Parent" || len(d.Partition.Parts) != 2 || d.Partition.Parts[1].Text != "Child" || d.IfExists != strings.HasPrefix(target, "IF EXISTS") || d.Only != strings.HasPrefix(target, "ONLY") {
			t.Fatal(statements[0])
		}
		if d.DetachPos.Character != utf8.RuneCountInString(q[:strings.Index(q, "DETACH")])+1 || d.Pos.Character != 7 {
			t.Fatal("positioned detach AST", d)
		}
	}
}

func TestPartitionDetachExcludedSyntax(t *testing.T) {
	for _, suffix := range []string{", ADD COLUMN k INT", " garbage"} {
		q := "ALTER TABLE parent DETACH PARTITION child" + suffix
		statements, err := Parse(q)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || len(statements) != 0 {
			t.Fatal("whole-input parse admitted ordinary prefix", q, statements, err)
		}
	}
}

func TestPartitionDetachConcurrentAST(t *testing.T) {
	for _, tc := range []struct {
		suffix string
		mode   PartitionDetachMode
	}{{"CONCURRENTLY", DetachConcurrent}, {"FINALIZE", DetachFinalize}} {
		q := `/*é*/ ALTER TABLE ONLY "schema"."Parent" DETACH PARTITION "schema"."Child" ` + tc.suffix
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		d, ok := statements[0].(*AlterTableDetachPartition)
		if !ok || d.Mode != tc.mode || !d.Only || d.Name.Parts[1].Text != "Parent" || d.Partition.Parts[1].Text != "Child" ||
			d.ModePos.Character != utf8.RuneCountInString(q[:strings.LastIndex(q, tc.suffix)])+1 {
			t.Fatal("mode/source ownership", statements[0])
		}
	}
}

func TestPartitionDetachConcurrentSyntax(t *testing.T) {
	for _, suffix := range []string{"CONCURRENTLY CONCURRENTLY", "CONCURRENTLY FINALIZE", "FINALIZE CONCURRENTLY", "FINALIZE FINALIZE", "CONCURRENTLY, ADD COLUMN k INT", "FINALIZE garbage"} {
		statements, err := Parse("ALTER TABLE parent DETACH PARTITION child " + suffix)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || len(statements) != 0 {
			t.Fatal("executed valid prefix", suffix, statements, err)
		}
	}
}
