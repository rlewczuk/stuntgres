//go:build reference

package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestReferenceCastExplicitParameterPreflight(t *testing.T) {
	p := castReferenceSetup(t)
	p.step(t, referenceExec("CREATE TABLE e(i INT4)", 0))
	p.step(t, referenceExec("CREATE TABLE populated(i INT4)", 0))
	p.step(t, referenceExec("INSERT INTO populated VALUES(1)", 1))
	for _, tc := range []struct {
		name, expression string
		bad, good, again any
		want             referenceDiagnostic
	}{
		{"text", "int4($1::text)", "oops", "42", "7", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"narrow", "CAST($1::int8 AS int4)", int64(2147483648), int64(42), int64(7), referenceDiagnostic{Code: "22003", Message: "integer out of range"}},
	} {
		for _, source := range []struct {
			name, from string
			count      int
		}{
			{"empty", "e", 0}, {"filtered", "populated WHERE i=0", 0}, {"populated", "populated", 1},
		} {
			q := "SELECT " + tc.expression + " FROM " + source.from
			step := castReferenceError(q, tc.want, false)
			step.errorStage = "execute"
			step.args = []any{tc.bad}
			t.Run(tc.name+"/"+source.name+"/direct", func(t *testing.T) { castReferenceBoth(t, p, step) })
			// PostgreSQL is always exercised first, in its own subtest. A native
			// failure cannot prevent the oracle from recording a later witness.
			for _, backend := range []struct {
				name string
				db   referenceExecutor
			}{{"postgres", p.postgres}, {"native", p.memory}} {
				t.Run(tc.name+"/"+source.name+"/fresh-prepared-and-private-frames/"+backend.name, func(t *testing.T) {
					stmt, err := backend.db.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatalf("conversion must not fail Prepare: %v", err)
					}
					defer stmt.Close()
					for index, arg := range []any{tc.bad, tc.good, nil, tc.bad, tc.again} {
						t.Run(fmt.Sprint(index), func(t *testing.T) {
							rows, err := stmt.QueryContext(p.ctx, arg)
							if index == 0 || index == 3 {
								if rows != nil {
									rows.Close()
									t.Fatal("conversion failure exposed cursor")
								}
								referenceCheckDiagnostic(t, err, step, "execute")
								return
							}
							if err != nil {
								t.Fatal(err)
							}
							defer rows.Close()
							columns, err := rows.Columns()
							if err != nil || len(columns) != 1 || columns[0] != "int4" {
								t.Fatal(columns, err)
							}
							meta, err := rows.ColumnTypes()
							if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "INT4" {
								t.Fatal(meta, err)
							}
							count := 0
							for rows.Next() {
								var got any
								if err := rows.Scan(&got); err != nil {
									t.Fatal(err)
								}
								var want any
								if index == 1 {
									want = int64(42)
								}
								if index == 4 {
									want = int64(7)
								}
								if got != want {
									t.Fatalf("frame value %v want %v", got, want)
								}
								count++
							}
							if rows.Err() != nil || count != source.count {
								t.Fatal(count, source.count, rows.Err())
							}
						})
					}
				})
			}
		}
		for _, prepared := range []bool{false, true} {
			q := "SELECT i FROM e WHERE i=" + tc.expression
			step := castReferenceError(q, tc.want, false)
			step.args, step.prepared, step.errorStage = []any{tc.bad}, prepared, "execute"
			t.Run(tc.name+"/rhs/prepared="+fmt.Sprint(prepared), func(t *testing.T) { castReferenceBoth(t, p, step) })
		}
	}
}

func TestReferenceCastExplicitSelectACLPreflight(t *testing.T) {
	reader := "cast_preflight_reader"
	roleReferenceCleanup(t, []string{reader})
	p := newReferencePair(t)
	// Namespace-USAGE setup is oracle fixture authority only. Native public
	// has default USAGE; no production GRANT support is required or added.
	for _, backend := range []struct {
		name     string
		db       referenceExecutor
		recorder *referenceRecorder
	}{
		{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord},
	} {
		t.Run(backend.name, func(t *testing.T) {
			// Pin one public connection: session authorization is session-local.
			db := p.postgres
			if backend.name == "native" {
				db = p.memory
			}
			c, err := db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
			defer func() {
				if _, err := c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Error(err)
				}
				if _, err := c.ExecContext(p.ctx, "DROP SCHEMA IF EXISTS cast_private CASCADE"); err != nil {
					t.Error(err)
				}
			}()
			for _, q := range []string{"CREATE ROLE " + reader, "CREATE TABLE secret(i INT4,s TEXT,n INT8)", "CREATE SCHEMA cast_private", "CREATE TABLE cast_private.secret(i INT4,s TEXT,n INT8)"} {
				run(referenceExec(q, 0))
			}
			run(referenceExec("INSERT INTO secret VALUES(7,'oops',2147483648)", 1))
			if backend.name == "postgres" {
				if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE ON SCHEMA public TO "+reader); err != nil {
					t.Fatal(err)
				}
			}
			run(referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
			for _, tc := range []struct {
				expression string
				want       referenceDiagnostic
			}{
				{"2147483648::int4", referenceDiagnostic{Code: "22003", Message: "integer out of range"}},
				{"CAST('oops'::text AS int4)", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
				{"CAST('42'::text AS int4)", referenceDiagnostic{Code: "42501", Message: "permission denied for table secret"}},
				{"7::int4", referenceDiagnostic{Code: "42501", Message: "permission denied for table secret"}},
				{"s::int4", referenceDiagnostic{Code: "42501", Message: "permission denied for table secret"}},
				{"CAST(n AS int4)", referenceDiagnostic{Code: "42501", Message: "permission denied for table secret"}},
			} {
				for _, private := range []bool{false, true} {
					q := "SELECT " + tc.expression + " FROM public.secret"
					want := tc.want
					if private {
						q = "SELECT " + tc.expression + " FROM cast_private.secret"
						want = referenceDiagnostic{Code: "42501", Message: "permission denied for schema cast_private", Position: strconv.Itoa(strings.Index(q, "cast_private") + 1)}
					}
					for _, prepared := range []bool{false, true} {
						t.Run(q+"/prepared="+fmt.Sprint(prepared), func(t *testing.T) {
							step := castReferenceError(q, want, false)
							step.prepared, step.errorStage = prepared, "execute"
							if private && prepared {
								step.errorStage = "prepare"
							}
							run(step)
						})
					}
				}
			}
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceSelect("SELECT i,s,n FROM secret", []string{"i", "s", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(7), "oops", int64(2147483648)}}))
			// newReferencePair cleans public only; remove this fixture namespace
			// before registered role teardown, including native-failure paths.
		})
	}
}
