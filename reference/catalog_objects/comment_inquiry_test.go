package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCommentInquiry(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "Recorded", true: "Source"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db := backend.DB
					run := func(s fixture.Step) {
						t.Helper()
						s.NonContext = nonContext && !s.SelectRows
						commentInquiryStep(t, p, backend, db, s)
					}
					for _, q := range []string{"CREATE TABLE t(a INT,b TEXT,c BIGINT)", "CREATE TABLE u(a INT)", "COMMENT ON TABLE t IS 'whole'", "COMMENT ON TABLE u IS 'other'", "COMMENT ON COLUMN t.a IS 'first'", "COMMENT ON COLUMN t.b IS 'second'", "COMMENT ON COLUMN t.c IS 'third'", "COMMENT ON ROLE postgres IS 'shared'"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO t VALUES(1,'x',3),(2,'y',4)", 2))
					var oid, other int64
					if err := db.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='t'").Scan(&oid); err != nil {
						t.Fatal(err)
					}
					if err := db.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='u'").Scan(&other); err != nil {
						t.Fatal(err)
					}
					roleOID := int64(10)
					if backend.PG {
						if err := db.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='postgres'").Scan(&roleOID); err != nil {
							t.Fatal(err)
						}
					}
					if oid <= 0 || other <= 0 || oid == other || roleOID <= 0 {
						t.Fatal("invalid discovered identities", oid, other, roleOID)
					}
					selectOne := func(q, label string, want any, args ...any) fixture.Step {
						return fixture.Select(q, []string{label}, []string{"TEXT"}, [][]any{{want}}, args...)
					}
					for _, tc := range []struct {
						expr, label string
						want        any
					}{
						{"obj_description('t'::regclass,'pg_class'::text)", "obj_description", "whole"},
						{"obj_description('t'::regclass)", "obj_description", "whole"},
						{"col_description('t'::regclass,0)", "col_description", "whole"},
						{"col_description('t'::regclass,1)", "col_description", "first"},
						{"col_description('t'::regclass,2)", "col_description", "second"},
						{"col_description('t'::regclass,3)", "col_description", "third"},
						{fmt.Sprintf("shobj_description(%d,'pg_authid'::text)", roleOID), "shobj_description", "shared"},
						{fmt.Sprintf("obj_description(%d,'pg_authid')", roleOID), "obj_description", nil},
						{"shobj_description('t'::regclass,'pg_class')", "shobj_description", nil},
						{"obj_description(11,'pg_namespace')", "obj_description", "system catalog schema"},
						{"obj_description(23,'pg_type')", "obj_description", "-2 billion to 2 billion integer, 4-byte storage"},
						{"col_description(NULL,1)", "col_description", nil}, {"col_description(1,NULL)", "col_description", nil},
						{"obj_description(NULL)", "obj_description", nil}, {"obj_description(NULL,'pg_class')", "obj_description", nil}, {"obj_description(1,NULL)", "obj_description", nil},
						{"shobj_description(NULL,'pg_authid')", "shobj_description", nil}, {"shobj_description(1,NULL)", "shobj_description", nil},
						{"shobj_description(10,NULL)", "shobj_description", nil},
						{"obj_description(4294967295::bigint,'pg_class')", "obj_description", nil},
						{"obj_description(0,'pg_class')", "obj_description", nil},
						{"pg_catalog.col_description('t'::regclass,1)", "col_description", "first"},
					} {
						s := selectOne("SELECT "+tc.expr, tc.label, tc.want)
						run(s)
						run(fixture.Prepared(s))
						many := fixture.Select("SELECT "+tc.expr+" FROM t", []string{tc.label}, []string{"TEXT"}, [][]any{{tc.want}, {tc.want}})
						run(many)
						run(fixture.Prepared(many))
						rows, err := db.Query(s.Query)
						commentInquiryRows(t, rows, err, backend.PG, false, tc.label, []any{tc.want})
						rows, err = db.Query(many.Query)
						commentInquiryRows(t, rows, err, backend.PG, false, tc.label, []any{tc.want, tc.want})
					}
					for _, cat := range []string{"PG_CLASS", "pg_catalog.pg_class", "\"pg_class\"", " pg_class", "", "missing", "pg_namespace"} {
						run(selectOne("SELECT obj_description($1,$2)", "obj_description", nil, oid, cat))
						run(selectOne("SELECT obj_description('t'::regclass,$1)", "obj_description", nil, cat))
						rows, err := db.Query("SELECT obj_description('t'::regclass,$1)", cat)
						commentInquiryRows(t, rows, err, backend.PG, false, "obj_description", []any{nil})
					}
					commentInquiryStep(t, p, backend, db, fixture.Exec("CREATE TABLE inquiry_smallint(v SMALLINT)", 0))
					commentInquiryStep(t, p, backend, db, fixture.Exec("INSERT INTO inquiry_smallint VALUES(1)", 1))
					smallint := selectOne("SELECT obj_description(v,'pg_class') FROM inquiry_smallint", "obj_description", nil)
					run(smallint)
					run(fixture.Prepared(smallint))
					for _, cast := range []string{"integer", "bigint", "oid"} {
						run(selectOne(fmt.Sprintf("SELECT obj_description(1::%s,'pg_class')", cast), "obj_description", nil))
					}
					run(selectOne("SELECT obj_description('1'::regclass,'pg_class')", "obj_description", nil))
					lazyCase := fixture.Select("SELECT CASE WHEN a=1 THEN obj_description('t'::regclass,'pg_class') ELSE col_description(NULL,1) END AS description FROM t ORDER BY a", []string{"description"}, []string{"TEXT"}, [][]any{{"whole"}, {nil}})
					run(lazyCase)
					run(fixture.Prepared(lazyCase))
					t.Run("Held", func(t *testing.T) {
						for _, tc := range []struct {
							q, label, target string
							first, second    []any
							want, other      any
						}{
							{"SELECT obj_description($1,$2)", "obj_description", "TABLE t", []any{oid, "pg_class"}, []any{other, "pg_class"}, "whole", "other"},
							{"SELECT obj_description($1)", "obj_description", "TABLE t", []any{oid}, []any{other}, "whole", "other"},
							{"SELECT col_description($1,$2)", "col_description", "COLUMN t.a", []any{oid, 1}, []any{oid, 3}, "first", "third"},
							{"SELECT shobj_description($1,$2)", "shobj_description", "ROLE postgres", []any{roleOID, "pg_authid"}, []any{0, "pg_authid"}, "shared", nil},
							{"SELECT obj_description($1::regclass,$2)", "obj_description", "TABLE t", []any{"t", "pg_class"}, []any{"u", "pg_class"}, "whole", "other"},
							{"SELECT obj_description($1::regclass)", "obj_description", "TABLE t", []any{"t"}, []any{"u"}, "whole", "other"},
							{"SELECT col_description($1::regclass,$2)", "col_description", "COLUMN t.a", []any{"t", 1}, []any{"t", 3}, "first", "third"},
						} {
							// Restore a prescribed initial value: earlier held calls mutate t.
							run(fixture.Exec("COMMENT ON "+tc.target+" IS 'initial'", 0))
							stmt, err := db.Prepare(tc.q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							check := func(args []any, want any) { t.Helper(); heldCommentInquiry(t, stmt, args, backend.PG, tc.label, want) }
							check(tc.first, "initial")
							check(tc.second, tc.other)
							for _, val := range []string{"initial", "replacement", "", " "} {
								run(fixture.Exec("COMMENT ON "+tc.target+" IS '"+val+"'", 0))
								var want any = val
								if val == "" {
									want = nil
								}
								check(tc.first, want)
							}
							args := append([]any(nil), tc.first...)
							args[0] = nil
							check(args, nil)
							args[0] = int64(4294967295)
							check(args, nil)
							if err := stmt.Close(); err != nil {
								t.Fatal(err)
							}
						}
					})
					// The source smoke retained this Context handle across comment
					// removal and a physical attribute hole, not just fresh queries.
					contextHeld, err := db.PrepareContext(p.Ctx, "SELECT col_description($1::regclass,$2)")
					if err != nil {
						t.Fatal(err)
					}
					defer contextHeld.Close()
					for _, tc := range []struct {
						sql  string
						want any
					}{{"'replacement'", "replacement"}, {"NULL", nil}, {"''", nil}, {"' '", " "}} {
						commentInquiryStep(t, p, backend, db, fixture.Exec("COMMENT ON COLUMN t.a IS "+tc.sql, 0))
						rows, err := contextHeld.QueryContext(p.Ctx, "t", 1)
						commentInquiryRows(t, rows, err, backend.PG, false, "col_description", []any{tc.want})
					}
					run(fixture.Exec("ALTER TABLE t DROP b", 0))
					for _, tc := range []struct {
						attribute int
						want      any
					}{{2, nil}, {3, "third"}} {
						rows, err := contextHeld.QueryContext(p.Ctx, "t", tc.attribute)
						commentInquiryRows(t, rows, err, backend.PG, false, "col_description", []any{tc.want})
					}
					for _, tc := range []struct {
						n    int
						want any
					}{{-1, nil}, {2, nil}, {3, "third"}, {99, nil}} {
						run(selectOne("SELECT col_description($1,$2)", "col_description", tc.want, oid, tc.n))
						rows, err := db.Query("SELECT col_description('t'::regclass,$1)", tc.n)
						commentInquiryRows(t, rows, err, backend.PG, false, "col_description", []any{tc.want})
					}
				})
			}
		})
	}
}

func heldCommentInquiry(t *testing.T, stmt *sql.Stmt, args []any, pg bool, label string, want any) {
	t.Helper()
	rows, err := stmt.Query(args...)
	commentInquiryRows(t, rows, err, pg, false, label, []any{want})
}
