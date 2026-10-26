package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateAtomicityIndexes(t *testing.T) {
	for _, kind := range []string{"UNIQUE", "EXCLUDE USING btree(id WITH =)"} {
		t.Run(kind, func(t *testing.T) {
			truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
				definition := "CONSTRAINT retained UNIQUE(id)"
				d := truncateWant("23505", `duplicate key value violates unique constraint "retained"`)
				d.Detail = "Key (id)=(1) already exists."
				d.Schema = "public"
				d.Table = "indexed"
				d.Constraint = "retained"
				if kind != "UNIQUE" {
					definition = "CONSTRAINT retained " + kind
					d = fixture.ExclusionReferenceConflict("indexed", "retained", "id", "1", "1")
				}
				truncateExec(t, f, f.A, "CREATE TABLE indexed(id INT,"+definition+")", 0)
				truncateExec(t, f, f.A, "INSERT INTO indexed VALUES(1),(2)", 2)
				var before, after int64
				probe := "SELECT oid::bigint FROM pg_constraint WHERE conname='retained'"
				if err := f.A.QueryRowContext(p.Ctx, probe).Scan(&before); err != nil || before <= 0 {
					t.Fatalf("definition identity %d: %v", before, err)
				}
				tx := truncateBegin(t, p, f.A, nil)
				truncateExec(t, f, tx, "TRUNCATE indexed", 0)
				truncateExec(t, f, tx, "INSERT INTO indexed VALUES(1)", 1)
				truncateExec(t, f, tx, "SAVEPOINT new_generation", 0)
				truncateError(t, f, tx, "INSERT INTO indexed VALUES(1)", d)
				truncateExec(t, f, tx, "ROLLBACK TO new_generation", 0)
				truncateRows(t, f, tx, "indexed", 1)
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if err := f.A.QueryRowContext(p.Ctx, probe).Scan(&after); err != nil || after != before {
					t.Fatalf("definition reminted %d -> %d: %v", before, after, err)
				}
				truncateError(t, f, f.A, "INSERT INTO indexed VALUES(1)", d)
				truncateExec(t, f, f.A, "TRUNCATE indexed", 0)
				truncateExec(t, f, f.A, "INSERT INTO indexed VALUES(1)", 1)
				truncateRows(t, f, f.A, "indexed", 1)
			})
		})
	}
}
func TestReferenceTruncateLocksSavepointCancellation(t *testing.T) {
	truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		truncateSeed(t, f)
		holder := truncateBegin(t, p, f.A, nil)
		truncateExec(t, f, holder, "LOCK q IN ACCESS SHARE MODE", 0)
		tx := truncateBegin(t, p, f.B, nil)
		truncateExec(t, f, tx, "INSERT INTO p VALUES(3)", 1)
		truncateExec(t, f, tx, "SAVEPOINT preserve", 0)
		truncateExec(t, f, tx, "SET LOCAL lock_timeout='10ms'", 0)
		truncateError(t, f, tx, "TRUNCATE p,q", truncateWant("55P03", "canceling statement due to lock timeout"))
		truncateExec(t, f, tx, "ROLLBACK TO preserve", 0)
		truncateRows(t, f, tx, "p", 1, 2, 3)
		truncateExec(t, f, tx, "TRUNCATE p", 0)
		truncateRows(t, f, tx, "p")
		truncateExec(t, f, tx, "ROLLBACK TO preserve", 0)
		truncateRows(t, f, tx, "p", 1, 2, 3)
		holder.Rollback()
		tx.Rollback()
		truncateRows(t, f, f.A, "p", 1, 2)
		truncateRows(t, f, f.A, "q", 7)
	})
}
