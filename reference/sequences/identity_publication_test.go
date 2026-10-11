package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIdentityPublicationFailure(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec(`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(MAXVALUE 2 CACHE 1),p INT NOT NULL)`, 0))
	p.Step(t, fixture.ImplicitError(`INSERT INTO i VALUES(DEFAULT,1),(DEFAULT,NULL)`, fixture.Diagnostic{Code: "23502", Message: `null value in column "p" of relation "i" violates not-null constraint`, Detail: "Failing row contains (2, null).", Schema: "public", Table: "i", Column: "p"}))
	p.Step(t, fixture.Select(`SELECT * FROM i`, []string{"id", "p"}, []string{"INT4", "INT4"}, nil))
	p.Step(t, identitySequenceState("i_id_seq", 2, true))
	p.Step(t, fixture.ImplicitError(`INSERT INTO i(p) VALUES(3)`, fixture.Diagnostic{Code: "2200H", Message: `nextval: reached maximum value of sequence "i_id_seq" (2)`}))
	p.Step(t, fixture.Select(`SELECT * FROM i`, []string{"id", "p"}, []string{"INT4", "INT4"}, nil))
}
func TestReferenceIdentityEarlyRowFailureStopsDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, tc := range []struct{ name, options, later string }{
				{"exhaustion", "MINVALUE 0 MAXVALUE 1 START 1 CACHE 1", "$2"},
				{"later_identity", "MINVALUE 0 MAXVALUE 10 START 1 CACHE 1", "$2"},
				{"later_side_producer", "MINVALUE 0 MAXVALUE 10 START 1 CACHE 1", "nextval('side')"},
			} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/prepared=%t", tc.name, prepared), func(t *testing.T) {
						c := fixture.IdentityConnection(t, p.Ctx, b.DB)
						var executor fixture.Executor = c
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, b.Recorder, s) }
						run(fixture.Exec(`CREATE TABLE early(id INT GENERATED ALWAYS AS IDENTITY(`+tc.options+`),p INT NOT NULL)`, 0))
						run(fixture.Exec(`CREATE SEQUENCE side START 50 CACHE 1`, 0))
						defer func() {
							for _, q := range []string{`DROP TABLE early`, `DROP SEQUENCE side`} {
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
							}
						}()
						q := `INSERT INTO early VALUES(DEFAULT,$1),(DEFAULT,` + tc.later + `)`
						if prepared {
							executor = fixture.IdentityHeld(t, p.Ctx, c, q)
						}
						run(identitySequenceState("early_id_seq", 1, false))
						args := []any{nil, 8}
						if tc.name == "later_side_producer" {
							args = args[:1]
						}
						result, err := executor.ExecContext(p.Ctx, q, args...)
						if result != nil {
							t.Fatal("failed batch returned result")
						}
						want := fixture.ControlsReferenceDiagnostic("23502", `null value in column "p" of relation "early" violates not-null constraint`)
						want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (1, null).", "public", "early", "p"
						fixture.AssertDiagnostic(t, err, &want)
						run(fixture.Select(`SELECT * FROM early`, []string{"id", "p"}, []string{"INT4", "INT4"}, nil))
						run(identitySequenceState("early_id_seq", 1, true))
						run(identitySequenceState("side", 50, false))
						run(identityScalar(`SELECT currval('early_id_seq')`, "currval", 1))
						run(identityScalar(`SELECT lastval()`, "lastval", 1))
						run(fixture.ImplicitError(`SELECT currval('side')`, fixture.Diagnostic{Code: "55000", Message: `currval of sequence "side" is not yet defined in this session`}))
						run(identityScalar(`SELECT lastval()`, "lastval", 1))
						firstID := int64(2)
						if tc.name == "exhaustion" {
							run(fixture.Exec(`ALTER TABLE early ALTER id RESTART WITH 0`, 0))
							firstID = 0
						}
						args[0] = 7
						result, err = executor.ExecContext(p.Ctx, q, args...)
						identityResult(t, result, err, 2)
						payload := int64(8)
						if tc.name == "later_side_producer" {
							payload = 50
						}
						run(fixture.Select(`SELECT * FROM early ORDER BY id`, []string{"id", "p"}, []string{"INT4", "INT4"}, [][]any{{firstID, int64(7)}, {firstID + 1, payload}}))
						run(identitySequenceState("early_id_seq", firstID+1, true))
						run(identityScalar(`SELECT currval('early_id_seq')`, "currval", firstID+1))
						run(identityScalar(`SELECT lastval()`, "lastval", firstID+1))
					})
				}
			}
		})
	}
}
func TestReferenceIdentitySessionsCacheAndNoDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, c := fixture.IdentityConnection(t, p.Ctx, b.DB), fixture.IdentityConnection(t, p.Ctx, b.DB)
			run := func(executor fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, b.Recorder, s) }
			run(a, fixture.Exec(`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(CACHE 2))`, 0))
			held := fixture.IdentityHeld(t, p.Ctx, a, `INSERT INTO i DEFAULT VALUES`)
			run(a, identitySequenceState("i_id_seq", 1, false))
			run(a, fixture.Exec(`INSERT INTO i OVERRIDING SYSTEM VALUE VALUES(99)`, 1))
			run(a, fixture.Exec(`INSERT INTO i OVERRIDING SYSTEM VALUE SELECT id FROM i WHERE false`, 0))
			run(a, identitySequenceState("i_id_seq", 1, false))
			run(held, fixture.Exec(`INSERT INTO i DEFAULT VALUES`, 1))
			run(a, identityScalar(`SELECT currval('i_id_seq')`, "currval", 1))
			run(c, fixture.Exec(`INSERT INTO i DEFAULT VALUES`, 1))
			run(c, identityScalar(`SELECT currval('i_id_seq')`, "currval", 3))
			run(held, fixture.Exec(`INSERT INTO i DEFAULT VALUES`, 1))
			run(a, identityScalar(`SELECT lastval()`, "lastval", 2))
			run(a, identitySequenceState("i_id_seq", 4, true))
			run(a, fixture.Exec(`BEGIN READ ONLY`, 0))
			defer a.ExecContext(p.Ctx, `ROLLBACK`)
			run(a, fixture.ImplicitError(`INSERT INTO i DEFAULT VALUES`, fixture.Diagnostic{Code: "25006", Message: "cannot execute INSERT in a read-only transaction"}))
			run(a, fixture.Exec(`ROLLBACK`, 0))
			run(a, identitySequenceState("i_id_seq", 4, true))
		})
	}
}
func identityRefConnExec(t *testing.T, ctx context.Context, c *sql.Conn, q string, count int64) {
	t.Helper()
	r, err := c.ExecContext(ctx, q)
	identityResult(t, r, err, count)
}

func identityRefConnValue(t *testing.T, ctx context.Context, c *sql.Conn, q string, want int64) {
	t.Helper()
	var got any
	if err := c.QueryRowContext(ctx, q).Scan(&got); err != nil || got != want {
		t.Fatal(q, got, want, err)
	}
}
