//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteReturningBoundaries(t *testing.T) {
	p := fixture.NewPair(t)
	// PostgreSQL-only controls combine records with otherwise shared scalar
	// subqueries, or require triggers and rewriting.
	t.Run("postgres", func(t *testing.T) {
		db, r := p.Postgres, p.PostgresRecord
		fixture.LockExec(t, p.Ctx, db, "CREATE TABLE t(id INT PRIMARY KEY)", 0)
		fixture.LockExec(t, p.Ctx, db, "INSERT INTO t VALUES(1)", 1)
		fixture.RunStep(t, p.Ctx, db, r, fixture.Select("DELETE FROM t RETURNING (SELECT 7) AS scalar,old::text AS predecessor,new::text AS replacement", []string{"scalar", "predecessor", "replacement"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(7), "(1)", nil}}))
		fixture.LockExec(t, p.Ctx, db, "INSERT INTO t VALUES(1)", 1)
		// PG18.0's parenthesized whole-row expansion preserves target OLD
		// fields here; this excluded record path is not ordinary new.id/new.*.
		fixture.RunStep(t, p.Ctx, db, r, fixture.Select("DELETE FROM t RETURNING (old).*,(new).*", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
		fixture.LockExec(t, p.Ctx, db, "INSERT INTO t VALUES(1)", 1)
		fixture.LockExec(t, p.Ctx, db, "CREATE FUNCTION suppress_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$", 0)
		fixture.LockExec(t, p.Ctx, db, "CREATE TRIGGER suppress BEFORE DELETE ON t FOR EACH ROW EXECUTE FUNCTION suppress_delete()", 0)
		fixture.RunStep(t, p.Ctx, db, r, fixture.Select("DELETE FROM t RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{}))
		fixture.DeleteReferenceRows(t, p, db, r, 1)
		fixture.LockExec(t, p.Ctx, db, "CREATE TABLE rewritten(id INT)", 0)
		fixture.LockExec(t, p.Ctx, db, "INSERT INTO rewritten VALUES(1)", 1)
		fixture.LockExec(t, p.Ctx, db, "CREATE RULE rewrite_delete AS ON DELETE TO rewritten DO INSTEAD UPDATE rewritten SET id=old.id+10 WHERE id=old.id RETURNING rewritten.*", 0)
		fixture.RunStep(t, p.Ctx, db, r, fixture.Select("DELETE FROM rewritten RETURNING old.id,new.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}}))
		fixture.RunStep(t, p.Ctx, db, r, fixture.Select("SELECT id FROM rewritten", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
	})
}
