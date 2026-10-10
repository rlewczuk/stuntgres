//go:build reference

package reference_test

import "testing"

func TestReferenceDeleteReturningBoundaries(t *testing.T) {
	p := newReferencePair(t)
	// PostgreSQL-only controls combine records with otherwise shared scalar
	// subqueries, or require SRFs, triggers, FKs and rewriting.
	t.Run("postgres", func(t *testing.T) {
		db, r := p.postgres, p.postgresRecord
		referenceLockExec(t, p.ctx, db, "CREATE TABLE t(id INT PRIMARY KEY)", 0)
		referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(1)", 1)
		referenceRunStep(t, p.ctx, db, r, referenceSelect("DELETE FROM t RETURNING (SELECT 7) AS scalar,old::text AS predecessor,new::text AS replacement", []string{"scalar", "predecessor", "replacement"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(7), "(1)", nil}}))
		referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(1)", 1)
		// PG18.0's parenthesized whole-row expansion preserves target OLD
		// fields here; this excluded record path is not ordinary new.id/new.*.
		referenceRunStep(t, p.ctx, db, r, referenceSelect("DELETE FROM t RETURNING (old).*,(new).*", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
		for _, tc := range []struct{ q, code, message string }{
			{"DELETE FROM t RETURNING generate_series(1,2)", "0A000", "set-returning functions are not allowed in RETURNING"},
		} {
			s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: "25"})
			s.selectRows = true
			referenceRunStep(t, p.ctx, db, r, s)
		}
		referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(1)", 1)
		referenceLockExec(t, p.ctx, db, "CREATE TABLE child(id INT CONSTRAINT child_ref REFERENCES t(id))", 0)
		referenceLockExec(t, p.ctx, db, "INSERT INTO child VALUES(1)", 1)
		s := referenceImplicitError("DELETE FROM t RETURNING id", referenceDiagnostic{Code: "23503", Message: `update or delete on table "t" violates foreign key constraint "child_ref" on table "child"`, Detail: `Key (id)=(1) is still referenced from table "child".`, Schema: "public", Table: "child", Constraint: "child_ref"})
		s.selectRows = true
		referenceRunStep(t, p.ctx, db, r, s)
		deleteReferenceRows(t, p, db, r, 1)
		referenceLockExec(t, p.ctx, db, "CREATE FUNCTION suppress_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$", 0)
		referenceLockExec(t, p.ctx, db, "CREATE TRIGGER suppress BEFORE DELETE ON t FOR EACH ROW EXECUTE FUNCTION suppress_delete()", 0)
		referenceRunStep(t, p.ctx, db, r, referenceSelect("DELETE FROM t RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{}))
		deleteReferenceRows(t, p, db, r, 1)
		referenceLockExec(t, p.ctx, db, "CREATE TABLE rewritten(id INT)", 0)
		referenceLockExec(t, p.ctx, db, "INSERT INTO rewritten VALUES(1)", 1)
		referenceLockExec(t, p.ctx, db, "CREATE RULE rewrite_delete AS ON DELETE TO rewritten DO INSTEAD UPDATE rewritten SET id=old.id+10 WHERE id=old.id RETURNING rewritten.*", 0)
		referenceRunStep(t, p.ctx, db, r, referenceSelect("DELETE FROM rewritten RETURNING old.id,new.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}}))
		referenceRunStep(t, p.ctx, db, r, referenceSelect("SELECT id FROM rewritten", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
	})
}
