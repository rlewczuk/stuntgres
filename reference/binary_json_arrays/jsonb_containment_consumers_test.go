package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbContainmentStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec(`CREATE TABLE jbc_stored(id int,doc jsonb,g bool GENERATED ALWAYS AS (doc @> '{"a":1}'::jsonb) STORED,CONSTRAINT jbc_ok CHECK(doc <@ '{"a":1,"b":2}'::jsonb))`, 0))
	run(fixture.Exec(`CREATE VIEW jbc_view AS SELECT id,doc @> '{"a":1}'::jsonb AS contains,'{"a":1}'::jsonb <@ doc AS reverse FROM jbc_stored`, 0))
	run(fixture.Exec(`INSERT INTO jbc_stored(id,doc) VALUES(1,'{"a":1}'),(2,'{}'),(3,NULL)`, 3))
	check := fixture.Select("SELECT id,contains,reverse FROM jbc_view ORDER BY id", []string{"id", "contains", "reverse"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}, {int64(2), false, false}, {int64(3), nil, nil}})
	jsonbExistsBoth(t, p, b, c, check)
	jsonbExistsBoth(t, p, b, c, fixture.Select("SELECT id,g FROM jbc_stored ORDER BY id", []string{"id", "g"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}}))
	return check
}

func TestReferenceJSONBContainmentConsumersControls(t *testing.T) {
	jsonbContainmentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonbContainmentStored(t, p, b, c)
		d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "jbc_stored" violates check constraint "jbc_ok"`)
		d.Detail = "Failing row contains (5, {\"a\": 2}, f)."
		d.Schema, d.Table, d.Constraint = "public", "jbc_stored", "jbc_ok"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(`INSERT INTO jbc_stored(id,doc) VALUES(4,'{}'),(5,'{"a":2}')`, d)
			s.ErrorStage = "execute"
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,g FROM jbc_stored WHERE id>3 ORDER BY id", []string{"id", "g"}, []string{"INT4", "BOOL"}, nil))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jbc_stored(id,doc) VALUES(4,'{"b":2}')`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,g FROM jbc_stored WHERE id=4", []string{"id", "g"}, []string{"INT4", "BOOL"}, [][]any{{int64(4), false}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM jbc_stored WHERE id=4", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		jsonbContainmentControls(t, p, b, c, check)
	})
}
