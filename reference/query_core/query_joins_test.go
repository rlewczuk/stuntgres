package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func queryJoinsReferenceSetup(t *testing.T) *fixture.Pair {
	p := fixture.NewScenario(t)
	queryJoinsReferenceSeed(t, p)
	return p
}

func queryJoinsReferenceSeed(t *testing.T, p *fixture.Pair) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE a(l TEXT,k INT,q INT)", 0),
		fixture.Exec("CREATE TABLE b(r TEXT,q INT,k INT)", 0),
		fixture.Exec("CREATE TABLE c(k INT,c TEXT)", 0),
		fixture.Exec("CREATE TABLE empty(r TEXT,q INT,k INT)", 0),
		fixture.Exec("CREATE TABLE wide(k BIGINT)", 0),
		fixture.Exec("INSERT INTO a VALUES('a',1,9),('a',1,9),('n',NULL,9),('z',2,8)", 4),
		fixture.Exec("INSERT INTO b VALUES('x',9,1),('x',9,1),('y',8,3),('n',9,NULL)", 4),
		fixture.Exec("INSERT INTO c VALUES(1,'c'),(1,'c')", 2),
		fixture.Exec("INSERT INTO wide VALUES(1)", 1),
	} {
		for _, backend := range p.Backends() {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
		}
	}
}

