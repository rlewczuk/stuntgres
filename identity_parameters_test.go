package stuntgres_test

import (
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
)

func TestIdentityDiscardedParameterIndex(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	tableCoreExec(t, db, false, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,p INT)`, 0)
	held, err := db.Prepare(`INSERT INTO i(p,id) OVERRIDING USER VALUE VALUES($1,$2)`)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	_, err = held.Exec(7, "bad")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $2 = '...'"})
	wantTuples(t, db, `SELECT last_value,is_called FROM i_id_seq`, [][]any{{int64(1), false}})
	if _, err = held.Exec(7, 99); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, `SELECT * FROM i`, [][]any{{int64(1), int64(7)}})
	_, err = db.Exec(`INSERT INTO i(p,id) OVERRIDING SYSTEM VALUE VALUES($1,$2)`, 8, "bad")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $2 = '...'"})
	_, err = db.Exec(`INSERT INTO i OVERRIDING USER VALUE VALUES(CAST($1::text AS integer),8)`, "bad")
	if err != nil {
		t.Fatal("discarded execution conversion ran", err)
	}
	wantTuples(t, db, `SELECT * FROM i ORDER BY id`, [][]any{{int64(1), int64(7)}, {int64(2), int64(8)}})
	_, err = db.Exec(`INSERT INTO i(p) VALUES(CAST($1::text AS integer))`, "bad")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
	tableCoreExec(t, db, false, `CREATE TABLE ordinary(a INT,b INT)`, 0)
	_, err = db.Exec(`INSERT INTO ordinary VALUES($1,$2)`, 8, "bad")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $2 = '...'"})
}

func TestIdentityDiscardedRuntimeConversionDemand(t *testing.T) {
	for _, tc := range []struct {
		name, source        string
		fails, side, column bool
		payloads            []int64
	}{
		{"single_values_pruned", `VALUES(CAST($1::text AS integer),7)`, false, false, false, []int64{7}},
		{"multi_values_demanded", `VALUES(CAST($1::text AS integer),7),(0,8)`, true, false, false, []int64{7, 8}},
		{"select_pruned", `SELECT CAST($1::text AS integer),7`, false, false, false, []int64{7}},
		{"select_column_pruned", `SELECT CAST(v AS integer),p FROM inputs`, false, false, true, []int64{7, 8}},
		{"decorated_values_pruned", `VALUES(CAST($1::text AS integer),7) LIMIT 1`, false, false, false, []int64{7}},
		{"ordered_select_demanded", `SELECT CAST($1::text AS integer),p FROM inputs ORDER BY 1,2`, true, false, false, []int64{7, 8}},
		{"ordered_values_demanded", `VALUES(CAST($1::text AS integer),7),(0,8) ORDER BY 1`, true, false, false, []int64{8, 7}},
		{"other_order_values_demanded", `VALUES(CAST($1::text AS integer),7),(0,8) ORDER BY 2`, true, false, false, []int64{7, 8}},
		{"expression_order_values_demanded", `VALUES(CAST($1::text AS integer),7),(0,8) ORDER BY column2+1`, true, false, false, []int64{7, 8}},
		{"limited_multi_values_demanded", `VALUES(CAST($1::text AS integer),7),(0,8) LIMIT 1`, true, false, false, []int64{7}},
		{"volatile_select_demanded", `SELECT CAST(CASE WHEN nextval('side')>0 THEN $1::text ELSE '' END AS integer),7`, true, true, false, []int64{7}},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", tc.name, prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				c := sequenceConn(t, db)
				ctx := t.Context()
				sequenceExec(t, c, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(CACHE 1),p INT)`)
				sequenceExec(t, c, `CREATE SEQUENCE side START 50 CACHE 1`)
				sequenceExec(t, c, `CREATE TABLE inputs(v TEXT,p INT)`)
				identityConnExec(t, ctx, c, `INSERT INTO inputs VALUES('bad',7),('8',8)`, 2)
				query := `INSERT INTO i OVERRIDING USER VALUE ` + tc.source
				var held *sql.Stmt
				var err error
				if prepared {
					held, err = c.PrepareContext(ctx, query)
					if err != nil {
						t.Fatal("conversion failed during Prepare", err)
					}
					defer held.Close()
				}
				wantTuples(t, db, `SELECT last_value,is_called FROM i_id_seq`, [][]any{{int64(1), false}})
				run := func(value string) (sql.Result, error) {
					args := []any{value}
					if tc.column {
						args = nil
					}
					if prepared {
						return held.ExecContext(ctx, args...)
					}
					return c.ExecContext(ctx, query, args...)
				}
				rowsFor := func(start int64) [][]any {
					rows := make([][]any, len(tc.payloads))
					for i, p := range tc.payloads {
						rows[i] = []any{start + int64(i), p}
					}
					return rows
				}
				result, err := run("bad")
				if tc.fails {
					if result != nil {
						t.Fatal("failed INSERT returned result", result)
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
					wantTuples(t, db, `SELECT * FROM i`, [][]any{})
					wantTuples(t, db, `SELECT last_value,is_called FROM i_id_seq`, [][]any{{int64(1), false}})
					var memory int64
					typeCoreDiagnostic(t, c.QueryRowContext(ctx, `SELECT currval('i_id_seq')`).Scan(&memory), stuntgres.Error{Code: "55000", Message: `currval of sequence "i_id_seq" is not yet defined in this session`})
					if !tc.side {
						typeCoreDiagnostic(t, c.QueryRowContext(ctx, `SELECT lastval()`).Scan(&memory), stuntgres.Error{Code: "55000", Message: "lastval is not yet defined in this session"})
					}
				} else {
					if err != nil {
						t.Fatal("pruned conversion was demanded", err)
					}
					if n, err := result.RowsAffected(); err != nil || n != int64(len(tc.payloads)) {
						t.Fatal(n, err)
					}
					wantTuples(t, db, `SELECT * FROM i ORDER BY id`, rowsFor(1))
				}
				wantTuples(t, db, `SELECT last_value,is_called FROM side`, [][]any{{int64(50), tc.side}})
				if tc.side {
					sequenceValue(t, c, `SELECT currval('side')`, 50)
					sequenceValue(t, c, `SELECT lastval()`, 50)
				}
				identityConnExec(t, ctx, c, `UPDATE inputs SET v='8'`, 2)
				result, err = run("8")
				if err != nil {
					t.Fatal("healthy held/direct reuse", err)
				}
				if n, err := result.RowsAffected(); err != nil || n != int64(len(tc.payloads)) {
					t.Fatal(n, err)
				}
				var want [][]any
				if !tc.fails {
					want = rowsFor(1)
				}
				want = append(want, rowsFor(int64(len(want)+1))...)
				wantTuples(t, db, `SELECT * FROM i ORDER BY id`, want)
				sequenceValue(t, c, `SELECT currval('i_id_seq')`, int64(len(want)))
				sequenceValue(t, c, `SELECT lastval()`, int64(len(want)))
				last := int64(50)
				if tc.side {
					last = 51
				}
				wantTuples(t, db, `SELECT last_value,is_called FROM side`, [][]any{{last, tc.side}})
			})
		}
	}
}
