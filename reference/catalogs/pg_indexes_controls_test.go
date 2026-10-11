package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func pgIndexesControlsScenario(t *testing.T) {
	p := fixture.NewScenario(t)
	native := p.Backends()[0]
	for _, q := range []string{"CREATE TABLE pgi_control_table(n INT,s TEXT,p TEXT)", "CREATE INDEX pgi_control ON pgi_control_table(lower(s),n DESC NULLS LAST) INCLUDE(p)", "CREATE INDEX pgi_control_hash ON pgi_control_table USING hash(n)"} {
		fixture.RunStep(t, p.Ctx, native.DB, native.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, native.DB, native.Recorder, fixture.Exec("INSERT INTO pgi_control_table VALUES(2,'A','payload')", 1))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "CREATE INDEX") || strings.Contains(dump.String(), "pg_indexes") {
		t.Fatal("Dump must contain owning indexes, never virtual rows", dump.String())
	}
	expected := [][]any{
		fixture.PgIndexesRow("public", "pgi_control_table", "pgi_control", "CREATE INDEX pgi_control ON public.pgi_control_table USING btree (lower(s), n DESC NULLS LAST) INCLUDE (p)"),
		fixture.PgIndexesRow("public", "pgi_control_table", "pgi_control_hash", "CREATE INDEX pgi_control_hash ON public.pgi_control_table USING hash (n)"),
	}
	q := "SELECT * FROM pg_catalog.pg_indexes WHERE schemaname='public' AND tablename='pgi_control_table' ORDER BY indexname"
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			check := func(db fixture.Executor, rows [][]any) {
				fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.PgIndexesSelect(b, q, rows))
			}
			data := func(db fixture.Executor) {
				fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Select("SELECT n,s,p FROM pgi_control_table", []string{"n", "s", "p"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(2), "A", "payload"}}))
			}
			if b.PG {
				conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				if _, err := conn.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("PG18 rejected actual native Dump", err)
				}
				check(conn, expected)
				data(conn)
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				check(fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, expected)
				return
			}
			check(b.DB, expected)
			data(b.DB)
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			check(held, expected)
			old, err := b.DB.QueryContext(p.Ctx, fixture.PgIndexesProbe, "pgi_control")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			oldTypes, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.PgIndexesColumnMetadata(t, oldTypes, false)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"CREATE SEQUENCE pgi_control_invalid CACHE 0;")); err == nil {
				t.Fatal("late invalid Restore succeeded")
			} else {
				want := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
				fixture.AssertDiagnostic(t, err, &want)
			}
			check(b.DB, expected)
			data(b.DB)
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check(b.DB, expected)
			data(b.DB)
			d := stuntgres.NewDriver()
			c, err := d.OpenConnector(t.Name() + "-foreign")
			if err != nil {
				t.Fatal(err)
			}
			other := sql.OpenDB(c)
			defer other.Close()
			if err := stuntgres.Restore(p.Ctx, other, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			// The separate raw Driver has no middleware; use the fixture's plain route.
			foreignRows := fixture.PgIndexesSelect(b, q, expected)
			foreignRows.NonContext = true
			fixture.RunStep(t, p.Ctx, other, b.Recorder, foreignRows)
			foreignData := fixture.Select("SELECT n,s,p FROM pgi_control_table", []string{"n", "s", "p"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(2), "A", "payload"}})
			foreignData.NonContext = true
			fixture.RunStep(t, p.Ctx, other, b.Recorder, foreignData)
			check(b.DB, expected)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			check(b.DB, nil)
			check(held, nil)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(b.DB, expected)
			check(held, expected)
			data(b.DB)
			pgIndexesRetained(t, old, oldTypes, false, expected[0])
		})
	}
}
func TestReferencePGIndexesControls(t *testing.T) { pgIndexesControlsScenario(t) }
