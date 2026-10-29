package tablecmds_test

import (
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/table"
)

func TestInheritanceComparisonCapturedDependency(t *testing.T) {
	e := engine.New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE EXTENSION cube",
		"CREATE TABLE p(v cube,CONSTRAINT inherited_cube_check CHECK(NULLIF(v,'1'::cube) IS NULL OR v='2'::cube))",
		"ALTER FUNCTION cube_eq(cube,cube) RENAME TO equality_now",
	} {
		roleExecute(t, e, op, query)
	}
	parent := retiredUnitTable(t, e, "p")
	var captured catalog.ExpressionDependency
	for _, dependency := range parent.Constraints.Check[0].Dependencies {
		procedure, ok := e.CatalogRead().Procedure(dependency.Ref.Address)
		if ok && procedure.Name() == "equality_now" {
			captured = dependency
			break
		}
	}
	current, ok := e.CatalogRead().Procedure(captured.Ref.Address)
	if !ok || current.Name() != "equality_now" || current.Revision() == captured.Revision {
		t.Fatal("fixture did not retain a comparison capture across routine identity publication", captured, current)
	}
	roleExecute(t, e, op, "CREATE TABLE c() INHERITS(p)")
	child := retiredUnitTable(t, e, "c")
	found := false
	for _, dependency := range child.Constraints.Check[0].Dependencies {
		if dependency.Ref == captured.Ref {
			if dependency != captured {
				t.Fatal("inherited CHECK recaptured current routine instead of preserving its owning manifest", dependency, captured)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("inherited CHECK lost selected comparison dependency")
	}
	if err := table.ValidateChecks(e.CatalogRead(), child, map[string]bool{}); err != nil {
		t.Fatal("inherited comparison manifest rejected", err)
	}
	roleExecute(t, e, op, "INSERT INTO c VALUES('1'),('2'),(NULL)")
	if _, err := e.Execute(op, enginetest.Statement(t, "INSERT INTO c VALUES('3')"), 0, nil); err == nil || !strings.Contains(err.Error(), "violates check constraint") {
		t.Fatal("inherited comparison CHECK did not reject invalid value", err)
	}
	for _, corruption := range []string{"missing", "recaptured"} {
		t.Run(corruption, func(t *testing.T) {
			forged := *child
			forged.Constraints.Check = append([]catalog.CheckConstraint(nil), child.Constraints.Check...)
			check := &forged.Constraints.Check[0]
			check.Dependencies = nil
			for _, dependency := range child.Constraints.Check[0].Dependencies {
				if dependency.Ref == captured.Ref {
					if corruption == "missing" {
						continue
					}
					dependency.Revision = current.Revision()
				}
				check.Dependencies = append(check.Dependencies, dependency)
			}
			if err := table.ValidateChecks(e.CatalogRead(), &forged, map[string]bool{}); err == nil {
				t.Fatal("forged inherited comparison hard manifest accepted")
			}
		})
	}
}
