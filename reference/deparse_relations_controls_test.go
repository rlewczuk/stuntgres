package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

func TestReferenceDeparseRelationsNativeControls(t *testing.T) {
	deparseRelationsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		deparseRelationsSeed(t, p, b, c)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE u(c INT UNIQUE NULLS NOT DISTINCT)", 0))
		run(referenceExec("INSERT INTO u VALUES(1),(NULL)", 2))
		run(referenceExec("CREATE VIEW stored AS SELECT pg_get_viewdef('v'::text) AS definition FROM t WHERE id=1", 0))
		// SQL expectation shared with the PostgreSQL control phase; controls have no PG API.
		check := func(db *sql.DB, pg bool, extra bool) {
			r, err := db.QueryContext(p.ctx, "SELECT k,label FROM v")
			deleteReferenceConsume(t, pg, r, err, deparseRelationsBag("", extra))
			r, err = db.QueryContext(p.ctx, "SELECT pg_get_indexdef('ix'::regclass,2,true) AS definition")
			deleteReferenceConsume(t, pg, r, err, referenceSelect("", []string{"definition"}, []string{"TEXT"}, [][]any{{"txt"}}))
			r, err = db.QueryContext(p.ctx, "SELECT pg_get_indexdef('u_c_key'::regclass) AS definition")
			deleteReferenceConsume(t, pg, r, err, referenceSelect("", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX u_c_key ON public.u USING btree (c) NULLS NOT DISTINCT"}}))
			r, err = db.QueryContext(p.ctx, "SELECT definition IS NOT NULL AS present FROM stored")
			deleteReferenceConsume(t, pg, r, err, referenceSelect("", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
		}
		check(b.db, b.pg, false)
		if b.pg {
			return
		}
		old := deparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef('v') AS definition")
		// Eager native cursor survives reset/restore and still owns its old descriptor/value.
		retained, err := c.QueryContext(p.ctx, "SELECT pg_get_viewdef('v') AS definition")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		deparseRelationsMetadata(t, false, retained)
		dump := referenceViewDump(t, p)
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		run(deparseRelationsTextStep(t, b, "SELECT pg_get_viewdef(0::oid) AS definition", nil))
		referenceViewRestore(t, p, dump)
		if !retained.Next() {
			t.Fatal(retained.Err())
		}
		var value any
		if err := retained.Scan(&value); err != nil {
			t.Fatal(err)
		}
		if referenceNormalize(value) != old || retained.Next() || retained.Err() != nil {
			t.Fatal("retained reset value", value, retained.Err())
		}
		retained.Close()
		check(b.db, false, false)
		native := p.native
		for range 2 {
			var out bytes.Buffer
			if err := stuntgres.Dump(p.ctx, native, &out); err != nil {
				t.Fatal(err)
			}
			fresh := updateReturningNativeDestination(t)
			fresh.SetMaxOpenConns(1)
			updateReturningReferenceExec(t, fresh, "CREATE TEMP SEQUENCE retained_one")
			updateReturningReferenceExec(t, fresh, "CREATE TEMP SEQUENCE retained_two")
			if err := stuntgres.Restore(p.ctx, fresh, bytes.NewReader(out.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(fresh, false, false)
			native = fresh
		}
		updateReturningReferenceExec(t, native, "INSERT INTO t VALUES(4,'d',true)")
		check(native, false, true)
		updateReturningReferenceExec(t, native, "CREATE TABLE sentinel(id INT)")
		updateReturningReferenceExec(t, native, "INSERT INTO sentinel VALUES(7)")
		assertUnchanged := func() {
			check(native, false, true)
			r, err := native.QueryContext(p.ctx, "SELECT id FROM sentinel")
			deleteReferenceConsume(t, false, r, err, referenceSelect("", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			r, err = native.QueryContext(p.ctx, "SELECT relname FROM pg_class WHERE relname='lost'")
			deleteReferenceConsume(t, false, r, err, referenceSelect("", []string{"relname"}, []string{"NAME"}, nil))
		}
		if err := stuntgres.Restore(p.ctx, native, bytes.NewBufferString("CREATE TABLE lost(id INT); CREATE VIEW broken AS SELECT missing FROM lost;")); err == nil {
			t.Fatal("invalid suffix accepted")
		}
		assertUnchanged()
		readFailure := errors.New("deparse relation import reader failed")
		if err := stuntgres.Restore(p.ctx, native, catalogControlFailingReader{err: readFailure}); !errors.Is(err, readFailure) {
			t.Fatal(err)
		}
		assertUnchanged()
		canceled, cancel := context.WithCancel(p.ctx)
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
		duplicate := controlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "u_c_key"`)
		duplicate.Detail = "Key (c)=(null) already exists."
		duplicate.Schema = "public"
		duplicate.Table = "u"
		duplicate.Constraint = "u_c_key"
		_, err = native.ExecContext(p.ctx, "INSERT INTO u VALUES(NULL)")
		referenceAssertDiagnostic(t, err, &duplicate)
	})
}
