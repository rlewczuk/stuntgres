package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAutoExplainEffects(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SEQUENCE ae_once", 0))
		run(fixture.Exec("CREATE TABLE ae_effects(id integer PRIMARY KEY, token bigint DEFAULT nextval('ae_once'), v text)", 0))
		run(fixture.Exec("INSERT INTO ae_effects(id,v) VALUES (1,'one'),(2,'two')", 2))
		autoExplainConfigure(t, p, b, c, notices)

		// A log-only replay would consume another sequence value even if the
		// duplicate key were somehow hidden. Check both effects and draw count.
		const insert = "INSERT INTO ae_effects(id,v) VALUES (3,'three')"
		run(fixture.Exec(insert, 1))
		document := autoExplainJSON(t, notices, insert, nil, true, 0)
		if !b.PG {
			autoExplainDMLNode(t, document["Plan"].(map[string]any), "Values", 1, 1)
		}

		const update = "UPDATE ae_effects SET v='changed' WHERE id=1 RETURNING id,v"
		run(fixture.Prepared(fixture.Select(update, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "changed"}})))
		autoExplainJSON(t, notices, update, nil, true, 1)

		const remove = "DELETE FROM ae_effects WHERE id=2"
		run(fixture.Prepared(fixture.Exec(remove, 1)))
		autoExplainJSON(t, notices, remove, nil, true, 0)

		const contents = "SELECT id,token,v FROM ae_effects ORDER BY id"
		want := [][]any{{int64(1), int64(1), "changed"}, {int64(3), int64(3), "three"}}
		run(fixture.Select(contents, []string{"id", "token", "v"}, []string{"INT4", "INT8", "TEXT"}, want))
		autoExplainJSON(t, notices, contents, nil, true, 2)
		const sequence = "SELECT last_value,is_called FROM ae_once"
		run(fixture.Select(sequence, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(3), true}}))
		autoExplainJSON(t, notices, sequence, nil, true, 1)

		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		autoExplainQuiet(t, notices)
		const rolledBack = "INSERT INTO ae_effects(id,v) VALUES (4,'rolled back')"
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(rolledBack, 1))
		autoExplainJSON(t, notices, rolledBack, nil, true, 0)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		autoExplainQuiet(t, notices)
		run(fixture.Select(contents, []string{"id", "token", "v"}, []string{"INT4", "INT8", "TEXT"}, want))
		autoExplainJSON(t, notices, contents, nil, true, 2)
		run(fixture.Select(sequence, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(4), true}}))
		autoExplainJSON(t, notices, sequence, nil, true, 1)

		// The divisor depends on each real row, so this is executor failure,
		// not constant folding. Failed execution must retain its own diagnostic
		// and cannot manufacture a successful completed plan or changed data.
		const failing = "UPDATE ae_effects SET v=(10/(id-id))::text"
		s := fixture.ErrorDiagnostic(failing, fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
		s.ErrorStage = "execute"
		run(s)
		autoExplainQuiet(t, notices)
		run(fixture.Select(contents, []string{"id", "token", "v"}, []string{"INT4", "INT8", "TEXT"}, want))
		autoExplainJSON(t, notices, contents, nil, true, 2)
	})
	autoExplainMutationCases(t)
}
