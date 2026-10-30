package core_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func explainJSONPreparedOwnership(t *testing.T, e *core.Engine, s *session.Session) {
	t.Helper()
	ast, description := sequencePreparedDescription(t, e, s, "EXPLAIN (FORMAT JSON,VERBOSE,COSTS false) SELECT id FROM explain_boundary_foreign")
	defer e.RetirePreparedDescription(&description)
	owner := description.Folds
	if owner == nil || owner.Live() || owner.Current().Generation != 0 || description.FoldPlanning != nil {
		t.Fatal("JSON Describe admitted folds")
	}
	result, err := sequencePreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Rows.Close()
	if len(result.Rows.Schema()) != 1 || result.Rows.Schema()[0].Usage.Type.ID() != sqltype.JSON || result.Count != 0 {
		t.Fatal("wrong typed wrapper output")
	}
	backing := result.Rows.(*exec.FinalRows).Anonymous[0]
	cells := make([]sqltype.Value, 1)
	present, err := result.Rows.Next(context.Background(), cells)
	if err != nil || !present || cells[0].Type().ID() != sqltype.JSON || cells[0].IsNull() {
		t.Fatal("not one typed JSON datum", cells, err)
	}
	retained := cells[0]
	before, err := retained.Type().Output(context.Background(), retained)
	if err != nil {
		t.Fatal(err)
	}
	if more, err := result.Rows.Next(context.Background(), cells); more || err != nil {
		t.Fatal("wrong row cardinality", more, err)
	}
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"ALTER TABLE explain_boundary_foreign RENAME TO explain_json_retired",
		"CREATE TABLE explain_boundary_foreign(id integer)",
		"DROP TABLE explain_json_retired",
	} {
		enginetest.SessionSQL(t, s, query)
	}
	current, err := sequencePreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Rows.Close()
	if present, err := current.Rows.Next(context.Background(), cells); err != nil || !present || cells[0].Type().ID() != sqltype.JSON {
		t.Fatal("current dependency did not rebind", err)
	}
	currentText, err := cells[0].Type().Output(context.Background(), cells[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(currentText, "explain_boundary_foreign") || strings.Contains(currentText, "explain_json_retired") {
		t.Fatal(currentText)
	}
	after, err := retained.Type().Output(context.Background(), retained)
	if err != nil || after != before || retained.Type().ID() != sqltype.JSON {
		t.Fatal("retained immutable JSON changed across close/rebind", err)
	}
	// The original published row backing and immutable typed payload survive
	// rebind unchanged; the next activation publishes separate row storage.
	currentBacking := current.Rows.(*exec.FinalRows).Anonymous[0]
	if &backing[0] == &currentBacking[0] || backing[0] != retained {
		t.Fatal("JSON activation reused/mutated a prior published row")
	}
	if description.Folds != owner || owner.Live() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 || description.FoldPlanning != nil {
		t.Fatal("wrapper published/replaced executable ownership")
	}
}
