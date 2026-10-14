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

func TestReferencePGAttributeControls(t *testing.T) {
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pa18_control(id integer NOT NULL DEFAULT 7,matrix integer[][],former varchar(7))")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pa18_control(id,matrix,former) VALUES(9,'{{1,2},{3,4}}','x')", 1))
		pgClassExec(t, p, b, b.DB, "ALTER TABLE pa18_control DROP former")
		q := "SELECT attname,attnum,atttypid,atttypmod,attndims,attnotnull,atthasdef FROM pg_attribute WHERE attrelid='pa18_control'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum"
		want := [][]any{{"id", int64(1), int64(23), int64(-1), int64(0), true, true}, {"matrix", int64(2), int64(1007), int64(-1), int64(2), false, false}}
		s := fixture.Select(q, []string{"attname", "attnum", "atttypid", "atttypmod", "attndims", "attnotnull", "atthasdef"}, []string{"NAME", "INT2", "OID", "INT4", "INT2", "BOOL", "BOOL"}, want)
		pgAttributeCheck(t, p, b, b.DB, s)
		if b.PG {
			// Real-mode logical expectations are asserted without inventing a
			// PostgreSQL Reset/Dump API. Actual native dump replay occurs below.
			return
		}
		held := fixture.ViewHold(t, p, b, q)
		oldID := pgAttributeID(t, p, b.DB, "pa18_control")
		parameterQuery := "SELECT attname,attnum,atttypid,atttypmod,attndims,attnotnull,atthasdef FROM pg_attribute WHERE attrelid=$1 AND attnum>0 AND NOT attisdropped ORDER BY attnum"
		parameterHeld := fixture.ViewHold(t, p, b, parameterQuery)
		parameterStep := s
		parameterStep.Query, parameterStep.Args = parameterQuery, []any{oldID}
		pgAttributeCheck(t, p, b, parameterHeld, parameterStep)
		old, err := p.Native.QueryContext(p.Ctx, "SELECT attndims,attmissingval FROM pg_attribute WHERE attrelid=$1 AND attname='matrix'", oldID)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		types, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		dump := fixture.ViewDump(t, p)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(dump)+"CREATE SEQUENCE pa18_control_bad CACHE 0;")); err == nil {
			t.Fatal("invalid restore succeeded")
		} else {
			want := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
			fixture.AssertDiagnostic(t, err, &want)
		}
		pgAttributeCheck(t, p, b, held, s)
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		pgAttributeCheck(t, p, b, held, s)
		if pgAttributeID(t, p, b.DB, "pa18_control") != oldID {
			t.Fatal("failed restore changed identity")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		pgAttributeRead(t, p, b, b.DB, oldID, "", nil)
		emptyParameterStep := parameterStep
		emptyParameterStep.Rows = nil
		pgAttributeCheck(t, p, b, parameterHeld, emptyParameterStep)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		newID := pgAttributeID(t, p, b.DB, "pa18_control")
		pgAttributeCheck(t, p, b, b.DB, s)
		// The old literal retains its logical relation identity across replacement.
		fenced, err := held.Statement.QueryContext(p.Ctx)
		if fenced != nil {
			fenced.Close()
			t.Fatal("replaced literal handle published rows")
		}
		fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
		pgAttributeCheck(t, p, b, b.DB, fixture.Prepared(s))
		parameterStep.Args = []any{newID}
		pgAttributeCheck(t, p, b, parameterHeld, parameterStep)
		pgAttributeRead(t, p, b, b.DB, newID, " AND attisdropped", nil)
		pgAttributeCheck(t, p, b, b.DB, fixture.Select("SELECT id,matrix FROM pa18_control", []string{"id", "matrix"}, []string{"INT4", "_INT4"}, [][]any{{int64(9), "{{1,2},{3,4}}"}}))
		if !old.Next() {
			t.Fatal("retained missing row", old.Err())
		}
		var rank int64
		var missing any
		if err := old.Scan(&rank, &missing); err != nil {
			t.Fatal(err)
		}
		if rank != 2 || missing != nil || old.Next() || old.Err() != nil {
			t.Fatal("retained catalog capture", rank, missing, old.Err())
		}
		if types[0].DatabaseTypeName() != "INT2" || types[1].DatabaseTypeName() != "ANYARRAY" {
			t.Fatal("retained descriptions", types)
		}
		if p.Postgres != nil {
			// Exercise the actual emitted native SQL on the pinned server, not
			// a replacement hand-written script or a native-only serializer test.
			if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
				t.Fatal("actual dump PG replay", err)
			}
			pg := fixture.Backend{Name: "postgres", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
			pgAttributeCheck(t, p, pg, p.Postgres, s)
			pgAttributeCheck(t, p, pg, p.Postgres, fixture.Select("SELECT id,matrix FROM pa18_control", []string{"id", "matrix"}, []string{"INT4", "_INT4"}, [][]any{{int64(9), "{{1,2},{3,4}}"}}))
			// Leave public clean for the ordinary shared PG phase above.
			pgClassExec(t, p, pg, p.Postgres, "DROP TABLE pa18_control")
		}
	})
}

