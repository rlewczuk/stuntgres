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

func uuidGenerationSaved(t *testing.T, p *fixture.Pair, b fixture.Backend, q fixture.Executor) [][]any {
	t.Helper()
	rows, err := q.QueryContext(p.Ctx, "SELECT id,a,b,c,d FROM uuid_gen ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 5 || cols[0].DatabaseTypeName() != "INT4" {
		t.Fatalf("stored metadata %#v", cols)
	}
	uuidGenerationColumns(t, cols[1:], b.PG, []string{"a", "b", "c", "d"}, true, nil)
	var saved [][]any
	seen := map[string]bool{}
	for rows.Next() {
		var id int64
		var a, bv, c, d string
		if err := rows.Scan(&id, &a, &bv, &c, &d); err != nil {
			t.Fatal(err)
		}
		for i, v := range []string{a, bv, c, d} {
			version := byte(4)
			if i >= 2 {
				version = 7
			}
			uuidGenerationDecode(t, v, version)
			if seen[v] {
				t.Fatalf("default captured/repeated UUID %s", v)
			}
			seen[v] = true
		}
		saved = append(saved, []any{id, a, bv, c, d})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return saved
}

func uuidGenerationConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE uuid_gen(id integer,a uuid DEFAULT gen_random_uuid(),b uuid DEFAULT uuidv4(),c uuid DEFAULT uuidv7(),d uuid DEFAULT uuidv7(interval '1 day'))", 0))
	insert, err := c.PrepareContext(p.Ctx, "INSERT INTO uuid_gen(id) VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{1, 2} {
		result, err := insert.ExecContext(p.Ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			t.Fatalf("prepared insert count %d %v", n, err)
		}
	}
	insert.Close()
	run(fixture.Exec("INSERT INTO uuid_gen(id) VALUES(3),(4)", 2))
	run(fixture.Exec("ALTER TABLE uuid_gen ALTER COLUMN d SET DEFAULT uuidv7(interval '0')", 0))
	run(fixture.Exec("INSERT INTO uuid_gen(id) VALUES(5)", 1))
	saved := uuidGenerationSaved(t, p, b, c)
	if len(saved) != 5 {
		t.Fatalf("default count %d", len(saved))
	}
	run(fixture.Exec("CREATE TABLE uuid_gen_closed(v uuid DEFAULT coalesce(uuidv7(),uuidv4()))", 0))
	run(fixture.Exec("INSERT INTO uuid_gen_closed DEFAULT VALUES", 1))
	closedFirst := uuidGenerationReadWithDeclaration(t, p, b, c, "SELECT v FROM uuid_gen_closed", true)
	uuidGenerationDecode(t, *closedFirst, 7)
	run(fixture.Exec("INSERT INTO uuid_gen_closed DEFAULT VALUES", 1))
	run(fixture.Select("SELECT count(DISTINCT v) AS n FROM uuid_gen_closed", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
	run(fixture.Exec("CREATE TABLE uuid_gen_shift(id integer,shift interval,CHECK(uuidv7(shift) IS NOT NULL))", 0))
	run(fixture.Exec("INSERT INTO uuid_gen_shift VALUES(1,interval '0'),(2,interval '1 day')", 2))
	run(fixture.Exec("CREATE VIEW uuid_gen_view AS SELECT id,uuidv7(shift) AS v FROM uuid_gen_shift", 0))
	run(fixture.Exec("CREATE VIEW uuid_gen_literal AS SELECT uuidv7(interval '1 day') AS v", 0))
	literalValue := uuidGenerationRead(t, p, b, c, "SELECT v FROM uuid_gen_literal")
	uuidGenerationDecode(t, *literalValue, 7)
	view, err := c.PrepareContext(p.Ctx, "SELECT v FROM uuid_gen_view WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	held := fixture.HeldStatement{Executor: c, Statement: view, SQL: "SELECT v FROM uuid_gen_view WHERE id=1"}
	first := uuidGenerationRead(t, p, b, held, held.SQL)
	second := uuidGenerationRead(t, p, b, held, held.SQL)
	if *first == *second {
		t.Fatal("view/provider frozen at Prepare")
	}
	uuidGenerationDecode(t, *first, 7)
	uuidGenerationDecode(t, *second, 7)
	run(fixture.Exec("ALTER TABLE uuid_gen_shift RENAME COLUMN shift TO delta", 0))
	v := uuidGenerationRead(t, p, b, held, held.SQL)
	uuidGenerationDecode(t, *v, 7)
	view.Close()
	run(fixture.Select("SELECT count(uuidv7(delta)) AS n FROM uuid_gen_shift", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
	run(fixture.Select("SELECT delta,count(uuidv7(delta)) AS n FROM uuid_gen_shift GROUP BY delta ORDER BY delta", []string{"delta", "n"}, []string{"INTERVAL", "INT8"}, [][]any{{"00:00:00", int64(1)}, {"1 day", int64(1)}}))
	run(fixture.Select("SELECT a.id FROM uuid_gen_shift a JOIN uuid_gen_shift b ON a.id=b.id AND uuidv7(a.delta) IS NOT NULL ORDER BY a.id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	update := fixture.Select("UPDATE uuid_gen_shift SET delta=$1 WHERE id=1 RETURNING uuidv7(delta) IS NOT NULL AS ok", []string{"ok"}, []string{"BOOL"}, [][]any{{true}}, "0")
	run(fixture.Prepared(update))
	run(fixture.Select("SELECT CASE WHEN false THEN uuidv7(interval '2147483647 months') ELSE NULL::uuid END AS v,coalesce('"+uuidA+"'::uuid,uuidv7(interval '2147483647 months')) AS w", []string{"v", "w"}, []string{"UUID", "UUID"}, [][]any{{nil, uuidA}}))
	for _, q := range []string{"CREATE TABLE uuid_gen_bad(id integer,v uuid GENERATED ALWAYS AS (uuidv7()) STORED)", "CREATE INDEX uuid_gen_bad_index ON uuid_gen ((uuidv7()))"} {
		_, err := c.ExecContext(p.Ctx, q)
		message := "generation expression is not immutable"
		if strings.HasPrefix(q, "CREATE INDEX") {
			message = "functions in index expression must be marked IMMUTABLE"
		}
		uuidGenerationError(t, err, uuidGenerationDiagnostic("42P17", message, ""))
	}
	run(fixture.Exec("CREATE TABLE uuid_gen_atomic(id integer,v uuid)", 0))
	run(fixture.Exec("INSERT INTO uuid_gen_atomic VALUES(99,'"+uuidA+"')", 1))
	_, err = c.ExecContext(p.Ctx, "INSERT INTO uuid_gen_atomic VALUES(1,uuidv7(interval '0')),(2,uuidv7(interval '2147483647 months'))")
	uuidGenerationError(t, err, uuidGenerationDiagnostic("22008", "timestamp out of range", ""))
	run(fixture.Select("SELECT id,v FROM uuid_gen_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "UUID"}, [][]any{{int64(99), uuidA}}))
	_, err = c.ExecContext(p.Ctx, "SELECT CASE WHEN true THEN uuidv7(interval '2147483647 months') ELSE NULL::uuid END")
	uuidGenerationError(t, err, uuidGenerationDiagnostic("22008", "timestamp out of range", ""))
	_, err = c.ExecContext(p.Ctx, "INSERT INTO uuid_gen_atomic VALUES(1,uuidv7()),(2,uuidv7(interval '-1000 years'))")
	uuidGenerationError(t, err, uuidGenerationDiagnostic("22008", "timestamp out of range for UUID version 7", "UUID version 7 supports timestamps from 1970-01-01 to approximately year 10889."))
	run(fixture.Select("SELECT id,v FROM uuid_gen_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "UUID"}, [][]any{{int64(99), uuidA}}))
	if b.PG {
		return
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if _, err := c.QueryContext(canceled, "SELECT uuidv7()"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation %v", err)
	}
	uuidGenerationControls(t, p, b, c, saved)
}

func uuidGenerationControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, saved [][]any) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"pg_catalog.gen_random_uuid(", "pg_catalog.uuidv4(", "pg_catalog.uuidv7("} {
		if !strings.Contains(dump.String(), fragment) {
			t.Fatalf("missing symbolic definition %q in %s", fragment, dump.String())
		}
	}
	for _, row := range saved {
		for _, v := range row[1:] {
			if !strings.Contains(dump.String(), v.(string)) {
				t.Fatalf("saved UUID missing from dump %s", v)
			}
		}
	}
	retained, err := p.Native.QueryContext(p.Ctx, "SELECT id,a,b,c,d FROM uuid_gen ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	cols, err := retained.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	uuidGenerationColumns(t, cols[1:], false, []string{"a", "b", "c", "d"}, true, nil)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for i, row := range saved {
		if !retained.Next() {
			t.Fatalf("retained row %d: %v", i, retained.Err())
		}
		var id int64
		var a, bv, cv, d string
		if err := retained.Scan(&id, &a, &bv, &cv, &d); err != nil {
			t.Fatal(err)
		}
		got := []any{id, a, bv, cv, d}
		for j, v := range got {
			if v != row[j] {
				t.Fatalf("retained row %d column %d got %v want %v", i, j, v, row[j])
			}
		}
	}
	if retained.Next() || retained.Err() != nil {
		t.Fatalf("retained tail %v", retained.Err())
	}
	retained.Close()
	want := fixture.Select("SELECT id,a,b,c,d FROM uuid_gen ORDER BY id", []string{"id", "a", "b", "c", "d"}, []string{"INT4", "UUID", "UUID", "UUID", "UUID"}, saved)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, want)
	bad := dump.String() + "\nINSERT INTO uuid_gen_atomic VALUES(1,uuidv7(interval '2147483647 months'));"
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
	uuidGenerationError(t, err, uuidGenerationDiagnostic("22008", "timestamp out of range", ""))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, want)
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled restore %v", err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, want)
	ioFailure := errors.New("UUID generation restore reader failure")
	if err := stuntgres.Restore(p.Ctx, p.Native, fixture.UUIDReferenceFailingReader{ioFailure}); !errors.Is(err, ioFailure) {
		t.Fatalf("restore I/O error %v", err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, want)
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatalf("actual emitted SQL replay %v\n%s", err, dump.String())
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
		result, err := p.Postgres.ExecContext(p.Ctx, "INSERT INTO uuid_gen(id) VALUES(6)")
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := result.RowsAffected(); n != 1 {
			t.Fatalf("replayed default affected %d", n)
		}
		all := uuidGenerationSaved(t, p, fixture.Backend{PG: true}, p.Postgres)
		if len(all) != 6 {
			t.Fatal("replayed default row count")
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO uuid_gen(id) VALUES(6)", 1))
	all := uuidGenerationSaved(t, p, b, c)
	if len(all) != 6 {
		t.Fatal("restored default row count")
	}
	v := uuidGenerationRead(t, p, b, c, "SELECT v FROM uuid_gen_view WHERE id=1")
	uuidGenerationDecode(t, *v, 7)
}

func TestReferenceUUIDGenerationStoredConsumersAndControls(t *testing.T) {
	uuidGenerationScenario(t, uuidGenerationConsumers)
}
func TestReferenceUUIDGenerationFeatureSmoke(t *testing.T) {
	uuidGenerationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		uuidGenerationBasic(t, p, b, c)
		uuidGenerationShifts(t, p, b, c)
		uuidGenerationGuards(t, p, b, c)
		uuidGenerationConsumers(t, p, b, c)
		uuidGenerationConstantNullConsumers(t, p, b, c)
	})
}
