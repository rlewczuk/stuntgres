package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGLSNDiffStored(t *testing.T) {
	p := fixture.NewScenario(t)
	query := "SELECT difference FROM pglsn_diff_view ORDER BY id"
	initial := fixture.Select(query, []string{"difference"}, []string{"NUMERIC"}, [][]any{{"18446744073709551615"}, {"-18446744073709551615"}, {nil}})
	complete := fixture.Select(query, []string{"difference"}, []string{"NUMERIC"}, [][]any{{"18446744073709551615"}, {"-18446744073709551615"}, {nil}, {"0"}})
	metadata := fixture.Select("SELECT atttypid::text,atttypmod FROM pg_catalog.pg_attribute WHERE attrelid='pglsn_diff_view'::regclass AND attname='difference'", []string{"atttypid", "atttypmod"}, []string{"TEXT", "INT4"}, [][]any{{"1700", int64(-1)}})
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE pglsn_diff_saved(id integer,a pg_lsn,b pg_lsn)", 0))
			run(fixture.Exec("INSERT INTO pglsn_diff_saved VALUES(1,'FFFFFFFF/FFFFFFFF','0/0'),(2,'0/0','FFFFFFFF/FFFFFFFF'),(3,NULL,'0/0')", 3))
			run(fixture.Exec("CREATE VIEW pglsn_diff_view AS SELECT id,pg_wal_lsn_diff(a,b) AS difference FROM pglsn_diff_saved", 0))
			run(initial)
			run(metadata)
			stmt, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, initial)
			run(fixture.Exec("INSERT INTO pglsn_diff_saved VALUES(4,'FFFFFFFF/FFFFFFFF','FFFFFFFF/FFFFFFFF')", 1))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, complete)
		})
	}
	// Native controls have no PostgreSQL counterpart. Replay the actual emitted
	// recipe on both a reset native catalog and, in real mode, the pinned oracle.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), `"pg_catalog"."pg_wal_lsn_diff"(`) && !strings.Contains(dump.String(), "pg_catalog.pg_wal_lsn_diff(") {
		t.Fatal("missing schema-qualified pure function recipe", dump.String())
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("unstable stored LSN difference recipe", err, dump.String(), redump.String())
	}
	for _, b := range p.Backends() {
		if !b.PG {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, complete)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(complete))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, metadata)
			continue
		}
		tx, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("DROP VIEW pglsn_diff_view", 0))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("DROP TABLE pglsn_diff_saved", 0))
		if _, err := tx.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("emitted pure function recipe replay", err)
		}
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, complete)
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, metadata)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}
