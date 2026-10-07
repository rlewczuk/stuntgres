//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func queryJoinsReferenceSetup(t *testing.T) *referencePair {
	p := newReferencePair(t)
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE a(l TEXT,k INT,q INT)", 0),
		referenceExec("CREATE TABLE b(r TEXT,q INT,k INT)", 0),
		referenceExec("CREATE TABLE c(k INT,c TEXT)", 0),
		referenceExec("CREATE TABLE empty(r TEXT,q INT,k INT)", 0),
		referenceExec("CREATE TABLE wide(k BIGINT)", 0),
		referenceExec("INSERT INTO a VALUES('a',1,9),('a',1,9),('n',NULL,9),('z',2,8)", 4),
		referenceExec("INSERT INTO b VALUES('x',9,1),('x',9,1),('y',8,3),('n',9,NULL)", 4),
		referenceExec("INSERT INTO c VALUES(1,'c'),(1,'c')", 2),
		referenceExec("INSERT INTO wide VALUES(1)", 1),
	} {
		// Setup must run regardless of a backend-only -run subtest filter.
		postgres := referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		memory := referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s)
		if !reflect.DeepEqual(memory, postgres) {
			t.Fatalf("setup parity got %#v want %#v", memory, postgres)
		}
	}
	return p
}

