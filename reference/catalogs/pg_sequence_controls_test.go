package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func pgSequenceControlsScenario(t *testing.T) {
	t.Helper()
	p := fixture.NewScenario(t)
	native := p.Backends()[0]
	pgSequenceExec(t, p, native, native.DB, "CREATE SEQUENCE pgs_control AS INTEGER INCREMENT 7 MINVALUE -20 MAXVALUE 200 START 15 CACHE 9 CYCLE")
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "CREATE SEQUENCE") || strings.Contains(dump.String(), "pg_sequence") {
		t.Fatal("Dump must serialize owning definition, not virtual rows", dump.String())
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			if !b.PG {
				oid := pgDescriptionRelationID(t, p, b, "pgs_control")
				row := pgSequenceCustom(oid)
				pgSequenceCheck(t, p, b, b.DB, oid, row)
				old, err := b.DB.QueryContext(p.Ctx, pgSequenceProbe, oid)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				// A supported CREATE fails option binding after valid dump
				// statements have staged, exercising atomic Restore failure.
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"CREATE SEQUENCE pgs_control_invalid CACHE 0;")); err == nil {
					t.Fatal("failed Restore succeeded")
				} else {
					want := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
					fixture.AssertDiagnostic(t, err, &want)
				}
				pgSequenceCheck(t, p, b, b.DB, oid, row)
				fixture.PgDescriptionRows(t, p, b, pgSequenceSelect(b, "SELECT * FROM pg_catalog.pg_sequence", [][]any{row}))
				canceled, cancel := context.WithCancel(p.Ctx)
				cancel()
				if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
					t.Fatal("Restore cancellation", err)
				}
				pgSequenceCheck(t, p, b, b.DB, oid, row)
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.PgDescriptionRows(t, p, b, pgSequenceSelect(b, "SELECT * FROM pg_catalog.pg_sequence", [][]any{}))
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
				fresh := pgDescriptionRelationID(t, p, b, "pgs_control")
				pgSequenceCheck(t, p, b, b.DB, fresh, pgSequenceCustom(fresh))
				pgSequenceRetained(t, old, false, row)
			} else {
				conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				if _, err := conn.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("PG18 rejected actual native Dump", err)
				}
				oid := pgDescriptionRelationID(t, p, b, "pgs_control")
				pgSequenceCheck(t, p, b, conn, oid, pgSequenceCustom(oid))
			}
		})
	}
}

func TestReferencePGSequenceControls(t *testing.T) { pgSequenceControlsScenario(t) }
