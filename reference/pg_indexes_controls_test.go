package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func pgIndexesControlsScenario(t *testing.T) {
	p := newReferenceScenario(t)
	native := p.backends()[0]
	for _, q := range []string{"CREATE TABLE pgi_control_table(n INT,s TEXT,p TEXT)", "CREATE INDEX pgi_control ON pgi_control_table(lower(s),n DESC NULLS LAST) INCLUDE(p)", "CREATE INDEX pgi_control_hash ON pgi_control_table USING hash(n)"} {
		referenceRunStep(t, p.ctx, native.db, native.recorder, referenceExec(q, 0))
	}
	referenceRunStep(t, p.ctx, native.db, native.recorder, referenceExec("INSERT INTO pgi_control_table VALUES(2,'A','payload')", 1))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "CREATE INDEX") || strings.Contains(dump.String(), "pg_indexes") {
		t.Fatal("Dump must contain owning indexes, never virtual rows", dump.String())
	}
	expected := [][]any{
		pgIndexesRow("public", "pgi_control_table", "pgi_control", "CREATE INDEX pgi_control ON public.pgi_control_table USING btree (lower(s), n DESC NULLS LAST) INCLUDE (p)"),
		pgIndexesRow("public", "pgi_control_table", "pgi_control_hash", "CREATE INDEX pgi_control_hash ON public.pgi_control_table USING hash (n)"),
	}
	q := "SELECT * FROM pg_catalog.pg_indexes WHERE schemaname='public' AND tablename='pgi_control_table' ORDER BY indexname"
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			check := func(db referenceExecutor, rows [][]any) {
				referenceRunStep(t, p.ctx, db, b.recorder, pgIndexesSelect(b, q, rows))
			}
			data := func(db referenceExecutor) {
				referenceRunStep(t, p.ctx, db, b.recorder, referenceSelect("SELECT n,s,p FROM pgi_control_table", []string{"n", "s", "p"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(2), "A", "payload"}}))
			}
			if b.pg {
				conn := sequenceCoreConn(t, p.ctx, b.db)
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				if _, err := conn.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal("PG18 rejected actual native Dump", err)
				}
				check(conn, expected)
				data(conn)
				stmt, err := conn.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				check(referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}, expected)
				return
			}
			check(b.db, expected)
			data(b.db)
			stmt, err := b.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}
			check(held, expected)
			old, err := b.db.QueryContext(p.ctx, pgIndexesProbe, "pgi_control")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			oldTypes, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			pgIndexesColumnMetadata(t, oldTypes, false)
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String()+"CREATE SEQUENCE pgi_control_invalid CACHE 0;")); err == nil {
				t.Fatal("late invalid Restore succeeded")
			} else {
				want := controlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
				referenceAssertDiagnostic(t, err, &want)
			}
			check(b.db, expected)
			data(b.db)
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check(b.db, expected)
			data(b.db)
			d := stuntgres.NewDriver()
			c, err := d.OpenConnector(t.Name() + "-foreign")
			if err != nil {
				t.Fatal(err)
			}
			other := sql.OpenDB(c)
			defer other.Close()
			if err := stuntgres.Restore(p.ctx, other, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			// The separate raw Driver has no middleware; use the fixture's plain route.
			foreignRows := pgIndexesSelect(b, q, expected)
			foreignRows.nonContext = true
			referenceRunStep(t, p.ctx, other, b.recorder, foreignRows)
			foreignData := referenceSelect("SELECT n,s,p FROM pgi_control_table", []string{"n", "s", "p"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(2), "A", "payload"}})
			foreignData.nonContext = true
			referenceRunStep(t, p.ctx, other, b.recorder, foreignData)
			check(b.db, expected)
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			check(b.db, nil)
			check(held, nil)
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(b.db, expected)
			check(held, expected)
			data(b.db)
			pgIndexesRetained(t, old, oldTypes, false, expected[0])
		})
	}
}
func TestReferencePGIndexesControls(t *testing.T) { pgIndexesControlsScenario(t) }
