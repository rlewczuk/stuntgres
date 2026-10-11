package reference_test

import (
	"bytes"
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceNativeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "s")
	for _, backend := range p.Backends() {
		db := backend.DB
		for _, q := range []string{"CREATE SCHEMA s", "CREATE TABLE t(id INT,txt TEXT)", "INSERT INTO t VALUES(2,'b')", "CREATE VIEW v AS SELECT id,txt FROM t", "CREATE VIEW w AS SELECT id FROM v", "CREATE OR REPLACE VIEW v WITH(security_barrier=true,security_invoker=true) AS SELECT id+10 AS id,txt,TRUE AS extra FROM t", "ALTER VIEW v RENAME COLUMN id TO key", "ALTER VIEW v RENAME TO renamed", "ALTER VIEW renamed SET SCHEMA s", "ALTER VIEW s.renamed OWNER TO CURRENT_USER", "ALTER VIEW s.renamed RESET(security_invoker)"} {
			fixture.UpdateReturningReferenceExec(t, db, q)
		}
		native := p.Native
		for range 2 {
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, native, &dump); err != nil {
					t.Fatal(err)
				}
				fresh := fixture.UpdateReturningNativeDestination(t)
				fresh.SetMaxOpenConns(1)
				fixture.UpdateReturningReferenceExec(t, fresh, "CREATE TEMPORARY SEQUENCE retained_one")
				fixture.UpdateReturningReferenceExec(t, fresh, "CREATE TEMPORARY SEQUENCE retained_two")
				if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				native, db = fresh, fresh
			}
			rows, err := db.Query("SELECT key,txt,extra FROM s.renamed")
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Select("", []string{"key", "txt", "extra"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(12), "b", true}}))
		}
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(3,'c')")
		check := func() {
			rows, err := db.Query("SELECT id FROM w ORDER BY id")
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Select("", []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}, {int64(13)}}))
		}
		check()
		if !backend.PG {
			if err := stuntgres.Restore(p.Ctx, native, bytes.NewBufferString("CREATE TABLE lost(id INT); CREATE VIEW broken AS SELECT missing FROM lost;")); err == nil {
				t.Fatal("invalid Restore accepted")
			}
			check()
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, native, bytes.NewBufferString("CREATE TABLE lost(id INT);")); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check()
		}
	}
}
