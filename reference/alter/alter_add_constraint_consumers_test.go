package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func addConstraintConsumers(t *testing.T) {
	t.Run("invalid_fk_all_admitted_producers", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
			run(fixture.Exec("CREATE TABLE c(id INT PRIMARY KEY,pid INT DEFAULT 9,note TEXT)", 0))
			run(fixture.Exec("INSERT INTO c VALUES(1,1,'valid'),(2,9,'old')", 2))
			run(fixture.Exec("ALTER TABLE c ADD CONSTRAINT fk FOREIGN KEY(pid) REFERENCES p NOT VALID", 0))
			bad := addConstraintFK("c", "fk", "pid", "9", "p")
			for _, q := range []string{
				"INSERT INTO c(id) VALUES(3)",
				"INSERT INTO c SELECT 3,9,'bad' FROM p WHERE id=1",
				"INSERT INTO c VALUES(3,9,'bad') ON CONFLICT DO NOTHING",
				"INSERT INTO c VALUES(1,9,'bad') ON CONFLICT(id) DO UPDATE SET pid=excluded.pid",
				"MERGE INTO c USING (SELECT 1 AS id,9 AS pid) s ON c.id=s.id WHEN MATCHED THEN UPDATE SET pid=s.pid",
				"MERGE INTO c USING (SELECT 3 AS id,9 AS pid) s ON FALSE WHEN NOT MATCHED THEN INSERT(id,pid) VALUES(s.id,s.pid)",
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, bad)
					s.Prepared = prepared
					run(s)
				}
			}
			run(fixture.Exec("INSERT INTO c VALUES(1,9,'skipped') ON CONFLICT DO NOTHING", 0))
			run(fixture.Exec("INSERT INTO c VALUES(2,9,'same') ON CONFLICT(id) DO UPDATE SET note=excluded.note", 1))
			for _, q := range []string{"UPDATE c SET pid=9 WHERE id=1 RETURNING pid", "UPDATE c SET pid=9 FROM p WHERE c.id=p.id AND c.id=1 RETURNING c.pid"} {
				s := fixture.Prepared(fixture.ErrorDiagnostic(q, bad))
				s.SelectRows = true
				run(s)
			}
			parent := fixture.ControlsReferenceDiagnostic("23503", `update or delete on table "p" violates foreign key constraint "fk" on table "c"`)
			parent.Detail, parent.Schema, parent.Table, parent.Constraint = `Key (id)=(1) is still referenced from table "c".`, "public", "c", "fk"
			for _, q := range []string{"UPDATE p SET id=3 WHERE id=1 RETURNING id", "DELETE FROM p USING c WHERE p.id=c.pid RETURNING p.id"} {
				s := fixture.Prepared(fixture.ErrorDiagnostic(q, parent))
				s.SelectRows = true
				run(s)
			}
			run(fixture.Select("SELECT id,pid,note FROM c ORDER BY id", []string{"id", "pid", "note"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "valid"}, {int64(2), int64(9), "same"}}))
			run(fixture.Select("UPDATE c SET pid=2 WHERE id=2 RETURNING id,pid", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(2)}}))
		})
	})
	t.Run("invalid_local_completed_rows_and_multirow_atomicity", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT,n INT,note TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(0,NULL,'old')", 1))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT VALID,ADD CONSTRAINT nn NOT NULL n NOT VALID", 0))
			nn := fixture.PrimaryKeyReferenceNull("t", "n", "0, null, changed")
			run(fixture.ErrorDiagnostic("UPDATE t SET note='changed'", nn))
			d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "t" violates check constraint "ck"`)
			d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (0, 1, old).", "public", "t", "ck"
			run(fixture.Prepared(fixture.ErrorDiagnostic("UPDATE t SET n=1", d)))
			run(fixture.Exec("UPDATE t SET a=1,n=1", 1))
			d.Detail = "Failing row contains (0, 2, bad)."
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(2,2,'valid'),(0,2,'bad')", d))
			run(fixture.Select("SELECT a,n,note FROM t", []string{"a", "n", "note"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "old"}}))
		})
	})
}

func addConstraintTransactionLifecycle(t *testing.T) {
	t.Run("private_catalog_peer_commit_rollback_and_readonly", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("INSERT INTO t VALUES(1,'one')", 1))
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("ALTER TABLE t ADD CONSTRAINT private_ck CHECK(a>0) NOT VALID", 0))
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, [][]any{{"private_ck"}}))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("BEGIN READ ONLY", 0))
			held := addConstraintHeld(t, p, b, c, "ALTER TABLE t ADD CONSTRAINT readonly_ck CHECK(a>0)")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, fixture.ControlsReferenceDiagnostic("25006", "cannot execute ALTER TABLE in a read-only transaction")))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(held.SQL, 0))
		})
	})
	t.Run("rename_drop_supporting_dependencies_and_held_writes", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("INSERT INTO t VALUES(9,'old')", 1))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID", 0))
			oid := addConstraintInvalid(t, p, b, c, "t", "fk", "FOREIGN KEY (a) REFERENCES p(id) NOT VALID")
			held := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1,$2)")
			run(fixture.Exec("ALTER TABLE t RENAME COLUMN a TO renamed", 0))
			if got := addConstraintInvalid(t, p, b, c, "t", "fk", "FOREIGN KEY (renamed) REFERENCES p(id) NOT VALID"); got != oid {
				t.Fatal("rename reminted unaffected FK", oid, got)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, addConstraintFK("t", "fk", "renamed", "9", "p"), 9, "bad"))
			_, err := c.ExecContext(p.Ctx, "ALTER TABLE p DROP CONSTRAINT p_pkey")
			d := fixture.ControlsReferenceDiagnostic("2BP01", `cannot drop constraint p_pkey on table p because other objects depend on it`)
			d.Detail = `constraint fk on table t depends on index p_pkey`
			d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			fixture.AssertDiagnostic(t, err, &d)
			run(fixture.Exec("ALTER TABLE p DROP CONSTRAINT p_pkey CASCADE", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(held.SQL, 1, 9, "now_allowed"))
			run(fixture.Select("SELECT renamed,note FROM t ORDER BY note", []string{"renamed", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "now_allowed"}, {int64(9), "old"}}))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
		})
	})
}
