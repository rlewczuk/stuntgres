package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// PostgreSQL plans a strict call with a constant NULL child to typed UUID NULL
// before generated/index immutability checks, despite the provider's volatility.
func TestReferenceUUIDGenerationConstantNullConsumers(t *testing.T) {
	uuidGenerationScenario(t, uuidGenerationConstantNullConsumers)
}

func uuidGenerationConstantNullConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for i, child := range []string{"NULL", "NULL::interval", "CASE WHEN true THEN NULL::interval ELSE interval '0' END", "coalesce(NULL::interval,NULL::interval)"} {
		table := "uuid_gen_null_" + strconv.Itoa(i)
		expression := "uuidv7(" + child + ")"
		run(fixture.Exec("CREATE TABLE "+table+"(id integer,v uuid GENERATED ALWAYS AS ("+expression+") STORED)", 0))
		run(fixture.Exec("CREATE INDEX "+table+"_index ON "+table+" (("+expression+"))", 0))
		run(fixture.Exec("INSERT INTO "+table+"(id) VALUES(1),(2)", 2))
		run(fixture.Select("SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "UUID"}, [][]any{{int64(1), nil}, {int64(2), nil}}))
		run(fixture.Prepared(fixture.Select("UPDATE "+table+" SET id=3 WHERE id=1 RETURNING v", []string{"v"}, []string{"UUID"}, [][]any{{nil}})))
		run(fixture.Select("SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "UUID"}, [][]any{{int64(2), nil}, {int64(3), nil}}))
	}
	for _, expression := range []string{"uuidv7()", "uuidv7(interval '0')"} {
		_, err := c.ExecContext(p.Ctx, "CREATE TABLE uuid_gen_nonnull_bad(id integer,v uuid GENERATED ALWAYS AS ("+expression+") STORED)")
		uuidGenerationError(t, err, uuidGenerationDiagnostic("42P17", "generation expression is not immutable", ""))
		_, err = c.ExecContext(p.Ctx, "CREATE INDEX uuid_gen_nonnull_bad_index ON uuid_gen_null_0 (("+expression+"))")
		uuidGenerationError(t, err, uuidGenerationDiagnostic("42P17", "functions in index expression must be marked IMMUTABLE", ""))
	}
	if b.PG {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "pg_catalog.uuidv7(") || !strings.Contains(dump.String(), "GENERATED ALWAYS AS") || !strings.Contains(dump.String(), "CREATE INDEX") {
		t.Fatalf("strict NULL planning erased symbolic generated/index definitions:\n%s", dump.String())
	}
	retained, err := p.Native.QueryContext(p.Ctx, "SELECT id,v FROM uuid_gen_null_0 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	metadata, err := retained.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	uuidGenerationColumns(t, metadata[1:], false, []string{"v"}, true, nil)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{2, 3} {
		if !retained.Next() {
			t.Fatalf("retained typed NULL row: %v", retained.Err())
		}
		var got int64
		var value any
		if err := retained.Scan(&got, &value); err != nil || got != id || value != nil {
			t.Fatalf("retained got %d/%v want %d/NULL: %v", got, value, id, err)
		}
	}
	if retained.Next() || retained.Err() != nil {
		t.Fatalf("retained tail %v", retained.Err())
	}
	retained.Close()
	// Repeated Restore proves selected interval inference remains usable without
	// pinning incidental printer wording or wrapper counts.
	for range 2 {
		var replay bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &replay); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(replay.Bytes())); err != nil {
			t.Fatal(err)
		}
		for i := range 4 {
			table := "uuid_gen_null_" + strconv.Itoa(i)
			run(fixture.Select("SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "UUID"}, [][]any{{int64(2), nil}, {int64(3), nil}}))
			run(fixture.Exec("INSERT INTO "+table+"(id) VALUES(4)", 1))
			run(fixture.Select("SELECT v FROM "+table+" WHERE id=4", []string{"v"}, []string{"UUID"}, [][]any{{nil}}))
			run(fixture.Exec("DELETE FROM "+table+" WHERE id=4", 1))
		}
	}
	for i := range 4 {
		table := "uuid_gen_null_" + strconv.Itoa(i)
		want := fixture.Select("SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "UUID"}, [][]any{{int64(2), nil}, {int64(3), nil}})
		run(want)
		run(fixture.Exec("INSERT INTO "+table+"(id) VALUES(4)", 1))
		run(fixture.Select("SELECT v FROM "+table+" WHERE id=4", []string{"v"}, []string{"UUID"}, [][]any{{nil}}))
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatalf("actual NULL-definition SQL replay: %v\n%s", err, dump.String())
		}
		for i := range 4 {
			table := "uuid_gen_null_" + strconv.Itoa(i)
			want := fixture.Select("SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "UUID"}, [][]any{{int64(2), nil}, {int64(3), nil}})
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO "+table+"(id) VALUES(4)", 1))
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT v FROM "+table+" WHERE id=4", []string{"v"}, []string{"UUID"}, [][]any{{nil}}))
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}
