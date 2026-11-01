package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func deferredConstraintNameCollisions(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		deferredSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE TABLE collision(id integer,v integer,CONSTRAINT c CHECK(v>0))")
		checkCollision := func(table string) {
			// The relation identity is contextual, not an allocator-sequence golden.
			var owner int64
			if err := c.QueryRowContext(p.Ctx, "SELECT '"+table+"'::regclass::oid::bigint").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			if owner <= 0 {
				t.Fatal("invalid owner identity", owner)
			}
			q := "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON " + table + " DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_log()"
			var result sql.Result
			var err error
			if prepared {
				h, e := c.PrepareContext(p.Ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				defer h.Close()
				result, err = h.ExecContext(p.Ctx)
			} else {
				result, err = c.ExecContext(p.Ctx, q)
			}
			if result != nil {
				t.Fatal("failed constraint declaration returned result", result)
			}
			// The unique catalog key reports the actual owning relation identity.
			want := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "pg_constraint_conrelid_contypid_conname_index"`)
			want.Detail = fmt.Sprintf("Key (conrelid, contypid, conname)=(%d, 0, c) already exists.", owner)
			want.Schema = "pg_catalog"
			want.Table = "pg_constraint"
			want.Constraint = "pg_constraint_conrelid_contypid_conname_index"
			fixture.AssertDiagnostic(t, err, &want)
		}
		checkCollision("collision")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_trigger WHERE tgrelid='collision'::regclass", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		userOperatorExec(t, p, b, c, deferredCreate("c", "INSERT", "DEFERRABLE INITIALLY DEFERRED"), "ALTER TRIGGER c ON d RENAME TO live")
		checkCollision("d")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT t.tgname,c.conname FROM pg_trigger t JOIN pg_constraint c ON c.oid=t.tgconstraint WHERE t.tgrelid='d'::regclass", []string{"tgname", "conname"}, []string{"NAME", "NAME"}, [][]any{{"live", "c"}}))
		for _, clause := range []string{"CHECK(v>0)", "UNIQUE(v)", "EXCLUDE USING btree(v WITH =)"} {
			afterStep(t, p, b, c, prepared, fixture.ErrorDiagnostic("ALTER TABLE d ADD CONSTRAINT c "+clause, fixture.ControlsReferenceDiagnostic("42710", `constraint "c" for relation "d" already exists`)))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT t.tgname,c.conname,c.contype FROM pg_trigger t JOIN pg_constraint c ON c.oid=t.tgconstraint WHERE t.tgrelid='d'::regclass", []string{"tgname", "conname", "contype"}, []string{"NAME", "NAME", "CHAR"}, [][]any{{"live", "c", "t"}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='c'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		afterStep(t, p, b, c, prepared, fixture.ErrorDiagnostic(deferredCreate("live", "INSERT", "DEFERRABLE"), fixture.ControlsReferenceDiagnostic("42710", `trigger "live" for relation "d" already exists`)))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, deferredAudit([]any{"INSERT", int64(1), nil, int64(10), int64(1), "live"}))
	})
}
