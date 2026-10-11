package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCastExplicitParameterPreflight(t *testing.T) {
	p := castReferenceSetup(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE e(i INT4)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE populated(i INT4)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO populated VALUES(1)", 1))
	for _, tc := range []struct {
		name, expression string
		bad, good, again any
		want             fixture.Diagnostic
	}{
		{"text", "int4($1::text)", "oops", "42", "7", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"narrow", "CAST($1::int8 AS int4)", int64(2147483648), int64(42), int64(7), fixture.Diagnostic{Code: "22003", Message: "integer out of range"}},
	} {
		for _, source := range []struct {
			name, from string
			count      int
		}{
			{"empty", "e", 0}, {"filtered", "populated WHERE i=0", 0}, {"populated", "populated", 1},
		} {
			q := "SELECT " + tc.expression + " FROM " + source.from
			step := castReferenceError(q, tc.want, false)
			step.ErrorStage = "execute"
			step.Args = []any{tc.bad}
			t.Run(tc.name+"/"+source.name+"/direct", func(t *testing.T) { fixture.ScalarStep(t, p, step) })
			// Each selected backend asserts execution-owned conversions independently.
			for _, backend := range p.Backends() {
				t.Run(tc.name+"/"+source.name+"/fresh-prepared-and-private-frames/"+backend.Name, func(t *testing.T) {
					stmt, err := backend.DB.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatalf("conversion must not fail Prepare: %v", err)
					}
					defer stmt.Close()
					for index, arg := range []any{tc.bad, tc.good, nil, tc.bad, tc.again} {
						t.Run(fmt.Sprint(index), func(t *testing.T) {
							rows, err := stmt.QueryContext(p.Ctx, arg)
							if index == 0 || index == 3 {
								if rows != nil {
									rows.Close()
									t.Fatal("conversion failure exposed cursor")
								}
								fixture.State(t, err, step.State)
								fixture.CheckDiagnostic(t, err, step, "execute")
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
							fixture.ScalarAssertMetadata(t, backend.PG, meta)
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
			step.Args, step.Prepared, step.ErrorStage = []any{tc.bad}, prepared, "execute"
			t.Run(tc.name+"/rhs/prepared="+fmt.Sprint(prepared), func(t *testing.T) { fixture.ScalarStep(t, p, step) })
		}
	}
}

func TestReferenceCastExplicitSelectACLPreflight(t *testing.T) {
	reader := "cast_preflight_reader"
	fixture.RoleReferenceCleanup(t, []string{reader})
	p := fixture.NewScenario(t)
	// Namespace-USAGE setup is oracle fixture authority only. Native public
	// has default USAGE; no production GRANT support is required or added.
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			// Pin one public connection: session authorization is session-local.
			db := backend.DB
			c, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.ScalarRunStep(t, p.Ctx, c, backend.Recorder, s) }
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Error(err)
				}
				if _, err := c.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS cast_private CASCADE"); err != nil {
					t.Error(err)
				}
			}()
			for _, q := range []string{"CREATE ROLE " + reader, "CREATE TABLE secret(i INT4,s TEXT,n INT8)", "CREATE SCHEMA cast_private", "CREATE TABLE cast_private.secret(i INT4,s TEXT,n INT8)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO secret VALUES(7,'oops',2147483648)", 1))
			if backend.PG {
				if _, err := fixture.Admin.ExecContext(p.Ctx, "GRANT USAGE ON SCHEMA public TO "+reader); err != nil {
					t.Fatal(err)
				}
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
			for _, tc := range []struct {
				expression string
				want       fixture.Diagnostic
			}{
				{"2147483648::int4", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}},
				{"CAST('oops'::text AS int4)", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
				{"CAST('42'::text AS int4)", fixture.Diagnostic{Code: "42501", Message: "permission denied for table secret"}},
				{"7::int4", fixture.Diagnostic{Code: "42501", Message: "permission denied for table secret"}},
				{"s::int4", fixture.Diagnostic{Code: "42501", Message: "permission denied for table secret"}},
				{"CAST(n AS int4)", fixture.Diagnostic{Code: "42501", Message: "permission denied for table secret"}},
			} {
				for _, private := range []bool{false, true} {
					q := "SELECT " + tc.expression + " FROM public.secret"
					want := tc.want
					if private {
						q = "SELECT " + tc.expression + " FROM cast_private.secret"
						want = fixture.Diagnostic{Code: "42501", Message: "permission denied for schema cast_private", Position: strconv.Itoa(strings.Index(q, "cast_private") + 1)}
					}
					for _, prepared := range []bool{false, true} {
						t.Run(q+"/prepared="+fmt.Sprint(prepared), func(t *testing.T) {
							step := castReferenceError(q, want, false)
							step.Prepared, step.ErrorStage = prepared, "execute"
							if private && prepared {
								step.ErrorStage = "prepare"
							}
							run(step)
						})
					}
				}
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT i,s,n FROM secret", []string{"i", "s", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(7), "oops", int64(2147483648)}}))
			// The mode-aware fixture cleans public only; remove this namespace
			// before registered role teardown, including native-failure paths.
		})
	}
}
