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

func stringNumericBaseStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE snb_saved(id integer,i integer,j bigint,s text,b4 text DEFAULT to_bin('-1'::integer),b8 text DEFAULT to_bin('-1'::bigint),o4 text DEFAULT to_oct('-1'::integer),o8 text DEFAULT to_oct('-1'::bigint),h4 text DEFAULT to_hex('-1'::integer),h8 text DEFAULT to_hex('-1'::bigint),d text DEFAULT md5('abc'),g text GENERATED ALWAYS AS (md5(s)) STORED,CONSTRAINT snb_saved_check CHECK(to_hex(i)='ffffffff'))", 0))
	run(fixture.Exec("CREATE INDEX snb_hex_index ON snb_saved ((to_hex(j)))", 0))
	run(fixture.Exec("CREATE INDEX snb_md5_index ON snb_saved ((md5(s)))", 0))
	run(fixture.Exec("CREATE VIEW snb_saved_view AS SELECT to_bin(i) AS b4,to_bin(j) AS b8,to_oct(i) AS o4,to_oct(j) AS o8,to_hex(i) AS h4,to_hex(j) AS h8,md5(s) AS d FROM snb_saved WHERE id=1", 0))
	run(fixture.Exec("INSERT INTO snb_saved(id,i,j,s) VALUES(1,-1,-1,'abc')", 1))
	names := []string{"b4", "b8", "o4", "o8", "h4", "h8", "d"}
	types := []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}
	want := []any{"11111111111111111111111111111111", "1111111111111111111111111111111111111111111111111111111111111111", "37777777777", "1777777777777777777777", "ffffffff", "ffffffffffffffff", "900150983cd24fb0d6963f7d28e17f72"}
	assertSaved := func() {
		t.Helper()
		stringNumericBaseBoth(t, p, b, c, fixture.Select("SELECT b4,b8,o4,o8,h4,h8,d FROM snb_saved WHERE id=1", names, types, [][]any{want}))
		stringNumericBaseBoth(t, p, b, c, fixture.Select("SELECT * FROM snb_saved_view", names, types, [][]any{want}))
		stringNumericBaseBoth(t, p, b, c, fixture.Select("SELECT g FROM snb_saved WHERE id=1", []string{"g"}, []string{"TEXT"}, [][]any{{"900150983cd24fb0d6963f7d28e17f72"}}))
	}
	assertSaved()
	if b.PG {
		return // No invented PostgreSQL Reset/Dump/Restore API.
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	q := "SELECT to_hex(i),to_hex(j),md5(s) FROM snb_saved"
	rows, err := p.Native.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ct, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	stringNumericBaseColumns(t, ct, []string{"to_hex", "to_hex", "md5"})
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("missing retained result", rows.Err())
	}
	var h4, h8, digest string
	if err := rows.Scan(&h4, &h8, &digest); err != nil {
		t.Fatal(err)
	}
	if h4 != "ffffffff" || h8 != "ffffffffffffffff" || digest != "900150983cd24fb0d6963f7d28e17f72" || rows.Next() || rows.Err() != nil {
		t.Fatalf("retained row: %s %s %s error=%v", h4, h8, digest, rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	assertSaved()
	// Execute restored symbolic defaults and generated expressions, rather than
	// proving only that already materialized cells survived replacement.
	run(fixture.Exec("INSERT INTO snb_saved(id,i,j,s) VALUES(2,-1,-1,'abc')", 1))
	run(fixture.Select("SELECT h4,h8,d,g FROM snb_saved WHERE id=2", []string{"h4", "h8", "d", "g"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"ffffffff", "ffffffffffffffff", "900150983cd24fb0d6963f7d28e17f72", "900150983cd24fb0d6963f7d28e17f72"}}))
	bad := "CREATE TABLE snb_replacement(d text); INSERT INTO snb_replacement VALUES(to_hex(NULL));"
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
	// The first statement plus space is 38 characters; the INSERT prefix
	// through VALUES( is 35. The function starts at character 38+35+1.
	d := stringNumericBaseDiagnostic("42725", "function to_hex(unknown) is not unique", stringNumericBaseAmbiguousHint, "74")
	fixture.AssertDiagnostic(t, err, &d)
	assertSaved()
	run(fixture.Select("SELECT count(*) FROM snb_saved", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
	stringNumericBaseColumns(t, ct, []string{"to_hex", "to_hex", "md5"})
	stringNumericBaseBoth(t, p, b, c, fixture.ErrorDiagnostic("SELECT d FROM snb_replacement", stringNumericBaseDiagnostic("42P01", `relation "snb_replacement" does not exist`, "", "15")))
	if p.Postgres != nil {
		// Replay the actual native emitted SQL, never a handwritten substitute.
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatalf("actual emitted SQL replay: %v\n%s", err, dump.String())
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT * FROM snb_saved_view", names, types, [][]any{want}))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO snb_saved(id,i,j,s) VALUES(2,-1,-1,'abc')", 1))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT h4,h8,d,g FROM snb_saved WHERE id=2", []string{"h4", "h8", "d", "g"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"ffffffff", "ffffffffffffffff", "900150983cd24fb0d6963f7d28e17f72", "900150983cd24fb0d6963f7d28e17f72"}}))
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceStringNumericBaseControls(t *testing.T) {
	stringNumericBaseScenario(t, stringNumericBaseStored)
}

func TestReferenceStringNumericBaseCancellation(t *testing.T) {
	stringNumericBaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// DB-owned statement/pool acquisition avoids the borrowed-Conn watcher race
		// for a tiny PostgreSQL query. No sleeps or timing-based midhash claim.
		q := "SELECT to_hex($1::integer),md5($2)"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			if prepared {
				rows, err = stmt.QueryContext(ctx, int64(42), "abc")
			} else {
				rows, err = b.DB.QueryContext(ctx, q, int64(42), "abc")
			}
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("prepared=%v cancellation: %v", prepared, err)
			}
			var sqlError *stuntgres.Error
			if errors.As(err, &sqlError) && sqlError.Position != "" {
				t.Fatal("fabricated cancellation position", sqlError.Position)
			}
		}
		s := stringNumericBaseText(q, []string{"to_hex", "md5"}, [][]any{{"2a", "900150983cd24fb0d6963f7d28e17f72"}}, int64(42), "abc")
		stringNumericBaseBoth(t, p, b, b.DB, s)
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	})
}
