package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGStatioAllSequencesLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "statio_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			statioSequenceExec(t, p, b, a, "CREATE SCHEMA statio_moved")
			statioSequenceExec(t, p, b, a, "CREATE SEQUENCE statio_lifecycle CACHE 3")
			oid := statioSequenceOID(t, p, a, "public.statio_lifecycle")
			statioSequenceExec(t, p, b, a, "BEGIN")
			pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_lifecycle')", "nextval", 1)
			statioSequenceExec(t, p, b, a, "COMMIT")
			stmt, err := observer.PrepareContext(p.Ctx, statioSequenceProbe)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: observer, Statement: stmt, SQL: statioSequenceProbe}
			statioSequenceCheck(t, p, b, held, oid, "public", "statio_lifecycle", 1, 0)
			// Native results eagerly own their rows. PostgreSQL streams rows on
			// the connection, so retain this ownership witness only natively.
			var old *sql.Rows
			if !b.PG {
				old, err = observer.QueryContext(p.Ctx, statioSequenceProbe, oid)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
			}
			for _, q := range []string{"ALTER SEQUENCE statio_lifecycle RENAME TO statio_renamed", "ALTER SEQUENCE statio_renamed SET SCHEMA statio_moved"} {
				statioSequenceExec(t, p, b, a, q)
			}
			statioSequenceCheck(t, p, b, held, oid, "statio_moved", "statio_renamed", 1, 0)
			for _, q := range []string{"BEGIN", "DROP SEQUENCE statio_moved.statio_renamed", "ROLLBACK"} {
				statioSequenceExec(t, p, b, a, q)
			}
			statioSequenceCheck(t, p, b, held, oid, "statio_moved", "statio_renamed", 1, 0)
			statioSequenceExec(t, p, b, a, "DROP SEQUENCE statio_moved.statio_renamed")
			statioSequenceCheck(t, p, b, held, oid, "", "", 0, 0)
			if old != nil {
				statioSequenceMetadata(t, old, false)
				if !old.Next() {
					t.Fatal("retained result missing", old.Err())
				}
				var r statioSequenceRow
				if err := old.Scan(&r.oid, &r.schema, &r.name, &r.read, &r.hit); err != nil {
					t.Fatal(err)
				}
				if r != (statioSequenceRow{oid: oid, schema: "public", name: "statio_lifecycle", read: 1}) {
					t.Fatal("retained result changed", r)
				}
				if old.Next() || old.Err() != nil {
					t.Fatal("retained result cardinality", old.Err())
				}
				old.Close()
			}
			statioSequenceExec(t, p, b, a, "CREATE SEQUENCE statio_moved.statio_renamed")
			next := statioSequenceOID(t, p, a, "statio_moved.statio_renamed")
			if next == oid {
				t.Fatal("recreation reused sequence identity")
			}
			statioSequenceCheck(t, p, b, held, next, "statio_moved", "statio_renamed", 0, 0)
			for _, q := range []string{"BEGIN", "CREATE SEQUENCE statio_aborted", "SELECT nextval('statio_aborted')", "ROLLBACK"} {
				if strings.HasPrefix(q, "SELECT") {
					pgSequencesScalar(t, p, b, a, q, "nextval", 1)
				} else {
					statioSequenceExec(t, p, b, a, q)
				}
			}
			if got := statioSequenceRows(t, p, b, observer, "SELECT * FROM pg_statio_all_sequences WHERE relname='statio_aborted'"); len(got) != 0 {
				t.Fatal("aborted sequence survived", got)
			}
		})
	}
}

func TestReferencePGStatioAllSequencesControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			statioSequenceExec(t, p, b, a, "CREATE SEQUENCE statio_control CACHE 3")
			oid := statioSequenceOID(t, p, a, "public.statio_control")
			statioSequenceExec(t, p, b, a, "BEGIN")
			for n := int64(1); n <= 4; n++ {
				pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_control')", "nextval", n)
			}
			statioSequenceExec(t, p, b, a, "COMMIT")
			statioSequenceCheck(t, p, b, observer, oid, "public", "statio_control", 1, 1)
			if b.PG {
				return
			} // Public native controls have no PostgreSQL analogue.
			statioSequenceExec(t, p, b, observer, "CREATE TEMP SEQUENCE statio_control_temp CACHE 3")
			tempOID := statioSequenceOID(t, p, observer, "pg_temp.statio_control_temp")
			// This writer has no prior encounter batch. Its first COMMIT publishes
			// TEMP history while leaving the owner and its CACHE tail attached.
			statioSequenceExec(t, p, b, observer, "BEGIN")
			pgSequencesScalar(t, p, b, observer, "SELECT nextval('statio_control_temp')", "nextval", 1)
			statioSequenceExec(t, p, b, observer, "COMMIT")
			tempRows := statioSequenceRows(t, p, b, a, statioSequenceProbe, tempOID)
			if len(tempRows) != 1 {
				t.Fatal("TEMP row", tempRows)
			}
			tempSchema := tempRows[0].schema
			statioSequenceCheck(t, p, b, a, tempOID, tempSchema, "statio_control_temp", 1, 0)
			old, err := a.QueryContext(p.Ctx, statioSequenceProbe, oid)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "pg_statio_all_sequences") || strings.Contains(dump.String(), "statio_control_temp") || !strings.Contains(dump.String(), "setval") {
				t.Fatal("dump contains monitoring/TEMP history or lacks generator state", dump.String())
			}
			statioSequenceCheck(t, p, b, a, oid, "public", "statio_control", 1, 1)
			statioSequenceCheck(t, p, b, a, tempOID, tempSchema, "statio_control_temp", 1, 0)
			want := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"CREATE SEQUENCE statio_bad CACHE 0;")), &want)
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			statioSequenceCheck(t, p, b, a, oid, "public", "statio_control", 1, 1)
			statioSequenceCheck(t, p, b, a, tempOID, tempSchema, "statio_control_temp", 1, 0)
			if got := statioSequenceRows(t, p, b, a, "SELECT * FROM pg_statio_all_sequences WHERE relname='statio_bad'"); len(got) != 0 {
				t.Fatal("failed restore leaked definition", got)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			statioSequenceCheck(t, p, b, a, oid, "", "", 0, 0)
			statioSequenceCheck(t, p, b, a, tempOID, tempSchema, "statio_control_temp", 1, 0)
			pgSequencesScalar(t, p, b, observer, "SELECT currval('statio_control_temp')", "currval", 1)
			pgSequencesScalar(t, p, b, observer, "SELECT nextval('statio_control_temp')", "nextval", 2)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			restoredOID := statioSequenceOID(t, p, a, "public.statio_control")
			statioSequenceCheck(t, p, b, a, restoredOID, "public", "statio_control", 0, 0)
			statioSequenceCheck(t, p, b, a, tempOID, tempSchema, "statio_control_temp", 1, 0)
			pgSequencesScalar(t, p, b, observer, "SELECT nextval('statio_control_temp')", "nextval", 3)
			statioSequenceCheck(t, p, b, a, tempOID, tempSchema, "statio_control_temp", 1, 0)
			// The restored generator reserves beyond the dumped high watermark;
			// its cumulative monitoring history is intentionally not serialized.
			pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_control')", "nextval", 7)
			statioSequenceMetadata(t, old, false)
			if !old.Next() {
				t.Fatal("retained control row", old.Err())
			}
			var retained statioSequenceRow
			if err := old.Scan(&retained.oid, &retained.schema, &retained.name, &retained.read, &retained.hit); err != nil {
				t.Fatal(err)
			}
			if retained != (statioSequenceRow{oid: oid, schema: "public", name: "statio_control", read: 1, hit: 1}) {
				t.Fatal("replacement mutated old result", retained)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal("retained control cardinality", old.Err())
			}
			old.Close()
			statioSequenceRetire(t, observer)
			statioSequenceCheck(t, p, b, a, tempOID, "", "", 0, 0)
		})
	}
}
