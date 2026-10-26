package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAlterDropColumnRoutineControls(t *testing.T) {
	dcrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		dcrCascadeSetup(t, p, b, c)
		check := func(x *sql.Conn, backend fixture.Backend) {
			dcrAttributeEdges(t, p, backend, x, "dcr_body()", "dcr_t", 1)
			dcrAttributeEdges(t, p, backend, x, "dcr_default(integer)", "dcr_t", 1)
			dcrAttributeEdges(t, p, backend, x, "dcr_both(integer)", "dcr_t", 1, 1)
			dcrValue(t, p, backend, x, "SELECT dcr_body() AS v", int64(7))
			dcrValue(t, p, backend, x, "SELECT dcr_default() AS v", nil)
			dcrValue(t, p, backend, x, "SELECT dcr_default(17) AS v", int64(17))
			dcrError(t, p, backend, x, "ALTER TABLE dcr_t DROP a RESTRICT", dcrBodyAndClosureRestrict(), false)
		}
		retire := func(x *sql.Conn, backend fixture.Backend, n *[]fixture.Diagnostic) {
			ids := dcrRetiredIDs(t, p, x)
			dcrExec(t, p, backend, x, "ALTER TABLE dcr_t DROP a CASCADE")
			dcrNotices(t, n, dcrCascadeObjects()...)
			dcrCascadeSurvivors(t, p, backend, x, ids)
		}
		check(c, b)
		if b.PG {
			retire(c, b, notices)
			return
		}
		// Native controls are explicit phases of this same SQL scenario. This
		// entire branch is unreachable while the shared discovery filter exists.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		c.Close()
		for range 2 {
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fresh, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			check(fresh, b)
			// Borrow notice-enabled fresh handles, never the pre-reset connection.
			fresh.Close()
			_, fresh, rec := fixture.NoticeReferencePool(t, p, false, notices)
			backend := b
			backend.Recorder = rec
			retire(fresh, backend, notices)
			fresh.Close()
		}
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/clone")
		if err != nil {
			t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		var cloneNotices []fixture.Diagnostic
		noticeConnector := connector.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) {
			cloneNotices = append(cloneNotices, fixture.ExtractDiagnostic(t, &n))
		})
		recorded, rec := fixture.RecordedNativePool(t, noticeConnector)
		cloneBackend := fixture.Backend{Recorder: rec}
		for range 2 {
			if err := stuntgres.Reset(p.Ctx, raw); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			clone, err := recorded.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			check(clone, cloneBackend)
			retire(clone, cloneBackend, &cloneNotices)
			clone.Close()
		}
		// Restore source as well and demonstrate that the clone's removals did
		// not mutate source state. Object identities are independently resolved.
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fresh, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		check(fresh, b)
		fresh.Close()
		if p.Postgres != nil {
			var pgNotices []fixture.Diagnostic
			_, pg, pgRec := fixture.NoticeReferencePool(t, p, true, &pgNotices)
			defer pg.Close()
			pgBackend := fixture.Backend{PG: true, Recorder: pgRec}
			if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("unchanged native SQL replay on release oracle", err)
			}
			check(pg, pgBackend)
			retire(pg, pgBackend, &pgNotices)
			// Leave the shared public fixture empty for its PostgreSQL phase.
			if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
	})
}