func TestReferenceQueryJoinsRegression(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	product := [][]any{
		{"a", int64(1), int64(9), "x", int64(9), int64(1)},
		{"a", int64(1), int64(9), "x", int64(9), int64(1)},
		{"a", int64(1), int64(9), "y", int64(8), int64(3)},
		{"a", int64(1), int64(9), "n", int64(9), nil},
		{"a", int64(1), int64(9), "x", int64(9), int64(1)},
		{"a", int64(1), int64(9), "x", int64(9), int64(1)},
		{"a", int64(1), int64(9), "y", int64(8), int64(3)},
		{"a", int64(1), int64(9), "n", int64(9), nil},
		{"n", nil, int64(9), "x", int64(9), int64(1)},
		{"n", nil, int64(9), "x", int64(9), int64(1)},
		{"n", nil, int64(9), "y", int64(8), int64(3)},
		{"n", nil, int64(9), "n", int64(9), nil},
		{"z", int64(2), int64(8), "x", int64(9), int64(1)},
		{"z", int64(2), int64(8), "x", int64(9), int64(1)},
		{"z", int64(2), int64(8), "y", int64(8), int64(3)},
		{"z", int64(2), int64(8), "n", int64(9), nil},
	}
	merged := [][]any{{int64(1), "a", int64(9), "x", int64(9)}, {int64(1), "a", int64(9), "x", int64(9)}, {int64(1), "a", int64(9), "x", int64(9)}, {int64(1), "a", int64(9), "x", int64(9)}}
	nested := [][]any{
		{int64(1), "a", int64(9), "x", int64(9), "c"}, {int64(1), "a", int64(9), "x", int64(9), "c"},
		{int64(1), "a", int64(9), "x", int64(9), "c"}, {int64(1), "a", int64(9), "x", int64(9), "c"},
		{int64(1), "a", int64(9), "x", int64(9), "c"}, {int64(1), "a", int64(9), "x", int64(9), "c"},
		{int64(1), "a", int64(9), "x", int64(9), "c"}, {int64(1), "a", int64(9), "x", int64(9), "c"},
	}
	steps := []referenceStep{
		referenceSelect("SELECT * FROM a JOIN b ON a.k=b.k", []string{"l", "k", "q", "r", "q", "k"}, []string{"TEXT", "INT4", "INT4", "TEXT", "INT4", "INT4"}, [][]any{{"a", int64(1), int64(9), "x", int64(9), int64(1)}, {"a", int64(1), int64(9), "x", int64(9), int64(1)}, {"a", int64(1), int64(9), "x", int64(9), int64(1)}, {"a", int64(1), int64(9), "x", int64(9), int64(1)}}),
		referenceSelect("SELECT * FROM a JOIN b USING(k)", []string{"k", "l", "q", "r", "q"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4"}, merged),
		referenceSelect("SELECT * FROM a JOIN b USING(q,k)", []string{"q", "k", "l", "r"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, [][]any{{int64(9), int64(1), "a", "x"}, {int64(9), int64(1), "a", "x"}, {int64(9), int64(1), "a", "x"}, {int64(9), int64(1), "a", "x"}}),
		referenceSelect("SELECT * FROM a NATURAL JOIN b", []string{"k", "q", "l", "r"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, [][]any{{int64(1), int64(9), "a", "x"}, {int64(1), int64(9), "a", "x"}, {int64(1), int64(9), "a", "x"}, {int64(1), int64(9), "a", "x"}}),
		referenceSelect("SELECT u.*,a.k,b.k FROM a JOIN b USING(q,k) AS u", []string{"q", "k", "k", "k"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(9), int64(1), int64(1), int64(1)}, {int64(9), int64(1), int64(1), int64(1)}, {int64(9), int64(1), int64(1), int64(1)}, {int64(9), int64(1), int64(1), int64(1)}}),
		referenceSelect("SELECT a.*,b.k FROM a JOIN b USING(k)", []string{"l", "k", "q", "k"}, []string{"TEXT", "INT4", "INT4", "INT4"}, [][]any{{"a", int64(1), int64(9), int64(1)}, {"a", int64(1), int64(9), int64(1)}, {"a", int64(1), int64(9), int64(1)}, {"a", int64(1), int64(9), int64(1)}}),
		referenceSelect("SELECT * FROM (a JOIN b USING(k)) j(kk,ll)", []string{"kk", "ll", "q", "r", "q"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4"}, merged),
		referenceSelect("SELECT * FROM a JOIN (b JOIN c USING(k)) USING(k)", []string{"k", "l", "q", "r", "q", "c"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "TEXT"}, nested),
		referenceSelect("SELECT * FROM (a JOIN b USING(k)) JOIN c USING(k)", []string{"k", "l", "q", "r", "q", "c"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "TEXT"}, nested),
		referenceSelect("SELECT x.* FROM a AS x(ll,kk) WHERE x.kk=1", []string{"ll", "kk", "q"}, []string{"TEXT", "INT4", "INT4"}, [][]any{{"a", int64(1), int64(9)}, {"a", int64(1), int64(9)}}),
		referenceSelect("SELECT a.k,wide.k,k FROM a JOIN wide USING(k)", []string{"k", "k", "k"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(1), int64(1), int64(1)}}),
		referenceSelect("SELECT wide.k,a.k,k FROM wide JOIN a USING(k)", []string{"k", "k", "k"}, []string{"INT8", "INT4", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(1), int64(1), int64(1)}}),
		referenceSelect("SELECT a.l,b.r FROM a JOIN b ON a.k=$1 AND a.k=b.k", []string{"l", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"a", "x"}, {"a", "x"}, {"a", "x"}}, 1),
		referenceSelect("SELECT a.l,b.r FROM a JOIN b ON a.k=$1 AND a.k=b.k", []string{"l", "r"}, []string{"TEXT", "TEXT"}, nil, nil),
		referenceSelect("SELECT a.l,b.r FROM a JOIN b ON a.k=$1 AND a.k=b.k", []string{"l", "r"}, []string{"TEXT", "TEXT"}, nil, 2),
		referenceSelect("SELECT $1 FROM a JOIN b ON a.k=$1 AND a.k=b.k", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}}, 1),
		referenceSelect("SELECT $1 FROM a JOIN b ON a.k=$1 AND a.k=b.k", []string{"?column?"}, []string{"INT4"}, nil, nil),
		referenceSelect("SELECT $1 FROM a JOIN b ON a.k=$1 AND a.k=b.k", []string{"?column?"}, []string{"INT4"}, nil, 2),
		referenceSelect("SELECT count(*) FROM a JOIN b USING(k)", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}),
		referenceSelect("SELECT k,count(*) FROM a JOIN b USING(k) GROUP BY k HAVING count(*)=4", []string{"k", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(4)}}),
		referenceSelect("SELECT a.l,b.r FROM a JOIN b ON CAST(a.k AS BIGINT)=b.k AND COALESCE(a.q,0)=b.q AND CASE WHEN b.r='x' THEN TRUE ELSE FALSE END", []string{"l", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"a", "x"}, {"a", "x"}, {"a", "x"}}),
		referenceSelect(`SELECT /*é*/ "X"."K",b.k FROM a AS "X"("L","K") JOIN b ON "X"."K"=b.k`, []string{"K", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}, {int64(1), int64(1)}, {int64(1), int64(1)}}),
		referenceSelect("SELECT x.l,y.l FROM a x CROSS JOIN a y", []string{"l", "l"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "a"}, {"a", "a"}, {"a", "n"}, {"a", "z"}, {"a", "a"}, {"a", "a"}, {"a", "n"}, {"a", "z"}, {"n", "a"}, {"n", "a"}, {"n", "n"}, {"n", "z"}, {"z", "a"}, {"z", "a"}, {"z", "n"}, {"z", "z"}}),
		referenceSelect("SELECT a.l FROM a CROSS JOIN b JOIN c ON a.k=c.k AND b.k=c.k", []string{"l"}, []string{"TEXT"}, [][]any{{"a"}, {"a"}, {"a"}, {"a"}, {"a"}, {"a"}, {"a"}, {"a"}}),
		referenceSelect("SELECT * FROM a NATURAL JOIN b AS renamed(rr,qq,kk)", []string{"l", "k", "q", "rr", "qq", "kk"}, []string{"TEXT", "INT4", "INT4", "TEXT", "INT4", "INT4"}, product),
	}
	for _, q := range []string{"SELECT * FROM a,b", "SELECT * FROM a CROSS JOIN b", "SELECT * FROM a INNER JOIN b ON TRUE"} {
		steps = append(steps, referenceSelect(q, []string{"l", "k", "q", "r", "q", "k"}, []string{"TEXT", "INT4", "INT4", "TEXT", "INT4", "INT4"}, product))
	}
	for _, q := range []string{"SELECT * FROM a JOIN b ON FALSE", "SELECT * FROM a JOIN b ON NULL", "SELECT * FROM a CROSS JOIN empty", "SELECT * FROM empty CROSS JOIN a"} {
		names := []string{"l", "k", "q", "r", "q", "k"}
		if q == "SELECT * FROM empty CROSS JOIN a" {
			names = []string{"r", "q", "k", "l", "k", "q"}
		}
		steps = append(steps, referenceSelect(q, names, []string{"TEXT", "INT4", "INT4", "TEXT", "INT4", "INT4"}, nil))
	}
	queryJoinsReferenceCases(t, p, steps)
}

