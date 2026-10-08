//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryAliasesResolution(t *testing.T) {
	p := newReferencePair(t)
	all := [][]any{{int64(1), int64(2), int64(1)}, {int64(2), int64(1), int64(5)}, {int64(3), nil, int64(2)}, {int64(4), int64(2), int64(0)}}
	cases := []struct {
		query        string
		names, types []string
		want         [][]any
	}{
		{"SELECT x.iid AS shown,x.a,x.b FROM t AS x(iid) ORDER BY shown", []string{"shown", "a", "b"}, []string{"INT4", "INT4", "INT4"}, all},
		{"SELECT x.iid AS shown,x.a,x.b FROM t x(iid) ORDER BY shown", []string{"shown", "a", "b"}, []string{"INT4", "INT4", "INT4"}, all},
		{"SELECT x.* FROM t x(iid) ORDER BY x.iid", []string{"iid", "a", "b"}, []string{"INT4", "INT4", "INT4"}, all},
		{"SELECT iid,a,b FROM t x(iid) ORDER BY iid", []string{"iid", "a", "b"}, []string{"INT4", "INT4", "INT4"}, all},
		{`SELECT "Mixed"."Key" AS "Shown" FROM t AS "Mixed"("Key") ORDER BY "Shown"`, []string{"Shown"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}},
		{"SELECT x.iid,x.iid+1 FROM t x(iid) ORDER BY x.iid", []string{"iid", "?column?"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}, {int64(3), int64(4)}, {int64(4), int64(5)}}},
		{"SELECT a AS b,id FROM t WHERE b=1 ORDER BY b", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}}},
		{"SELECT a AS b,id FROM t ORDER BY b,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}, {int64(2), int64(4)}, {nil, int64(3)}}},
		{"SELECT a AS b,id FROM t ORDER BY t.b,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}, {int64(2), int64(1)}, {nil, int64(3)}, {int64(1), int64(2)}}},
		{"SELECT a AS b,id FROM t ORDER BY b+0,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}, {int64(2), int64(1)}, {nil, int64(3)}, {int64(1), int64(2)}}},
		{"SELECT a AS b,b FROM t ORDER BY id", []string{"b", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(5)}, {nil, int64(2)}, {int64(2), int64(0)}}},
		{"SELECT a AS x,a AS x FROM t ORDER BY x,id", []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(2), int64(2)}, {nil, nil}}},
	}
	for _, backend := range []struct {
		name     string
		db       *sql.DB
		recorder *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
			for _, population := range []string{"empty", "populated"} {
				if population == "populated" {
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4))
				}
				t.Run(population, func(t *testing.T) {
					for _, tc := range cases {
						t.Run(tc.query, func(t *testing.T) {
							want := tc.want
							if population == "empty" {
								want = [][]any{}
							}
							step := referenceSelect(tc.query, tc.names, tc.types, want)
							referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
							referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
						})
					}
				})
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE VIEW alias_view AS SELECT x.iid AS shown,x.a AS amount FROM t x(iid) WHERE x.b=1 ORDER BY shown", 0))
			step := referenceSelect("SELECT shown,amount FROM alias_view ORDER BY shown", []string{"shown", "amount"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}})
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
		})
	}
}

func TestReferenceQueryAliasesMetadataAndPreparedFreshness(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name     string
		db       *sql.DB
		recorder *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			for _, step := range []referenceStep{
				referenceExec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0),
				referenceExec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4),
				referenceExec("CREATE TABLE typed(s VARCHAR(5),n NUMERIC(7,2))", 0),
				referenceExec("INSERT INTO typed VALUES('hello',12.50)", 1),
			} {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
			}
			q := "SELECT x.iid+$1 AS sum FROM t x(iid) WHERE x.iid=$2 ORDER BY sum"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			for _, tc := range []struct {
				args []any
				want [][]any
			}{
				{[]any{10, 1}, [][]any{{int64(11)}}},
				{[]any{20, 2}, [][]any{{int64(22)}}},
				{[]any{nil, 2}, [][]any{{nil}}},
				{[]any{10, nil}, [][]any{}},
			} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceSelect(q, []string{"sum"}, []string{"INT4"}, tc.want, tc.args...))
			}
			for _, empty := range []bool{false, true} {
				q := "SELECT x.word AS label,x.amount AS price FROM typed x(word,amount)"
				want := [][]any{{"hello", "12.50"}}
				if empty {
					q += " WHERE FALSE"
					want = [][]any{}
				}
				step := referenceSelect(q, []string{"label", "price"}, []string{"VARCHAR", "NUMERIC"}, want)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
				for _, prepared := range []bool{false, true} {
					var rows *sql.Rows
					var metadataStmt *sql.Stmt
					if prepared {
						metadataStmt, err = backend.db.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						rows, err = metadataStmt.QueryContext(p.ctx)
					} else {
						rows, err = backend.db.QueryContext(p.ctx, q)
					}
					if err != nil {
						t.Fatal(err)
					}
					metadata, err := rows.ColumnTypes()
					rows.Close()
					if metadataStmt != nil {
						metadataStmt.Close()
					}
					if err != nil || len(metadata) != 2 {
						t.Fatal(metadata, err)
					}
					if length, ok := metadata[0].Length(); !ok || length != 5 {
						t.Fatal("alias lost VARCHAR length", length, ok)
					}
					if precision, scale, ok := metadata[1].DecimalSize(); !ok || precision != 7 || scale != 2 {
						t.Fatal("alias lost NUMERIC precision/scale", precision, scale, ok)
					}
				}
			}
		})
	}
}

func TestReferenceQueryAliasesFailure(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name     string
		db       *sql.DB
		recorder *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4))
			q := "SELECT 1/(x.iid-2) AS quotient FROM t x(iid) ORDER BY x.iid"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal("analysis must succeed before runtime division failure", err)
			}
			defer stmt.Close()
			step := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
			step.selectRows, step.errorStage = true, "execute"
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			referenceRunStep(t, p.ctx, held, backend.recorder, step)
		})
	}
}
