package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeparseRelationsNativeControls(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.DeparseRelationsSeed(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE u(c INT UNIQUE NULLS NOT DISTINCT)", 0))
		run(fixture.Exec("INSERT INTO u VALUES(1),(NULL)", 2))
		run(fixture.Exec("CREATE VIEW stored AS SELECT pg_get_viewdef('v'::text) AS definition FROM t WHERE id=1", 0))
		run(fixture.Exec(`CREATE INDEX clause_cover ON t(id int4_ops,txt COLLATE "POSIX" text_pattern_ops DESC NULLS LAST) INCLUDE(visible)`, 0))
		// SQL expectation shared with the PostgreSQL control phase; controls have no PG API.
		check := func(db *sql.DB, pg bool, extra bool) {
			r, err := db.QueryContext(p.Ctx, "SELECT k,label FROM v")
			fixture.DeleteReferenceConsume(t, pg, r, err, fixture.DeparseRelationsBag("", extra))
			r, err = db.QueryContext(p.Ctx, "SELECT pg_get_indexdef('ix'::regclass,2,true) AS definition")
			fixture.DeleteReferenceConsume(t, pg, r, err, fixture.Select("", []string{"definition"}, []string{"TEXT"}, [][]any{{"txt"}}))
			r, err = db.QueryContext(p.Ctx, "SELECT pg_get_indexdef('u_c_key'::regclass) AS definition")
			fixture.DeleteReferenceConsume(t, pg, r, err, fixture.Select("", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX u_c_key ON public.u USING btree (c) NULLS NOT DISTINCT"}}))
			r, err = db.QueryContext(p.Ctx, "SELECT definition IS NOT NULL AS present FROM stored")
			fixture.DeleteReferenceConsume(t, pg, r, err, fixture.Select("", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
			for _, tc := range []struct{ call, want string }{
				{"pg_get_indexdef('clause_cover'::regclass)", `CREATE INDEX clause_cover ON public.t USING btree (id, txt COLLATE "POSIX" text_pattern_ops DESC NULLS LAST) INCLUDE (visible)`},
				{"pg_get_indexdef('clause_cover'::regclass,3,false)", "visible"},
			} {
				r, err := db.QueryContext(p.Ctx, "SELECT "+tc.call+" AS definition")
				if err != nil {
					t.Fatal(err)
				}
				fixture.DeparseRelationsMetadata(t, pg, r)
				fixture.DeleteReferenceConsume(t, pg, r, err, fixture.Select("", []string{"definition"}, []string{"TEXT"}, [][]any{{tc.want}}))
			}
		}
		check(b.DB, b.PG, false)
		if b.PG {
			return
		}
		old := fixture.DeparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef('v') AS definition")
		// Eager native cursor survives reset/restore and still owns its old descriptor/value.
		retained, err := c.QueryContext(p.Ctx, "SELECT pg_get_viewdef('v') AS definition")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		fixture.DeparseRelationsMetadata(t, false, retained)
		dump := fixture.ViewDump(t, p)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_viewdef(0::oid) AS definition", nil))
		fixture.ViewRestore(t, p, dump)
		if !retained.Next() {
			t.Fatal(retained.Err())
		}
		var value any
		if err := retained.Scan(&value); err != nil {
			t.Fatal(err)
		}
		if fixture.Normalize(value) != old || retained.Next() || retained.Err() != nil {
			t.Fatal("retained reset value", value, retained.Err())
		}
		retained.Close()
		check(b.DB, false, false)
		native := p.Native
		for range 2 {
			var out bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, native, &out); err != nil {
				t.Fatal(err)
			}
			fresh := fixture.UpdateReturningNativeDestination(t)
			fresh.SetMaxOpenConns(1)
			fixture.UpdateReturningReferenceExec(t, fresh, "CREATE TEMP SEQUENCE retained_one")
			fixture.UpdateReturningReferenceExec(t, fresh, "CREATE TEMP SEQUENCE retained_two")
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(out.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(fresh, false, false)
			native = fresh
		}
		fixture.UpdateReturningReferenceExec(t, native, "INSERT INTO t VALUES(4,'d',true)")
		check(native, false, true)
		fixture.UpdateReturningReferenceExec(t, native, "CREATE TABLE sentinel(id INT)")
		fixture.UpdateReturningReferenceExec(t, native, "INSERT INTO sentinel VALUES(7)")
		assertUnchanged := func() {
			check(native, false, true)
			r, err := native.QueryContext(p.Ctx, "SELECT id FROM sentinel")
			fixture.DeleteReferenceConsume(t, false, r, err, fixture.Select("", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			r, err = native.QueryContext(p.Ctx, "SELECT relname FROM pg_class WHERE relname='lost'")
			fixture.DeleteReferenceConsume(t, false, r, err, fixture.Select("", []string{"relname"}, []string{"NAME"}, nil))
		}
		if err := stuntgres.Restore(p.Ctx, native, bytes.NewBufferString("CREATE TABLE lost(id INT); CREATE VIEW broken AS SELECT missing FROM lost;")); err == nil {
			t.Fatal("invalid suffix accepted")
		}
		assertUnchanged()
		readFailure := errors.New("deparse relation import reader failed")
		if err := stuntgres.Restore(p.Ctx, native, fixture.CatalogControlFailingReader{Err: readFailure}); !errors.Is(err, readFailure) {
			t.Fatal(err)
		}
		assertUnchanged()
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		assertUnchanged()
		canceledRows, canceledErr := native.QueryContext(canceled, "SELECT pg_get_viewdef('v') AS definition")
		if canceledRows != nil {
			canceledRows.Close()
			t.Fatal("precanceled query published rows")
		}
		if !errors.Is(canceledErr, context.Canceled) {
			t.Fatal(canceledErr)
		}
		assertUnchanged()
		duplicate := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "u_c_key"`)
		duplicate.Detail = "Key (c)=(null) already exists."
		duplicate.Schema = "public"
		duplicate.Table = "u"
		duplicate.Constraint = "u_c_key"
		_, err = native.ExecContext(p.Ctx, "INSERT INTO u VALUES(NULL)")
		fixture.AssertDiagnostic(t, err, &duplicate)
	})
}
