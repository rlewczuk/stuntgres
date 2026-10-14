package parser

import (
	"errors"
	"strconv"
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
	for _, suffix := range []string{"CONCURRENTLY", "FINALIZE"} {
		q := "ALTER TABLE parent DETACH PARTITION child " + suffix
		_, err := Parse(q)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "DETACH PARTITION "+suffix+" is unsupported" || diagnostic.Position != strconv.Itoa(strings.Index(q, suffix)+1) {
			t.Fatal(q, err)
		}
	}
	for _, suffix := range []string{", ADD COLUMN k INT", " garbage"} {
		q := "ALTER TABLE parent DETACH PARTITION child" + suffix
		statements, err := Parse(q)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || len(statements) != 0 {
			t.Fatal("whole-input parse admitted ordinary prefix", q, statements, err)
		}
	}
}