func TestReferencePGAttributeSmoke(t *testing.T) {
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pa18_smoke(id integer NOT NULL,label varchar(7),matrix integer[][])", "CREATE VIEW pa18_smoke_view AS SELECT id,matrix FROM pa18_smoke", "CREATE INDEX pa18_smoke_idx ON pa18_smoke(id) INCLUDE(label)")
		id := pgAttributeID(t, p, b.DB, "pa18_smoke")
		q := "SELECT * FROM pg_attribute WHERE attrelid=$1 ORDER BY attnum"
		held := fixture.ViewHold(t, p, b, q)
		users := [][]any{pgAttributeRow(id, "id", 23, 4, 1, -1, 0, true, "i", "p", true, 0), pgAttributeRow(id, "label", 1043, -1, 2, 11, 0, false, "i", "x", false, 100), pgAttributeRow(id, "matrix", 1007, -1, 3, -1, 2, false, "i", "x", false, 0)}
		want := append(pgAttributeSystemRows(id), users...)
		pgAttributeRead(t, p, b, b.DB, id, "", want)
		pgAttributeCheck(t, p, b, held, fixture.Select(q, pgAttributeColumns, pgAttributeTypes, want, id))
		view := pgAttributeID(t, p, b.DB, "pa18_smoke_view")
		pgAttributeRead(t, p, b, b.DB, view, "", [][]any{pgAttributeRow(view, "id", 23, 4, 1, -1, 0, true, "i", "p", false, 0), pgAttributeRow(view, "matrix", 1007, -1, 2, -1, 0, false, "i", "x", false, 0)})
		index := pgAttributeID(t, p, b.DB, "pa18_smoke_idx")
		pgAttributeRead(t, p, b, b.DB, index, "", [][]any{pgAttributeRow(index, "id", 23, 4, 1, -1, 0, true, "i", "p", false, 0), pgAttributeRow(index, "label", 1043, -1, 2, 11, 0, false, "i", "x", false, 0)})
		// Remove the dependent index before deleting its INCLUDE column.
		pgClassExec(t, p, b, b.DB, "DROP INDEX pa18_smoke_idx", "ALTER TABLE pa18_smoke DROP label", "ALTER TABLE pa18_smoke ADD note text")
		users[1] = pgAttributeDropped(users[1])
		users = append(users, pgAttributeRow(id, "note", 25, -1, 4, -1, 0, false, "i", "x", false, 100))
		want = append(pgAttributeSystemRows(id), users...)
		pgAttributeRead(t, p, b, b.DB, id, "", want)
		pgAttributeCheck(t, p, b, held, fixture.Select(q, pgAttributeColumns, pgAttributeTypes, want, id))
	})
}

func TestReferencePGAttributeTypedControls(t *testing.T) {
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB,
			"CREATE TYPE pa18_control_type AS(a integer[][])",
			"CREATE TABLE pa18_control_old OF pa18_control_type",
			"ALTER TYPE pa18_control_type ALTER ATTRIBUTE a TYPE integer[][][] CASCADE",
			"CREATE TABLE pa18_control_late OF pa18_control_type",
			"CREATE TABLE pa18_control_attached(a integer[][][])",
			"ALTER TABLE pa18_control_attached OF pa18_control_type")
		q := "SELECT c.relname,a.attndims FROM pg_attribute a JOIN pg_class c ON a.attrelid=c.oid WHERE c.relname IN ('pa18_control_attached','pa18_control_late','pa18_control_old','pa18_control_type') AND a.attname='a' ORDER BY c.relname"
		s := fixture.Select(q, []string{"relname", "attndims"}, []string{"NAME", "INT2"}, [][]any{{"pa18_control_attached", int64(3)}, {"pa18_control_late", int64(0)}, {"pa18_control_old", int64(3)}, {"pa18_control_type", int64(3)}})
		pgAttributeCheck(t, p, b, b.DB, s)
		pgAttributeCheck(t, p, b, b.DB, fixture.Prepared(s))
		held := fixture.ViewHold(t, p, b, q)
		pgAttributeCheck(t, p, b, held, s)
		if b.PG {
			return
		}
		old, err := p.Native.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		dump := fixture.ViewDump(t, p)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		pgAttributeCheck(t, p, b, b.DB, s)
		pgAttributeCheck(t, p, b, held, s)
		for i, want := range []struct {
			name string
			rank int64
		}{{"pa18_control_attached", 3}, {"pa18_control_late", 0}, {"pa18_control_old", 3}, {"pa18_control_type", 3}} {
			if !old.Next() {
				t.Fatal("retained typed rank row", i, old.Err())
			}
			var name string
			var rank int64
			if err := old.Scan(&name, &rank); err != nil {
				t.Fatal(err)
			}
			if name != want.name || rank != want.rank {
				t.Fatal("retained typed rank", i, name, rank, want)
			}
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained typed rank multiplicity", old.Err())
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
				t.Fatal("actual mixed-rank dump PG replay", err)
			}
			pg := fixture.Backend{Name: "postgres", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
			pgAttributeCheck(t, p, pg, p.Postgres, s)
			pgClassExec(t, p, pg, p.Postgres, "DROP TABLE pa18_control_attached", "DROP TABLE pa18_control_late", "DROP TABLE pa18_control_old", "DROP TYPE pa18_control_type")
		}
	})
}