func TestReferenceQueryJoinsRegression(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	merged := [][]any{{int64(1), "a", int64(9), "x", int64(9)}, {int64(1), "a", int64(9), "x", int64(9)}, {int64(1), "a", int64(9), "x", int64(9)}, {int64(1), "a", int64(9), "x", int64(9)}}
	nested := [][]any{
		{int64(1), "a", int64(9), "x", int64(9), "c"}, {int64(1), "a", int64(9), "x", int64(9), "c"},
		{int64(1), "a", int64(9), "x", int64(9), "c"}, {int64(1), "a", int64(9), "x", int64(9), "c"},
		{int64(1), "a", int64(9), "x", int64(9), "c"}, {int64(1), "a", int64(9), "x", int64(9), "c"},
		{int64(1), "a", int64(9), "x", int64(9), "c"}, {int64(1), "a", int64(9), "x", int64(9), "c"},
	}
	steps := []fixture.Step{
		fixture.Select("SELECT * FROM a JOIN b ON a.k=b.k", []string{"l", "k", "q", "r", "q", "k"}, []string{"TEXT", "INT4", "INT4", "TEXT", "INT4", "INT4"}, [][]any{{"a", int64(1), int64(9), "x", int64(9), int64(1)}, {"a", int64(1), int64(9), "x", int64(9), int64(1)}, {"a", int64(1), int64(9), "x", int64(9), int64(1)}, {"a", int64(1), int64(9), "x", int64(9), int64(1)}}),
		fixture.Select("SELECT * FROM a JOIN b USING(k)", []string{"k", "l", "q", "r", "q"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4"}, merged),
		fixture.Select("SELECT * FROM a JOIN b USING(q,k)", []string{"q", "k", "l", "r"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, [][]any{{int64(9), int64(1), "a", "x"}, {int64(9), int64(1), "a", "x"}, {int64(9), int64(1), "a", "x"}, {int64(9), int64(1), "a", "x"}}),
		fixture.Select("SELECT * FROM a NATURAL JOIN b", []string{"k", "q", "l", "r"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, [][]any{{int64(1), int64(9), "a", "x"}, {int64(1), int64(9), "a", "x"}, {int64(1), int64(9), "a", "x"}, {int64(1), int64(9), "a", "x"}}),
		fixture.Select("SELECT * FROM a NATURAL INNER JOIN b", []string{"k", "q", "l", "r"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, [][]any{{int64(1), int64(9), "a", "x"}, {int64(1), int64(9), "a", "x"}, {int64(1), int64(9), "a", "x"}, {int64(1), int64(9), "a", "x"}}),
		fixture.Select("SELECT u.*,a.k,b.k FROM a JOIN b USING(q,k) AS u", []string{"q", "k", "k", "k"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(9), int64(1), int64(1), int64(1)}, {int64(9), int64(1), int64(1), int64(1)}, {int64(9), int64(1), int64(1), int64(1)}, {int64(9), int64(1), int64(1), int64(1)}}),
		fixture.Select("SELECT a.*,b.k FROM a JOIN b USING(k)", []string{"l", "k", "q", "k"}, []string{"TEXT", "INT4", "INT4", "INT4"}, [][]any{{"a", int64(1), int64(9), int64(1)}, {"a", int64(1), int64(9), int64(1)}, {"a", int64(1), int64(9), int64(1)}, {"a", int64(1), int64(9), int64(1)}}),
		fixture.Select("SELECT * FROM (a JOIN b USING(k)) j(kk,ll)", []string{"kk", "ll", "q", "r", "q"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4"}, merged),
		fixture.Select("SELECT * FROM a JOIN (b JOIN c USING(k)) USING(k)", []string{"k", "l", "q", "r", "q", "c"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "TEXT"}, nested),
		fixture.Select("SELECT * FROM (a JOIN b USING(k)) JOIN c USING(k)", []string{"k", "l", "q", "r", "q", "c"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "TEXT"}, nested),
		fixture.Select("SELECT x.* FROM a AS x(ll,kk) WHERE x.kk=1", []string{"ll", "kk", "q"}, []string{"TEXT", "INT4", "INT4"}, [][]any{{"a", int64(1), int64(9)}, {"a", int64(1), int64(9)}}),
		fixture.Select("SELECT a.k,wide.k,k FROM a JOIN wide USING(k)", []string{"k", "k", "k"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(1), int64(1), int64(1)}}),
		fixture.Select("SELECT wide.k,a.k,k FROM wide JOIN a USING(k)", []string{"k", "k", "k"}, []string{"INT8", "INT4", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(1), int64(1), int64(1)}}),
		fixture.Select("SELECT a.l,b.r FROM a JOIN b ON a.k=$1 AND a.k=b.k", []string{"l", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"a", "x"}, {"a", "x"}, {"a", "x"}}, 1),
		fixture.Select("SELECT a.l,b.r FROM a JOIN b ON a.k=$1 AND a.k=b.k", []string{"l", "r"}, []string{"TEXT", "TEXT"}, nil, nil),
		fixture.Select("SELECT a.l,b.r FROM a JOIN b ON a.k=$1 AND a.k=b.k", []string{"l", "r"}, []string{"TEXT", "TEXT"}, nil, 2),
		fixture.Select("SELECT $1 FROM a JOIN b ON a.k=$1 AND a.k=b.k", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}}, 1),
		fixture.Select("SELECT $1 FROM a JOIN b ON a.k=$1 AND a.k=b.k", []string{"?column?"}, []string{"INT4"}, nil, nil),
		fixture.Select("SELECT $1 FROM a JOIN b ON a.k=$1 AND a.k=b.k", []string{"?column?"}, []string{"INT4"}, nil, 2),
		fixture.Select("SELECT count(*) FROM a JOIN b USING(k)", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}),
		fixture.Select("SELECT k,count(*) FROM a JOIN b USING(k) GROUP BY k HAVING count(*)=4", []string{"k", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(4)}}),
		fixture.Select("SELECT a.l,b.r FROM a JOIN b ON CAST(a.k AS BIGINT)=b.k AND COALESCE(a.q,0)=b.q AND CASE WHEN b.r='x' THEN TRUE ELSE FALSE END", []string{"l", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"a", "x"}, {"a", "x"}, {"a", "x"}}),
		fixture.Select("SELECT CASE WHEN a.k=b.k THEN a.l ELSE 'bad' END,CAST(b.k AS BIGINT) FROM a JOIN b USING(k) WHERE COALESCE(a.q,0)=b.q", []string{"case", "k"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {"a", int64(1)}, {"a", int64(1)}, {"a", int64(1)}}),
		fixture.Select(`SELECT /*é*/ "X"."K",b.k FROM a AS "X"("L","K") JOIN b ON "X"."K"=b.k`, []string{"K", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}, {int64(1), int64(1)}, {int64(1), int64(1)}}),
	}
	for _, q := range []string{"SELECT * FROM a JOIN b ON FALSE", "SELECT * FROM a JOIN b ON NULL", "SELECT * FROM a CROSS JOIN empty", "SELECT * FROM empty CROSS JOIN a"} {
		names := []string{"l", "k", "q", "r", "q", "k"}
		if q == "SELECT * FROM empty CROSS JOIN a" {
			names = []string{"r", "q", "k", "l", "k", "q"}
		}
		steps = append(steps, fixture.Select(q, names, []string{"TEXT", "INT4", "INT4", "TEXT", "INT4", "INT4"}, nil))
	}
	queryJoinsReferenceCases(t, p, steps)
}

func queryJoinsReferenceCases(t *testing.T, p *fixture.Pair, steps []fixture.Step) {
	t.Helper()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, s := range steps {
				s.Bag = true
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(s))
			}
		})
	}
}