func queryJoinsReferenceCases(t *testing.T, p *referencePair, steps []referenceStep) {
	// PostgreSQL is independently runnable even when the native subtest is red.
	observed := make([]referenceObserved, 2*len(steps))
	for _, backend := range []struct {
		name     string
		db       *sql.DB
		recorder *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			for i, s := range steps {
				s.bag = true
				t.Run(s.query, func(t *testing.T) {
					t.Run("direct", func(t *testing.T) {
						got := referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
						if backend.name == "postgres" {
							observed[2*i] = got
						} else if !reflect.DeepEqual(got, observed[2*i]) {
							t.Fatalf("backend parity got %#v want %#v", got, observed[2*i])
						}
					})
					t.Run("prepared", func(t *testing.T) {
						got := referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
						if backend.name == "postgres" {
							observed[2*i+1] = got
						} else if !reflect.DeepEqual(got, observed[2*i+1]) {
							t.Fatalf("backend parity got %#v want %#v", got, observed[2*i+1])
						}
					})
				})
			}
		})
	}
}

// Independently assert the pinned fixed-shape contract on each selected backend.
func queryJoinsPreparedSchema(t *testing.T, p *referencePair, db *sql.DB) {
	conn, err := db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{"CREATE TABLE shape_a(k INT,q INT)", "CREATE TABLE shape_b(k INT,other INT)", "INSERT INTO shape_a VALUES(1,7)", "INSERT INTO shape_b VALUES(1,8)"} {
		if _, err := conn.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	stable, err := conn.PrepareContext(p.ctx, "SELECT k FROM shape_a NATURAL JOIN shape_b")
	if err != nil {
		t.Fatal(err)
	}
	defer stable.Close()
	changed, err := conn.PrepareContext(p.ctx, "SELECT * FROM shape_a NATURAL JOIN shape_b")
	if err != nil {
		t.Fatal(err)
	}
	defer changed.Close()
	for _, initial := range []struct {
		stmt    *sql.Stmt
		columns []string
		values  []int64
	}{
		{stable, []string{"k"}, []int64{1}},
		{changed, []string{"k", "q", "other"}, []int64{1, 7, 8}},
	} {
		rows, err := initial.stmt.QueryContext(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(columns, initial.columns) {
			rows.Close()
			t.Fatal(columns, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != len(initial.values) {
			rows.Close()
			t.Fatal(types, err)
		}
		for _, typ := range types {
			if typ.DatabaseTypeName() != "INT4" {
				rows.Close()
				t.Fatal(typ)
			}
		}
		values := make([]int64, len(initial.values))
		dest := make([]any, len(values))
		for i := range values {
			dest[i] = &values[i]
		}
		if !rows.Next() {
			rows.Close()
			t.Fatal("initial NATURAL row missing", rows.Err())
		}
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if !reflect.DeepEqual(values, initial.values) || rows.Next() || rows.Err() != nil {
			rows.Close()
			t.Fatal(values, initial.values, rows.Err())
		}
		rows.Close()
	}
	for _, q := range []string{"ALTER TABLE shape_b RENAME other TO q"} {
		if _, err := conn.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := stable.QueryContext(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"k"}) {
		t.Fatal(names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
		t.Fatal(types, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("new NATURAL key must reject formerly matching rows", rows.Err())
	}
	rows.Close()
	rows, err = changed.QueryContext(p.ctx)
	if rows != nil {
		rows.Close()
		t.Fatal("changed prepared shape published rows")
	}
	want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"}
	referenceAssertDiagnostic(t, err, &want)
}

func TestReferenceQueryJoinsPreparedRenameOracle(t *testing.T) { queryJoinsPreparedRename(t, true) }
func TestReferenceQueryJoinsPreparedRename(t *testing.T)       { queryJoinsPreparedRename(t, false) }

func queryJoinsPreparedRename(t *testing.T, oracle bool) {
	p := queryJoinsReferenceSetup(t)
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"postgres", p.postgres}, {"native", p.memory}} {
		if oracle && backend.name != "postgres" {
			continue
		}
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			for _, q := range []string{"CREATE TABLE shape_a(k INT,q INT DEFAULT 7)", "CREATE TABLE shape_b(k INT,q INT DEFAULT 8)", "INSERT INTO shape_a(k) VALUES(1)", "INSERT INTO shape_b(k) VALUES(1)"} {
				if _, err = conn.ExecContext(p.ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			const query = "SELECT shape_a.k FROM shape_a JOIN shape_b ON shape_a.k=shape_b.k"
			stmt, err := conn.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if _, err = conn.ExecContext(p.ctx, "ALTER TABLE shape_b RENAME k TO gone"); err != nil {
				t.Fatal(err)
			}
			rows, err := stmt.QueryContext(p.ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("rename exposed stale rows")
			}
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column shape_b.k does not exist", Hint: `Perhaps you meant to reference the column "shape_a.k".`, Position: "57"}
			if oracle {
				t.Logf("JOIN PREPARED RENAME ORACLE %q: %#v", query, referenceExtractDiagnostic(t, err))
			}
			referenceAssertDiagnostic(t, err, &want)
			const dropQuery = "SELECT shape_a.k FROM shape_a JOIN shape_b ON shape_a.k=shape_b.gone"
			dropped, err := conn.PrepareContext(p.ctx, dropQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer dropped.Close()
			if _, err = conn.ExecContext(p.ctx, "ALTER TABLE shape_b DROP COLUMN gone"); err != nil {
				t.Fatal(err)
			}
			rows, err = dropped.QueryContext(p.ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("drop exposed stale rows")
			}
			want = referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column shape_b.gone does not exist", Position: "57"}
			if oracle {
				t.Logf("JOIN PREPARED DROP ORACLE %q: %#v", dropQuery, referenceExtractDiagnostic(t, err))
			}
			referenceAssertDiagnostic(t, err, &want)
		})
	}
}

func TestReferenceQueryJoinsConsumers(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	for _, backend := range []struct {
		db       *sql.DB
		recorder *referenceRecorder
	}{{p.postgres, p.postgresRecord}, {p.memory, p.memoryRecord}} {
		for _, s := range []referenceStep{referenceExec("INSERT INTO b VALUES('d',7,1)", 1), referenceExec("CREATE TABLE dst(k INT NOT NULL,stamp INT DEFAULT 7)", 0), referenceExec("CREATE TABLE ordered(k INT,l TEXT)", 0), referenceExec("INSERT INTO ordered VALUES(2,'two'),(1,'one')", 2)} {
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
		}
		ordered := referenceSelect("SELECT ordered.l FROM ordered CROSS JOIN wide w(wk) ORDER BY k", []string{"l"}, []string{"TEXT"}, [][]any{{"one"}, {"two"}})
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, ordered)
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(ordered))
	}
	divzero := referenceImplicitError("INSERT INTO dst(k) SELECT 1/(b.k-3) FROM a CROSS JOIN b", referenceDiagnostic{Code: "22012", Message: "division by zero"})
	divzero.errorStage = "execute"
	notnull := referenceImplicitError("INSERT INTO dst(k) SELECT a.k FROM a CROSS JOIN b", referenceDiagnostic{Code: "23502", Message: `null value in column "k" of relation "dst" violates not-null constraint`, Detail: "Failing row contains (null, 7).", Schema: "public", Table: "dst", Column: "k"})
	notnull.errorStage = "execute"
	queryJoinsReferenceCases(t, p, []referenceStep{
		referenceSelect("SELECT r FROM a JOIN b USING(k)", []string{"r"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}, {"x"}, {"x"}, {"d"}, {"d"}}),
		referenceSelect("SELECT r FROM a NATURAL JOIN b", []string{"r"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}, {"x"}, {"x"}}),
		referenceExec("INSERT INTO dst(k) SELECT k FROM a NATURAL JOIN b", 4),
		divzero,
		notnull,
		referenceSelect("SELECT k,stamp FROM dst", []string{"k", "stamp"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(1), int64(7)}, {int64(1), int64(7)}, {int64(1), int64(7)}, {int64(1), int64(7)}, {int64(1), int64(7)}, {int64(1), int64(7)}, {int64(1), int64(7)}}),
	})
}
