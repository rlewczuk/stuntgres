package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRangeTypeConstructorAliases(t *testing.T) {
	for _, tc := range []struct {
		subtype, left, right string
		setup                []string
	}{
		{"regclass", `'range_ctor_alias.left_target'::regclass`, `'range_ctor_alias.right_target'::regclass`, []string{
			`CREATE TABLE range_ctor_alias.left_target(k integer)`,
			`CREATE TABLE range_ctor_alias.right_target(k integer)`,
		}},
		{"regtype", `'range_ctor_alias.left_target'::regtype`, `'range_ctor_alias.right_target'::regtype`, []string{
			`CREATE TYPE range_ctor_alias.left_target AS ENUM('a','b')`,
			`CREATE TYPE range_ctor_alias.right_target AS ENUM('a','b')`,
		}},
		{"regprocedure", `'range_ctor_alias.endpoint(integer)'::regprocedure`, `'range_ctor_alias.endpoint(text)'::regprocedure`, []string{
			`CREATE FUNCTION range_ctor_alias.endpoint(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$`,
			`CREATE FUNCTION range_ctor_alias.endpoint(text) RETURNS text LANGUAGE SQL AS $$SELECT $1$$`,
		}},
	} {
		t.Run(tc.subtype, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "range_ctor_alias")
			rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
				// The first allocated schema precedes the referenced objects. Fresh
				// native replay retains an occupied temporary namespace/relation,
				// exercising the existing collision-remap path, not numeric OID goldens.
				run(fixture.Exec(`CREATE SCHEMA range_ctor_alias`, 0))
				for _, q := range tc.setup {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec(`CREATE TYPE constructor_alias AS RANGE(SUBTYPE=`+tc.subtype+`,SUBTYPE_OPCLASS=pg_catalog.oid_ops,MULTIRANGE_TYPE_NAME=constructor_aliases)`, 0))
				run(fixture.Exec(`CREATE TABLE constructor_alias_data(k integer,r constructor_alias,m constructor_aliases)`, 0))
				rangeExpr := `constructor_alias(` + tc.left + `,` + tc.left + `,'[]')`
				unbounded := `constructor_alias(NULL::` + tc.subtype + `,` + tc.right + `,'[]')`
				run(fixture.Exec(`INSERT INTO constructor_alias_data VALUES(1,`+rangeExpr+`,constructor_aliases(`+rangeExpr+`)),(2,`+unbounded+`,constructor_aliases(`+unbounded+`))`, 2))
				expected := `(CASE WHEN k=1 THEN (` + tc.left + `)::oid ELSE (` + tc.right + `)::oid END)`
				q := `SELECT k,lower(r)::oid=` + expected + ` AS lo,upper(r)::oid=` + expected + ` AS hi,lower(range_merge(m))::oid=` + expected + ` AS multi_lo,upper(range_merge(m))::oid=` + expected + ` AS multi_hi FROM constructor_alias_data ORDER BY k`
				check := func(backend fixture.Backend, exec fixture.Executor) {
					t.Helper()
					for _, prepared := range []bool{false, true} {
						s := fixture.Select(q, []string{"k", "lo", "hi", "multi_lo", "multi_hi"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), true, true, true, true}, {int64(2), nil, true, nil, true}})
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, exec, backend.Recorder, s)
					}
				}
				check(b, conn)
				saved := fixture.Select(q, []string{"k", "lo", "hi", "multi_lo", "multi_hi"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), true, true, true, true}, {int64(2), nil, true, nil, true}})
				after := saved
				after.Rows = append(append([][]any(nil), saved.Rows...), []any{int64(3), true, true, true, true})
				nextRange := `constructor_alias(` + tc.right + `,` + tc.right + `,'[]')`
				insert := fixture.Prepared(fixture.Exec(`INSERT INTO constructor_alias_data VALUES(3,`+nextRange+`,constructor_aliases(`+nextRange+`))`, 1))
				rangeTypeConstructorReplay(t, p, b, conn, false, check, saved, insert, after)
			})
		})
	}
}
