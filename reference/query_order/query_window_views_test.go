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

func queryWindowView(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowRun(t, p, b, fixture.Exec("CREATE VIEW window_v AS SELECT id,row_number() OVER ordered AS rn,count(*) OVER(base ORDER BY k) AS n FROM t WINDOW base AS(PARTITION BY p),ordered AS(base ORDER BY k,id)", 0))
	queryWindowRoutes(t, p, b, queryWindowResult("SELECT id,rn,n FROM window_v ORDER BY id"))
}

func queryWindowRestore(t *testing.T, p *fixture.Pair, extra ...fixture.Step) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	fresh := fixture.NewFixture(t, false, true)
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, prepared := range []bool{false, true} {
		s := queryWindowResult("SELECT id,rn,n FROM window_v ORDER BY id")
		s.Prepared = prepared
		fresh.Step(t, s)
	}
	held, err := p.Memory.QueryContext(p.Ctx, "SELECT id,rn,n FROM window_v ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, s := range extra {
		for _, prepared := range []bool{false, true} {
			s.Prepared = prepared
			fresh.Step(t, s)
		}
	}
	q := "SELECT count(*) OVER() AS n FROM t ORDER BY id LIMIT 1"
	stmt, err := p.Memory.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	heldStmt := fixture.HeldStatement{p.Memory, stmt, q}
	fixture.RunStep(t, p.Ctx, heldStmt, p.MemoryRecord, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(7)}}))
	if _, err := p.Memory.ExecContext(p.Ctx, "INSERT INTO t VALUES(8,'a',30)"); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT count(*) OVER() AS n FROM t ORDER BY id LIMIT 1", []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}}))
	fixture.RunStep(t, p.Ctx, heldStmt, p.MemoryRecord, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}}))
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	queryWindowHeldRows(t, held, queryWindowResult("").Rows)
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, queryWindowResult("SELECT id,rn,n FROM window_v ORDER BY id"))
	fresh.Step(t, fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
	fresh.Step(t, fixture.Exec("INSERT INTO sentinel VALUES(91)", 1))
	// The valid selected view definition is staged before an invalid trailing
	// statement; no bytes in the pristine successful snapshot are rewritten.
	fixture.RunStep(t, p.Ctx, heldStmt, p.MemoryRecord, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(7)}}))
	bad := append(append([]byte{}, dump.Bytes()...), []byte("\nSELECT broken_restore;\n")...)
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(bad)); err == nil {
		t.Fatal("invalid restore succeeded")
	}
	fresh.Step(t, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
	ctx, cancel := context.WithCancel(fresh.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, fresh.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	fresh.Step(t, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
}

func queryWindowHeldRows(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || len(names) != 3 || names[0] != "id" || names[1] != "rn" || names[2] != "n" {
		t.Fatal(names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		t.Fatal("retained metadata", types, err)
	}
	for i, want := range []string{"INT4", "INT8", "INT8"} {
		if types[i].DatabaseTypeName() != want || types[i].Name() != names[i] {
			t.Fatal("retained metadata", i, types[i].Name(), types[i].DatabaseTypeName(), want)
		}
	}
	var got [][]any
	for rows.Next() {
		var id, rn, n int64
		if err := rows.Scan(&id, &rn, &n); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{id, rn, n})
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if _, ok := fixture.CompareRows(got, want, false); !ok {
		t.Fatal(got, want)
	}
}

func TestReferenceQueryWindowViews(t *testing.T) {
	queryWindowScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowSeed(t, p, b)
		queryWindowView(t, p, b)
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE c(id INT,k INT)", 0), fixture.Exec("INSERT INTO c VALUES(1,2),(2,1)", 2),
			fixture.Exec("CREATE VIEW collision(k,id) AS SELECT row_number() OVER(ORDER BY k),id FROM c", 0),
			fixture.Exec("CREATE TABLE a(id INT,k VARCHAR(2))", 0), fixture.Exec("CREATE TABLE other(k VARCHAR(3))", 0),
			fixture.Exec("INSERT INTO a VALUES(1,'b'),(2,'a')", 2), fixture.Exec("INSERT INTO other VALUES('a'),('b')", 2),
			fixture.Exec("CREATE VIEW using_collision(k,id) AS SELECT row_number() OVER(ORDER BY k),__view_using_0.id FROM a AS __view_using_0 JOIN other USING(k)", 0),
			fixture.Exec("CREATE VIEW merged_usage AS SELECT k FROM a JOIN other USING(k)", 0),
			fixture.Exec("CREATE VIEW literal_window AS SELECT id,count(*) OVER(PARTITION BY 't'::REGCLASS,1.00::NUMERIC ORDER BY k) AS n FROM t", 0),
			fixture.Exec("ALTER TABLE t RENAME COLUMN k TO renamed", 0),
		} {
			queryWindowRun(t, p, b, s)
		}
		for _, name := range []string{"window_v", "collision", "using_collision", "literal_window"} {
			var definition string
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='"+name+"'").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			queryWindowRun(t, p, b, fixture.Exec("CREATE VIEW replay_"+name+" AS "+definition, 0))
		}
		for _, prefix := range []string{"", "replay_"} {
			queryWindowRoutes(t, p, b, queryWindowResult("SELECT id,rn,n FROM "+prefix+"window_v ORDER BY id"))
			for _, name := range []string{"collision", "using_collision"} {
				queryWindowRoutes(t, p, b, fixture.Select("SELECT k,id FROM "+prefix+name+" ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}))
			}
			queryWindowRoutes(t, p, b, fixture.Select("SELECT id,n FROM "+prefix+"literal_window ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT8"}, queryWindowValues(5, 5, 6, 3, 7, 2, 2)))
		}
		queryWindowRoutes(t, p, b, fixture.Select("SELECT k FROM merged_usage ORDER BY k", []string{"k"}, []string{"VARCHAR"}, [][]any{{"a"}, {"b"}}))
		if !b.PG {
			queryWindowRestore(t, p,
				fixture.Select("SELECT k,id FROM collision ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}),
				fixture.Select("SELECT k,id FROM using_collision ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}),
				fixture.Select("SELECT id,n FROM literal_window ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT8"}, queryWindowValues(5, 5, 6, 3, 7, 2, 2)),
			)
		}
	})
}

func TestReferenceQueryWindowFeatureSmoke(t *testing.T) {
	queryWindowScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowSeed(t, p, b)
		queryWindowRoutes(t, p, b, queryWindowResult("SELECT id,row_number() OVER ordered AS rn,count(*) OVER(base ORDER BY k) AS n FROM t WINDOW base AS(PARTITION BY p),ordered AS(base ORDER BY k,id) ORDER BY id"))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT row_number() OVER(),count(*) OVER()", []string{"row_number", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT row_number() OVER(),count(*) OVER() FROM empty_t", []string{"row_number", "count"}, []string{"INT8", "INT8"}, nil))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id FROM t ORDER BY row_number() OVER(ORDER BY id DESC)", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(6)}, {int64(5)}, {int64(4)}, {int64(3)}, {int64(2)}, {int64(1)}}))
		for _, tc := range []struct{ q, code, message, marker string }{
			{"SELECT count(row_number() OVER ()) OVER () FROM t", "42P20", "window function calls cannot be nested", "row_number"},
			{"SELECT row_number() OVER missing FROM t", "42704", `window "missing" does not exist`, "missing"},
			{"SELECT id FROM t WHERE row_number() OVER ()>0", "42P20", "window functions are not allowed in WHERE", "row_number"},
		} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.Position(strings.Index(tc.q, tc.marker) + 1)})
			s.SelectRows = true
			queryWindowRoutes(t, p, b, s)
		}
		queryWindowView(t, p, b)
		if !b.PG {
			queryWindowRestore(t, p)
		}
	})
}
