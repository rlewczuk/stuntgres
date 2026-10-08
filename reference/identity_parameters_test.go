//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceIdentityDiscardedParameterIndex(t *testing.T) {
	identityReferenceCase(t, func(t *testing.T, db *sql.DB) {
		identityRefExec(t, db, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,p INT)`, 0)
		held, err := db.Prepare(`INSERT INTO i(p,id) OVERRIDING USER VALUE VALUES($1,$2)`)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		_, err = held.Exec(7, "bad")
		want := controlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		want.Context = "unnamed portal parameter $2 = '...'"
		referenceAssertDiagnostic(t, err, &want)
		identityRefRows(t, db, `SELECT last_value FROM i_id_seq`, [][]int64{{1}})
		if _, err = held.Exec(7, 99); err != nil {
			t.Fatal(err)
		}
		identityRefRows(t, db, `SELECT * FROM i`, [][]int64{{1, 7}})
		_, err = db.Exec(`INSERT INTO i OVERRIDING USER VALUE VALUES(CAST($1::text AS integer),8)`, "bad")
		if err != nil {
			t.Fatal("discarded execution conversion ran", err)
		}
		identityRefRows(t, db, `SELECT * FROM i ORDER BY id`, [][]int64{{1, 7}, {2, 8}})
		_, err = db.Exec(`INSERT INTO i(p) VALUES(CAST($1::text AS integer))`, "bad")
		want = controlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		referenceAssertDiagnostic(t, err, &want)
		identityRefExec(t, db, `CREATE TABLE ordinary(a INT,b INT)`, 0)
		_, err = db.Exec(`INSERT INTO ordinary VALUES($1,$2)`, 8, "bad")
		want.Context = "unnamed portal parameter $2 = '...'"
		referenceAssertDiagnostic(t, err, &want)
	})
}

func TestReferenceIdentityDiscardedRuntimeConversionDemand(t *testing.T) {
	identityReferenceCase(t, func(t *testing.T, db *sql.DB) {
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
					ctx := t.Context()
					c, err := db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					for _, q := range []string{`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(CACHE 1),p INT)`, `CREATE SEQUENCE side START 50 CACHE 1`, `CREATE TABLE inputs(v TEXT,p INT)`} {
						sequenceProbeExec(t, ctx, c, q)
					}
					identityRefConnExec(t, ctx, c, `INSERT INTO inputs VALUES('bad',7),('8',8)`, 2)
					defer sequenceProbeExec(t, ctx, c, `DROP TABLE i`)
					defer sequenceProbeExec(t, ctx, c, `DROP TABLE inputs`)
					defer sequenceProbeExec(t, ctx, c, `DROP SEQUENCE side`)
					query := `INSERT INTO i OVERRIDING USER VALUE ` + tc.source
					var held *sql.Stmt
					if prepared {
						held, err = c.PrepareContext(ctx, query)
						if err != nil {
							t.Fatal("conversion failed during Prepare", err)
						}
						defer held.Close()
					}
					var last int64
					var called bool
					if err = c.QueryRowContext(ctx, `SELECT last_value,is_called FROM i_id_seq`).Scan(&last, &called); err != nil || last != 1 || called {
						t.Fatal(last, called, err)
					}
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
					rowsFor := func(start int64) [][]int64 {
						rows := make([][]int64, len(tc.payloads))
						for i, p := range tc.payloads {
							rows[i] = []int64{start + int64(i), p}
						}
						return rows
					}
					result, err := run("bad")
					if tc.fails {
						if result != nil {
							t.Fatal("failed INSERT returned result", result)
						}
						want := controlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
						referenceAssertDiagnostic(t, err, &want)
						identityRefRows(t, db, `SELECT * FROM i`, nil)
						if err = c.QueryRowContext(ctx, `SELECT last_value,is_called FROM i_id_seq`).Scan(&last, &called); err != nil || last != 1 || called {
							t.Fatal(last, called, err)
						}
						want = controlsReferenceDiagnostic("55000", `currval of sequence "i_id_seq" is not yet defined in this session`)
						referenceAssertDiagnostic(t, c.QueryRowContext(ctx, `SELECT currval('i_id_seq')`).Scan(&last), &want)
						if !tc.side {
							want = controlsReferenceDiagnostic("55000", "lastval is not yet defined in this session")
							referenceAssertDiagnostic(t, c.QueryRowContext(ctx, `SELECT lastval()`).Scan(&last), &want)
						}
					} else {
						if err != nil {
							t.Fatal("pruned conversion was demanded", err)
						}
						if n, err := result.RowsAffected(); err != nil || n != int64(len(tc.payloads)) {
							t.Fatal(n, err)
						}
						identityRefRows(t, db, `SELECT * FROM i ORDER BY id`, rowsFor(1))
					}
					if err = c.QueryRowContext(ctx, `SELECT last_value,is_called FROM side`).Scan(&last, &called); err != nil || last != 50 || called != tc.side {
						t.Fatal(last, called, err)
					}
					if tc.side {
						sequenceProbeValue(t, ctx, c, `SELECT currval('side')`, 50)
						sequenceProbeValue(t, ctx, c, `SELECT lastval()`, 50)
					}
					identityRefConnExec(t, ctx, c, `UPDATE inputs SET v='8'`, 2)
					result, err = run("8")
					if err != nil {
						t.Fatal("healthy held/direct reuse", err)
					}
					if n, err := result.RowsAffected(); err != nil || n != int64(len(tc.payloads)) {
						t.Fatal(n, err)
					}
					var want [][]int64
					if !tc.fails {
						want = rowsFor(1)
					}
					want = append(want, rowsFor(int64(len(want)+1))...)
					identityRefRows(t, db, `SELECT * FROM i ORDER BY id`, want)
					sequenceProbeValue(t, ctx, c, `SELECT currval('i_id_seq')`, int64(len(want)))
					sequenceProbeValue(t, ctx, c, `SELECT lastval()`, int64(len(want)))
					wantLast := int64(50)
					if tc.side {
						wantLast = 51
					}
					if err = c.QueryRowContext(ctx, `SELECT last_value,is_called FROM side`).Scan(&last, &called); err != nil || last != wantLast || called != tc.side {
						t.Fatal(last, called, err)
					}
				})
			}
		}
	})
}
