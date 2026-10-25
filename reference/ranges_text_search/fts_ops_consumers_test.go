package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFTSOpsConsumers(t *testing.T) {
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE fts_ops_source(id integer,a tsquery,b tsquery,d integer,v tsvector,w tsvector)", 0))
		run(fixture.Exec("INSERT INTO fts_ops_source VALUES(2,'a | b','c:*A',2,'a:1 z:10','a:2B')", 1))
		run(fixture.Exec("CREATE TABLE fts_ops_saved(id integer,q tsquery DEFAULT ('a'::tsquery <-> tsquery_phrase('b','c',2)),v tsvector DEFAULT ('a:1'::tsvector || 'a:2B'::tsvector))", 0))
		run(fixture.Exec("INSERT INTO fts_ops_saved(id) VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO fts_ops_saved SELECT id,tsquery_phrase(a,b,d),v || w FROM fts_ops_source", 1))
		run(fixture.Exec("UPDATE fts_ops_saved SET v=v || 'x'::tsvector WHERE id=2", 1))
		run(fixture.Exec("CREATE VIEW fts_ops_view AS SELECT id,tsquery_phrase(q,'x',0) AS q,v || 'view'::tsvector AS v FROM fts_ops_saved", 0))
		want := [][]any{{int64(1), "'a' <-> ( 'b' <2> 'c' )", "'a':1,3B"}, {int64(2), "( 'a' | 'b' ) <2> 'c':*A", "'a':1,12B 'x' 'z':10"}}
		saved := fixture.Select("SELECT id,q,v FROM fts_ops_saved ORDER BY id", []string{"id", "q", "v"}, []string{"INT4", "TSQUERY", "TSVECTOR"}, want)
		run(saved)
		run(fixture.Prepared(saved))
		view := fixture.Select("SELECT id,q,v FROM fts_ops_view ORDER BY id", []string{"id", "q", "v"}, []string{"INT4", "TSQUERY", "TSVECTOR"}, [][]any{{int64(1), "'a' <-> ( 'b' <2> 'c' ) <0> 'x'", "'a':1,3B 'view'"}, {int64(2), "( 'a' | 'b' ) <2> 'c':*A <0> 'x'", "'a':1,12B 'view' 'x' 'z':10"}})
		run(view)
		run(fixture.Prepared(view))
		// The second tuple fails at execution; the first must not become visible.
		query := "INSERT INTO fts_ops_saved VALUES(3,tsquery_phrase($1::tsquery,$2::tsquery,$3::int4),'ok'),(4,tsquery_phrase($1::tsquery,$2::tsquery,$4::int4),'bad')"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(query, ftsOpsDistanceError(), "a", "b", 1, 16385)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			run(saved)
		}
		tx, err := conn.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		failure := fixture.ErrorDiagnostic("INSERT INTO fts_ops_saved VALUES(5,tsquery_phrase($1::tsquery,$2::tsquery,$3::int4),'bad')", ftsOpsDistanceError(), "a", "b", -1)
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, failure)
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("SELECT id FROM fts_ops_saved", fixture.TransactionAborted()))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(saved)
		// Held SELECT consumes fresh typed values, not just folded constants.
		heldSQL := "SELECT tsquery_phrase(a,$1::tsquery,$2::int4) AS q,v || $3::tsvector AS v FROM fts_ops_source ORDER BY id"
		stmt, err := conn.PrepareContext(p.Ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: heldSQL}
		for _, tc := range []struct {
			q                    any
			distance             int
			vector, wantQ, wantV string
		}{
			{"x", 0, "b:1", "( 'a' | 'b' ) <0> 'x'", "'a':1 'b':11 'z':10"},
			{"y", 2, "a:1B", "( 'a' | 'b' ) <2> 'y'", "'a':1,11B 'z':10"},
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(heldSQL, []string{"q", "v"}, []string{"TSQUERY", "TSVECTOR"}, [][]any{{tc.wantQ, tc.wantV}}, tc.q, tc.distance, tc.vector))
		}
		if !b.PG {
			ftsOpsNativeControls(t, p, b, "fts_ops_saved", saved, view)
		}
	})
}

// Native controls have no PG API equivalent. Replay the actual emitted SQL on
// PostgreSQL and assert the same independent rows/types, not dump substrings.
func ftsOpsNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, table string, saved fixture.Step, extras ...fixture.Step) {
	t.Helper()
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		rows, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, saved.Query, nil)
		if err != nil {
			return err
		}
		defer rows.Close()
		metadata := func() {
			if !reflect.DeepEqual(rows.Columns(), saved.Columns) {
				t.Fatal("retained columns", rows.Columns())
			}
			for i, typ := range saved.Types {
				if rows.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i) != typ {
					t.Fatal("retained type", i)
				}
				if typ == "TSQUERY" || typ == "TSVECTOR" {
					if rows.(driver.RowsColumnTypeScanType).ColumnTypeScanType(i) != reflect.TypeFor[string]() {
						t.Fatal("retained scan type", i)
					}
				}
			}
		}
		metadata()
		var dump, redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			return err
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			return err
		}
		if dump.String() != redump.String() {
			t.Fatal("FTS operation redump changed")
		}
		check := func() {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, saved)
			for _, s := range extras {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		check()
		if p.Postgres != nil {
			tx, err := p.Postgres.BeginTx(p.Ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(p.Ctx, dump.String()); err != nil {
				return err
			}
			fixture.RunStep(t, p.Ctx, tx, p.PostgresRecord, saved)
			for _, s := range extras {
				fixture.RunStep(t, p.Ctx, tx, p.PostgresRecord, s)
			}
			if err := tx.Rollback(); err != nil {
				return err
			}
		}
		// A later staged VALUES expression fails during phrase evaluation, not
		// admission. Restore query sources remain outside the supported surface.
		script := "CREATE TABLE fts_ops_rejected(d integer,q tsquery); INSERT INTO fts_ops_rejected VALUES(1,'sentinel'); INSERT INTO fts_ops_rejected VALUES(2,tsquery_phrase('a','b',16385))"
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
		d := ftsOpsDistanceError()
		fixture.AssertDiagnostic(t, err, &d)
		check()
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, strings.NewReader("CREATE TABLE fts_ops_lost(q tsquery)")); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Restore", err)
		}
		var ignored bytes.Buffer
		if err := stuntgres.Dump(canceled, p.Native, &ignored); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Dump", err)
		}
		if err := stuntgres.Reset(canceled, p.Native); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Reset", err)
		}
		check()
		// Restored defaults and stored view providers must remain executable.
		if table == "fts_ops_saved" {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO fts_ops_saved(id) VALUES(9)", 1))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT q,v FROM fts_ops_saved WHERE id=9", []string{"q", "v"}, []string{"TSQUERY", "TSVECTOR"}, [][]any{{"'a' <-> ( 'b' <2> 'c' )", "'a':1,3B"}}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM fts_ops_saved WHERE id=9", 1))
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE fts_ops_replacement(v text); INSERT INTO fts_ops_replacement VALUES('replacement')")); err != nil {
			return err
		}
		metadata()
		dest := make([]driver.Value, len(saved.Columns))
		for _, want := range saved.Rows {
			if err := rows.Next(dest); err != nil {
				return err
			}
			for i, v := range want {
				if dest[i] != v {
					t.Fatal("retained payload", dest, want)
				}
			}
		}
		if err := rows.Next(dest); err != io.EOF {
			t.Fatal("retained EOF", err)
		}
		metadata()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM fts_ops_replacement", []string{"v"}, []string{"TEXT"}, [][]any{{"replacement"}}))
}
