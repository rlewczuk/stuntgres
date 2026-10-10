package parser

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestExclusionSyntax(t *testing.T) {
	query := `CREATE TABLE t(v INT,CONSTRAINT "ex" EXCLUDE USING btree((v+0) int4_ops DESC NULLS LAST WITH OPERATOR(pg_catalog.=)) INCLUDE(v) WITH(fillfactor=70) WHERE(v>0) DEFERRABLE INITIALLY DEFERRED)`
	statements, err := Parse(query)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	table, ok := statements[0].(*CreateTable)
	if !ok || len(table.Elements) != 2 {
		t.Fatal(statements)
	}
	constraint := table.Elements[1].Exclusion
	if constraint == nil || constraint.Name.Text != "ex" || constraint.Index.Method != "btree" || !constraint.Deferrable || !constraint.InitiallyDeferred || !constraint.DeferrabilitySet || !constraint.InitiallySet {
		t.Fatal("lost exclusion definition properties", constraint)
	}
	if len(constraint.Index.Keys) != 1 || len(constraint.Operators) != 1 || len(constraint.Index.Include) != 1 || constraint.Index.Predicate == nil || len(constraint.Index.Options) != 1 {
		t.Fatal("lost index clauses", constraint)
	}
	key := constraint.Index.Keys[0]
	if key.Expression == nil || key.Opclass == nil || !key.Desc || !key.DirectionExplicit {
		t.Fatal("lost key definition", key)
	}
	operator := constraint.Operators[0]
	if len(operator.Parts) != 2 || operator.Parts[0].Text != "pg_catalog" || operator.Parts[1].Text != "=" || operator.Parts[1].Pos.Character == 0 {
		t.Fatal("lost original operator identity", operator)
	}
	statements, err = Parse("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE USING hash(v WITH =) INITIALLY DEFERRED")
	if err != nil {
		t.Fatal(err)
	}
	alter, ok := statements[0].(*AlterTable)
	if !ok || len(alter.Actions) != 1 || alter.Actions[0].Action != AddExclusion || alter.Actions[0].Exclusion == nil || !alter.Actions[0].Exclusion.Deferrable {
		t.Fatal("missing ALTER exclusion action", statements)
	}
	statements, err = Parse(`ALTER TABLE IF EXISTS t RENAME CONSTRAINT "ex" TO renamed`)
	if err != nil {
		t.Fatal(err)
	}
	rename, ok := statements[0].(*AlterTableRenameConstraint)
	if !ok || !rename.IfExists || rename.Constraint.Text != "ex" || rename.NewName.Text != "renamed" || rename.ConstraintPos.Character == 0 {
		t.Fatal("missing feature-local rename syntax", statements)
	}
}

func TestExclusionSetConstraintsSyntax(t *testing.T) {
	for _, query := range []string{"SET CONSTRAINTS ALL DEFERRED", "SET CONSTRAINTS ALL IMMEDIATE", "SET CONSTRAINTS ex, public.other DEFERRED", `SET CONSTRAINTS "ALL" IMMEDIATE`} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		set, ok := statements[0].(*SetConstraints)
		if !ok || set.Pos.Character != 1 || !set.All && len(set.Names) == 0 {
			t.Fatal(query, statements)
		}
	}
	for _, query := range []string{"SELECT exclude FROM exclude", "SET exclude='on'"} {
		if _, err := Parse(query); err != nil {
			t.Fatal("EXCLUDE token displaced its existing keyword use", query, err)
		}
	}
	for _, unit := range []string{"ROWS", "RANGE"} {
		for _, exclusion := range []string{"NO OTHERS", "CURRENT ROW", "GROUP", "TIES"} {
			query := "SELECT sum(v) OVER(" + unit + " CURRENT ROW EXCLUDE " + exclusion + ") FROM t"
			statements, err := Parse(query)
			var diagnostic *sqlstate.Error
			if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "explicit window frames are unsupported" || diagnostic.Position != "20" {
				t.Fatal("EXCLUDE changed the owning window-frame capability boundary", query, statements, err)
			}
		}
	}
}

func TestExclusionPropertySyntaxDiagnostics(t *testing.T) {
	for _, suffix := range []string{"DEFERRABLE NOT DEFERRABLE", "INITIALLY DEFERRED INITIALLY IMMEDIATE", "NOT DEFERRABLE INITIALLY DEFERRED", "INITIALLY DEFERRED NOT DEFERRABLE"} {
		statements, err := Parse("CREATE TABLE prefix(v INT); CREATE TABLE t(v INT,EXCLUDE(v WITH =) " + suffix + ")")
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position == "" {
			t.Fatal("constraint properties must fail parsing atomically", suffix, statements, err)
		}
	}
}
