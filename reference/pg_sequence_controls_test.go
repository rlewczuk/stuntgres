package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func pgSequenceControlsScenario(t *testing.T) {
	t.Helper()
	p := newReferenceScenario(t)
	native := p.backends()[0]
	pgSequenceExec(t, p, native, native.db, "CREATE SEQUENCE pgs_control AS INTEGER INCREMENT 7 MINVALUE -20 MAXVALUE 200 START 15 CACHE 9 CYCLE")
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "CREATE SEQUENCE") || strings.Contains(dump.String(), "pg_sequence") {
		t.Fatal("Dump must serialize owning definition, not virtual rows", dump.String())
	}
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			if !b.pg {
				oid := pgDescriptionRelationID(t, p, b, "pgs_control")
				row := pgSequenceCustom(oid)
				pgSequenceCheck(t, p, b, b.db, oid, row)
				old, err := b.db.QueryContext(p.ctx, pgSequenceProbe, oid)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				// A supported CREATE fails option binding after valid dump
				// statements have staged, exercising atomic Restore failure.
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String()+"CREATE SEQUENCE pgs_control_invalid CACHE 0;")); err == nil {
					t.Fatal("failed Restore succeeded")
				} else {
					want := controlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
					referenceAssertDiagnostic(t, err, &want)
				}
				pgSequenceCheck(t, p, b, b.db, oid, row)
				pgDescriptionRows(t, p, b, pgSequenceSelect(b, "SELECT * FROM pg_catalog.pg_sequence", [][]any{row}))
				canceled, cancel := context.WithCancel(p.ctx)
				cancel()
				if err := stuntgres.Restore(canceled, p.native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
					t.Fatal("Restore cancellation", err)
				}
				pgSequenceCheck(t, p, b, b.db, oid, row)
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				pgDescriptionRows(t, p, b, pgSequenceSelect(b, "SELECT * FROM pg_catalog.pg_sequence", [][]any{}))
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
				fresh := pgDescriptionRelationID(t, p, b, "pgs_control")
				pgSequenceCheck(t, p, b, b.db, fresh, pgSequenceCustom(fresh))
				pgSequenceRetained(t, old, false, row)
			} else {
				conn := sequenceCoreConn(t, p.ctx, b.db)
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				if _, err := conn.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal("PG18 rejected actual native Dump", err)
				}
				oid := pgDescriptionRelationID(t, p, b, "pgs_control")
				pgSequenceCheck(t, p, b, conn, oid, pgSequenceCustom(oid))
			}
		})
	}
}

func TestReferencePGSequenceControls(t *testing.T) { pgSequenceControlsScenario(t) }
