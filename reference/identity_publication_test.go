package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceIdentityPublicationFailure(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec(`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(MAXVALUE 2 CACHE 1),p INT NOT NULL)`, 0))
	p.step(t, referenceImplicitError(`INSERT INTO i VALUES(DEFAULT,1),(DEFAULT,NULL)`, referenceDiagnostic{Code: "23502", Message: `null value in column "p" of relation "i" violates not-null constraint`, Detail: "Failing row contains (2, null).", Schema: "public", Table: "i", Column: "p"}))
	p.step(t, referenceSelect(`SELECT * FROM i`, []string{"id", "p"}, []string{"INT4", "INT4"}, nil))
	p.step(t, identitySequenceState("i_id_seq", 2, true))
	p.step(t, referenceImplicitError(`INSERT INTO i(p) VALUES(3)`, referenceDiagnostic{Code: "2200H", Message: `nextval: reached maximum value of sequence "i_id_seq" (2)`}))
	p.step(t, referenceSelect(`SELECT * FROM i`, []string{"id", "p"}, []string{"INT4", "INT4"}, nil))
}
func TestReferenceIdentityEarlyRowFailureStopsDemand(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			for _, tc := range []struct{ name, options, later string }{
				{"exhaustion", "MINVALUE 0 MAXVALUE 1 START 1 CACHE 1", "$2"},
				{"later_identity", "MINVALUE 0 MAXVALUE 10 START 1 CACHE 1", "$2"},
				{"later_side_producer", "MINVALUE 0 MAXVALUE 10 START 1 CACHE 1", "nextval('side')"},
			} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/prepared=%t", tc.name, prepared), func(t *testing.T) {
						c := identityConnection(t, p.ctx, b.db)
						var executor referenceExecutor = c
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, executor, b.recorder, s) }
						run(referenceExec(`CREATE TABLE early(id INT GENERATED ALWAYS AS IDENTITY(`+tc.options+`),p INT NOT NULL)`, 0))
						run(referenceExec(`CREATE SEQUENCE side START 50 CACHE 1`, 0))
						defer func() {
							for _, q := range []string{`DROP TABLE early`, `DROP SEQUENCE side`} {
								referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(q, 0))
							}
						}()
						q := `INSERT INTO early VALUES(DEFAULT,$1),(DEFAULT,` + tc.later + `)`
						if prepared {
							executor = identityHeld(t, p.ctx, c, q)
						}
						run(identitySequenceState("early_id_seq", 1, false))
						args := []any{nil, 8}
						if tc.name == "later_side_producer" {
							args = args[:1]
						}
						result, err := executor.ExecContext(p.ctx, q, args...)
						if result != nil {
							t.Fatal("failed batch returned result")
						}
						want := controlsReferenceDiagnostic("23502", `null value in column "p" of relation "early" violates not-null constraint`)
						want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (1, null).", "public", "early", "p"
						referenceAssertDiagnostic(t, err, &want)
						run(referenceSelect(`SELECT * FROM early`, []string{"id", "p"}, []string{"INT4", "INT4"}, nil))
						run(identitySequenceState("early_id_seq", 1, true))
						run(identitySequenceState("side", 50, false))
						run(identityScalar(`SELECT currval('early_id_seq')`, "currval", 1))
						run(identityScalar(`SELECT lastval()`, "lastval", 1))
						run(referenceImplicitError(`SELECT currval('side')`, referenceDiagnostic{Code: "55000", Message: `currval of sequence "side" is not yet defined in this session`}))
						run(identityScalar(`SELECT lastval()`, "lastval", 1))
						firstID := int64(2)
						if tc.name == "exhaustion" {
							run(referenceExec(`ALTER TABLE early ALTER id RESTART WITH 0`, 0))
							firstID = 0
						}
						args[0] = 7
						result, err = executor.ExecContext(p.ctx, q, args...)
						identityResult(t, result, err, 2)
						payload := int64(8)
						if tc.name == "later_side_producer" {
							payload = 50
						}
						run(referenceSelect(`SELECT * FROM early ORDER BY id`, []string{"id", "p"}, []string{"INT4", "INT4"}, [][]any{{firstID, int64(7)}, {firstID + 1, payload}}))
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
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, c := identityConnection(t, p.ctx, b.db), identityConnection(t, p.ctx, b.db)
			run := func(executor referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, executor, b.recorder, s) }
			run(a, referenceExec(`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(CACHE 2))`, 0))
			held := identityHeld(t, p.ctx, a, `INSERT INTO i DEFAULT VALUES`)
			run(a, identitySequenceState("i_id_seq", 1, false))
			run(a, referenceExec(`INSERT INTO i OVERRIDING SYSTEM VALUE VALUES(99)`, 1))
			run(a, referenceExec(`INSERT INTO i OVERRIDING SYSTEM VALUE SELECT id FROM i WHERE false`, 0))
			run(a, identitySequenceState("i_id_seq", 1, false))
			run(held, referenceExec(`INSERT INTO i DEFAULT VALUES`, 1))
			run(a, identityScalar(`SELECT currval('i_id_seq')`, "currval", 1))
			run(c, referenceExec(`INSERT INTO i DEFAULT VALUES`, 1))
			run(c, identityScalar(`SELECT currval('i_id_seq')`, "currval", 3))
			run(held, referenceExec(`INSERT INTO i DEFAULT VALUES`, 1))
			run(a, identityScalar(`SELECT lastval()`, "lastval", 2))
			run(a, identitySequenceState("i_id_seq", 4, true))
			run(a, referenceExec(`BEGIN READ ONLY`, 0))
			defer a.ExecContext(p.ctx, `ROLLBACK`)
			run(a, referenceImplicitError(`INSERT INTO i DEFAULT VALUES`, referenceDiagnostic{Code: "25006", Message: "cannot execute INSERT in a read-only transaction"}))
			run(a, referenceExec(`ROLLBACK`, 0))
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