// Independently assert the pinned fixed-shape contract on each selected backend.
func queryJoinsPreparedSchema(t *testing.T, p *fixture.Pair, db *sql.DB) {
	conn, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{"CREATE TABLE shape_a(k INT,q INT)", "CREATE TABLE shape_b(k INT,other INT)", "INSERT INTO shape_a VALUES(1,7)", "INSERT INTO shape_b VALUES(1,8)"} {
		if _, err := conn.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	stable, err := conn.PrepareContext(p.Ctx, "SELECT k FROM shape_a NATURAL JOIN shape_b")
	if err != nil {
		t.Fatal(err)
	}
	defer stable.Close()
	changed, err := conn.PrepareContext(p.Ctx, "SELECT * FROM shape_a NATURAL JOIN shape_b")
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
		rows, err := initial.stmt.QueryContext(p.Ctx)
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
		if _, err := conn.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := stable.QueryContext(p.Ctx)
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
	rows, err = changed.QueryContext(p.Ctx)
	if rows != nil {
		rows.Close()
		t.Fatal("changed prepared shape published rows")
	}
	want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"}
	fixture.AssertDiagnostic(t, err, &want)
}

func TestReferenceQueryJoinsPreparedSchema(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			queryJoinsPreparedSchema(t, p, backend.DB)
		})
	}
}

func TestReferenceQueryJoinsPreparedRename(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			for _, q := range []string{"CREATE TABLE shape_a(k INT,q INT DEFAULT 7)", "CREATE TABLE shape_b(k INT,q INT DEFAULT 8)", "INSERT INTO shape_a(k) VALUES(1)", "INSERT INTO shape_b(k) VALUES(1)"} {
				if _, err = conn.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			const query = "SELECT shape_a.k FROM shape_a JOIN shape_b ON shape_a.k=shape_b.k"
			stmt, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if _, err = conn.ExecContext(p.Ctx, "ALTER TABLE shape_b RENAME k TO gone"); err != nil {
				t.Fatal(err)
			}
			rows, err := stmt.QueryContext(p.Ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("rename exposed stale rows")
			}
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column shape_b.k does not exist", Hint: `Perhaps you meant to reference the column "shape_a.k".`, Position: "57"}
			fixture.AssertDiagnostic(t, err, &want)
			const dropQuery = "SELECT shape_a.k FROM shape_a JOIN shape_b ON shape_a.k=shape_b.gone"
			dropped, err := conn.PrepareContext(p.Ctx, dropQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer dropped.Close()
			if _, err = conn.ExecContext(p.Ctx, "ALTER TABLE shape_b DROP COLUMN gone"); err != nil {
				t.Fatal(err)
			}
			rows, err = dropped.QueryContext(p.Ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("drop exposed stale rows")
			}
			want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column shape_b.gone does not exist", Position: "57"}
			fixture.AssertDiagnostic(t, err, &want)
		})
	}
}

func TestReferenceQueryJoinsConsumers(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	for _, backend := range p.Backends() {
		for _, s := range []fixture.Step{fixture.Exec("INSERT INTO b VALUES('d',7,1)", 1), fixture.Exec("CREATE TABLE dst(k INT NOT NULL,stamp INT DEFAULT 7)", 0), fixture.Exec("CREATE TABLE ordered(k INT,l TEXT)", 0), fixture.Exec("INSERT INTO ordered VALUES(2,'two'),(1,'one')", 2)} {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
		}
		ordered := fixture.Select("SELECT ordered.l FROM ordered CROSS JOIN wide w(wk) ORDER BY k", []string{"l"}, []string{"TEXT"}, [][]any{{"one"}, {"two"}})
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, ordered)
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(ordered))
	}
	divzero := fixture.ImplicitError("INSERT INTO dst(k) SELECT 1/(b.k-3) FROM a CROSS JOIN b", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
	divzero.ErrorStage = "execute"
	notnull := fixture.ImplicitError("INSERT INTO dst(k) SELECT a.k FROM a CROSS JOIN b", fixture.Diagnostic{Code: "23502", Message: `null value in column "k" of relation "dst" violates not-null constraint`, Detail: "Failing row contains (null, 7).", Schema: "public", Table: "dst", Column: "k"})
	notnull.ErrorStage = "execute"
	queryJoinsReferenceCases(t, p, []fixture.Step{
		fixture.Select("SELECT r FROM a JOIN b USING(k)", []string{"r"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}, {"x"}, {"x"}, {"d"}, {"d"}}),
		fixture.Select("SELECT r FROM a NATURAL JOIN b", []string{"r"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}, {"x"}, {"x"}}),
		fixture.Exec("INSERT INTO dst(k) SELECT k FROM a NATURAL JOIN b", 4),
		divzero,
		notnull,
		fixture.Select("SELECT k,stamp FROM dst", []string{"k", "stamp"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(1), int64(7)}, {int64(1), int64(7)}, {int64(1), int64(7)}, {int64(1), int64(7)}, {int64(1), int64(7)}, {int64(1), int64(7)}, {int64(1), int64(7)}}),
	})
}
