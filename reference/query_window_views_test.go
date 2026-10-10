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

func queryWindowView(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	queryWindowRun(t, p, b, referenceExec("CREATE VIEW window_v AS SELECT id,row_number() OVER ordered AS rn,count(*) OVER(base ORDER BY k) AS n FROM t WINDOW base AS(PARTITION BY p),ordered AS(base ORDER BY k,id)", 0))
	queryWindowRoutes(t, p, b, queryWindowResult("SELECT id,rn,n FROM window_v ORDER BY id"))
}

func queryWindowRestore(t *testing.T, p *referencePair, extra ...referenceStep) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	fresh := newReferenceFixture(t, false, true)
	if err := stuntgres.Restore(fresh.ctx, fresh.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, prepared := range []bool{false, true} {
		s := queryWindowResult("SELECT id,rn,n FROM window_v ORDER BY id")
		s.prepared = prepared
		fresh.step(t, s)
	}
	held, err := p.memory.QueryContext(p.ctx, "SELECT id,rn,n FROM window_v ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, s := range extra {
		for _, prepared := range []bool{false, true} {
			s.prepared = prepared
			fresh.step(t, s)
		}
	}
	q := "SELECT count(*) OVER() AS n FROM t ORDER BY id LIMIT 1"
	stmt, err := p.memory.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	heldStmt := referenceHeldStatement{p.memory, stmt, q}
	referenceRunStep(t, p.ctx, heldStmt, p.memoryRecord, referenceSelect(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(7)}}))
	if _, err := p.memory.ExecContext(p.ctx, "INSERT INTO t VALUES(8,'a',30)"); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT count(*) OVER() AS n FROM t ORDER BY id LIMIT 1", []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}}))
	referenceRunStep(t, p.ctx, heldStmt, p.memoryRecord, referenceSelect(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}}))
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	queryWindowHeldRows(t, held, queryWindowResult("").rows)
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, queryWindowResult("SELECT id,rn,n FROM window_v ORDER BY id"))
	fresh.step(t, referenceExec("CREATE TABLE sentinel(v INT)", 0))
	fresh.step(t, referenceExec("INSERT INTO sentinel VALUES(91)", 1))
	// The valid selected view definition is staged before an invalid trailing
	// statement; no bytes in the pristine successful snapshot are rewritten.
	referenceRunStep(t, p.ctx, heldStmt, p.memoryRecord, referenceSelect(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(7)}}))
	bad := append(append([]byte{}, dump.Bytes()...), []byte("\nSELECT broken_restore;\n")...)
	if err := stuntgres.Restore(fresh.ctx, fresh.native, bytes.NewReader(bad)); err == nil {
		t.Fatal("invalid restore succeeded")
	}
	fresh.step(t, referenceSelect("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
	ctx, cancel := context.WithCancel(fresh.ctx)
	cancel()
	if err := stuntgres.Restore(ctx, fresh.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	fresh.step(t, referenceSelect("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
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
	if _, ok := referenceCompareRows(got, want, false); !ok {
		t.Fatal(got, want)
	}
}

func TestReferenceQueryWindowViews(t *testing.T) {
	queryWindowScenario(t, func(p *referencePair, b referenceBackend) {
		queryWindowSeed(t, p, b)
		queryWindowView(t, p, b)
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE c(id INT,k INT)", 0), referenceExec("INSERT INTO c VALUES(1,2),(2,1)", 2),
			referenceExec("CREATE VIEW collision(k,id) AS SELECT row_number() OVER(ORDER BY k),id FROM c", 0),
			referenceExec("CREATE TABLE a(id INT,k VARCHAR(2))", 0), referenceExec("CREATE TABLE other(k VARCHAR(3))", 0),
			referenceExec("INSERT INTO a VALUES(1,'b'),(2,'a')", 2), referenceExec("INSERT INTO other VALUES('a'),('b')", 2),
			referenceExec("CREATE VIEW using_collision(k,id) AS SELECT row_number() OVER(ORDER BY k),__view_using_0.id FROM a AS __view_using_0 JOIN other USING(k)", 0),
			referenceExec("CREATE VIEW merged_usage AS SELECT k FROM a JOIN other USING(k)", 0),
			referenceExec("CREATE VIEW literal_window AS SELECT id,count(*) OVER(PARTITION BY 't'::REGCLASS,1.00::NUMERIC ORDER BY k) AS n FROM t", 0),
			referenceExec("ALTER TABLE t RENAME COLUMN k TO renamed", 0),
		} {
			queryWindowRun(t, p, b, s)
		}
		for _, name := range []string{"window_v", "collision", "using_collision", "literal_window"} {
			var definition string
			if err := b.db.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='"+name+"'").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			queryWindowRun(t, p, b, referenceExec("CREATE VIEW replay_"+name+" AS "+definition, 0))
		}
		for _, prefix := range []string{"", "replay_"} {
			queryWindowRoutes(t, p, b, queryWindowResult("SELECT id,rn,n FROM "+prefix+"window_v ORDER BY id"))
			for _, name := range []string{"collision", "using_collision"} {
				queryWindowRoutes(t, p, b, referenceSelect("SELECT k,id FROM "+prefix+name+" ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}))
			}
			queryWindowRoutes(t, p, b, referenceSelect("SELECT id,n FROM "+prefix+"literal_window ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT8"}, queryWindowValues(5, 5, 6, 3, 7, 2, 2)))
		}
		queryWindowRoutes(t, p, b, referenceSelect("SELECT k FROM merged_usage ORDER BY k", []string{"k"}, []string{"VARCHAR"}, [][]any{{"a"}, {"b"}}))
		if !b.pg {
			queryWindowRestore(t, p,
				referenceSelect("SELECT k,id FROM collision ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}),
				referenceSelect("SELECT k,id FROM using_collision ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}),
				referenceSelect("SELECT id,n FROM literal_window ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT8"}, queryWindowValues(5, 5, 6, 3, 7, 2, 2)),
			)
		}
	})
}

func TestReferenceQueryWindowFeatureSmoke(t *testing.T) {
	queryWindowScenario(t, func(p *referencePair, b referenceBackend) {
		queryWindowSeed(t, p, b)
		queryWindowRoutes(t, p, b, queryWindowResult("SELECT id,row_number() OVER ordered AS rn,count(*) OVER(base ORDER BY k) AS n FROM t WINDOW base AS(PARTITION BY p),ordered AS(base ORDER BY k,id) ORDER BY id"))
		queryWindowRoutes(t, p, b, referenceSelect("SELECT row_number() OVER(),count(*) OVER()", []string{"row_number", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}))
		queryWindowRoutes(t, p, b, referenceSelect("SELECT row_number() OVER(),count(*) OVER() FROM empty_t", []string{"row_number", "count"}, []string{"INT8", "INT8"}, nil))
		queryWindowRoutes(t, p, b, referenceSelect("SELECT id FROM t ORDER BY row_number() OVER(ORDER BY id DESC)", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(6)}, {int64(5)}, {int64(4)}, {int64(3)}, {int64(2)}, {int64(1)}}))
		for _, tc := range []struct{ q, code, message, marker string }{
			{"SELECT count(row_number() OVER ()) OVER () FROM t", "42P20", "window function calls cannot be nested", "row_number"},
			{"SELECT row_number() OVER missing FROM t", "42704", `window "missing" does not exist`, "missing"},
			{"SELECT id FROM t WHERE row_number() OVER ()>0", "42P20", "window functions are not allowed in WHERE", "row_number"},
		} {
			s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: referencePosition(strings.Index(tc.q, tc.marker) + 1)})
			s.selectRows = true
			queryWindowRoutes(t, p, b, s)
		}
		queryWindowView(t, p, b)
		if !b.pg {
			queryWindowRestore(t, p)
		}
	})
}
