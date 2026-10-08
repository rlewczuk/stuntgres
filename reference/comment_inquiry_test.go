//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

// Oracle can be selected alone before the native implementation exists. Both
// backends must satisfy the same independent constants, not just each other.
func TestReferenceCommentInquiry(t *testing.T) {
	for _, backend := range []string{"Oracle", "Native"} {
		t.Run(backend, func(t *testing.T) {
			p := newReferencePair(t)
			db, rec := p.postgres, p.postgresRecord
			if backend == "Native" {
				db, rec = p.memory, p.memoryRecord
			}
			run := func(s referenceStep) { t.Helper(); referenceRunStep(t, p.ctx, db, rec, s) }
			for _, q := range []string{"CREATE TABLE t(a INT,b TEXT,c BIGINT)", "CREATE TABLE u(a INT)", "COMMENT ON TABLE t IS 'whole'", "COMMENT ON TABLE u IS 'other'", "COMMENT ON COLUMN t.a IS 'first'", "COMMENT ON COLUMN t.b IS 'second'", "COMMENT ON COLUMN t.c IS 'third'", "COMMENT ON ROLE postgres IS 'shared'"} {
				run(referenceExec(q, 0))
			}
			var oid, other int64
			if err := db.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='t'").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='u'").Scan(&other); err != nil {
				t.Fatal(err)
			}
			roleOID := int64(10)
			if backend == "Oracle" {
				if err := db.QueryRowContext(p.ctx, "SELECT oid FROM pg_roles WHERE rolname='postgres'").Scan(&roleOID); err != nil {
					t.Fatal(err)
				}
			}
			selectOne := func(q, label string, want any, args ...any) referenceStep {
				return referenceSelect(q, []string{label}, []string{"TEXT"}, [][]any{{want}}, args...)
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
				{"col_description(NULL,1)", "col_description", nil}, {"col_description(1,NULL)", "col_description", nil},
				{"obj_description(NULL)", "obj_description", nil}, {"obj_description(NULL,'pg_class')", "obj_description", nil}, {"obj_description(1,NULL)", "obj_description", nil},
				{"shobj_description(NULL,'pg_authid')", "shobj_description", nil}, {"shobj_description(1,NULL)", "shobj_description", nil},
				{"obj_description(4294967295::bigint,'pg_class')", "obj_description", nil},
				{"pg_catalog.col_description('t'::regclass,1)", "col_description", "first"},
			} {
				s := selectOne("SELECT "+tc.expr, tc.label, tc.want)
				run(s)
				run(referencePrepared(s))
			}
			for _, cat := range []string{"PG_CLASS", "pg_catalog.pg_class", "\"pg_class\"", " pg_class", "", "missing", "pg_namespace"} {
				run(selectOne("SELECT obj_description($1,$2)", "obj_description", nil, oid, cat))
			}
			run(referenceExec("CREATE TABLE inquiry_smallint(v SMALLINT)", 0))
			run(referenceExec("INSERT INTO inquiry_smallint VALUES(1)", 1))
			smallint := selectOne("SELECT obj_description(v,'pg_class') FROM inquiry_smallint", "obj_description", nil)
			run(smallint)
			run(referencePrepared(smallint))
			for _, cast := range []string{"integer", "bigint", "oid"} {
				run(selectOne(fmt.Sprintf("SELECT obj_description(1::%s,'pg_class')", cast), "obj_description", nil))
			}
			run(selectOne("SELECT obj_description('1'::regclass,'pg_class')", "obj_description", nil))
			run(referenceExec("INSERT INTO t(a) VALUES(1),(2)", 2))
			lazyCase := referenceSelect("SELECT CASE WHEN a=1 THEN obj_description('t'::regclass,'pg_class') ELSE col_description(NULL,1) END AS description FROM t ORDER BY a", []string{"description"}, []string{"TEXT"}, [][]any{{"whole"}, {nil}})
			run(lazyCase)
			run(referencePrepared(lazyCase))
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
				} {
					// Restore a prescribed initial value: earlier held calls mutate t.
					run(referenceExec("COMMENT ON "+tc.target+" IS 'initial'", 0))
					stmt, err := db.PrepareContext(p.ctx, tc.q)
					if err != nil {
						t.Fatal(err)
					}
					check := func(args []any, want any) { t.Helper(); heldCommentInquiry(t, stmt, args, tc.label, want) }
					check(tc.first, "initial")
					check(tc.second, tc.other)
					for _, val := range []string{"replacement", "", " "} {
						run(referenceExec("COMMENT ON "+tc.target+" IS '"+val+"'", 0))
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
			run(referenceExec("ALTER TABLE t DROP b", 0))
			for _, tc := range []struct {
				n    int
				want any
			}{{-1, nil}, {2, nil}, {3, "third"}, {99, nil}} {
				run(selectOne("SELECT col_description($1,$2)", "col_description", tc.want, oid, tc.n))
			}
		})
	}
}

func heldCommentInquiry(t *testing.T, stmt *sql.Stmt, args []any, label string, want any) {
	t.Helper()
	rows, err := stmt.Query(args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || len(cols) != 1 || cols[0] != label {
		t.Fatal(cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "TEXT" {
		t.Fatal(types, err)
	}
	if !rows.Next() {
		t.Fatal("missing row", rows.Err())
	}
	var got any
	if err := rows.Scan(&got); err != nil {
		t.Fatal(err)
	}
	if referenceNormalize(got) != want {
		t.Fatal("independent held value", got, want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra/error row", rows.Err())
	}
}
