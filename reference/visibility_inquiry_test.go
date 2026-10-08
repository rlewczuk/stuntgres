//go:build reference

package reference_test

import (
	"fmt"
	"testing"

	"stuntgres"
)

func TestReferenceVisibilityInquiryFixedAndTyped(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []string{"Oracle", "Native"} {
		t.Run(backend, func(t *testing.T) {
			db, rec := p.postgres, p.postgresRecord
			if backend == "Native" {
				db, rec = p.memory, p.memoryRecord
			}
			run := func(s referenceStep) { t.Helper(); referenceRunStep(t, p.ctx, db, rec, s) }
			for _, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
				for _, arg := range []any{nil, int64(0), int64(4294967295)} {
					s := referenceSelect("SELECT pg_catalog."+fn+"($1)", []string{fn}, []string{"BOOL"}, [][]any{{nil}}, arg)
					run(s)
					run(referencePrepared(s))
				}
			}
			for _, id := range []int{16, 17, 18, 19, 1003, 20, 21, 23, 25, 26, 1042, 1043, 700, 701, 1082, 1083, 1114, 1184, 1186, 1266, 1700, 2950, 2205, 2278, 705} {
				s := referenceSelect("SELECT pg_type_is_visible($1)", []string{"pg_type_is_visible"}, []string{"BOOL"}, [][]any{{true}}, id)
				run(s)
				run(referencePrepared(s))
			}
			for _, id := range []int{100, 950, 951, 962, 963, 811, 6411} {
				s := referenceSelect("SELECT pg_collation_is_visible($1)", []string{"pg_collation_is_visible"}, []string{"BOOL"}, [][]any{{true}}, id)
				run(s)
				run(referencePrepared(s))
			}
			for id := 4402; id <= 4531; id++ {
				var want any = true
				if id == 4450 || id == 4451 {
					want = nil
				}
				s := referenceSelect("SELECT pg_conversion_is_visible($1)", []string{"pg_conversion_is_visible"}, []string{"BOOL"}, [][]any{{want}}, id)
				run(s)
				run(referencePrepared(s))
			}
			for _, tc := range []struct {
				fn string
				id int
			}{{"pg_table_is_visible", 23}, {"pg_type_is_visible", 950}, {"pg_collation_is_visible", 4402}, {"pg_conversion_is_visible", 950}} {
				run(referenceSelect("SELECT "+tc.fn+"($1)", []string{tc.fn}, []string{"BOOL"}, [][]any{{nil}}, tc.id))
			}
			run(referenceExec("CREATE TABLE visibility_ids(id OID)", 0))
			run(referenceExec("INSERT INTO visibility_ids VALUES(23),(23),(0),(NULL)", 4))
			run(referenceExec("CREATE VIEW visibility_filter AS SELECT id,pg_type_is_visible(id) AS visible FROM visibility_ids", 0))
			for _, selected := range []referenceStep{
				referenceSelect("SELECT visible,count(*) FROM visibility_filter GROUP BY visible ORDER BY visible", []string{"visible", "count"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(2)}, {nil, int64(2)}}),
				referenceSelect("SELECT COALESCE(visible,FALSE) FROM visibility_filter ORDER BY id", []string{"coalesce"}, []string{"BOOL"}, [][]any{{false}, {true}, {true}, {false}}),
				referenceSelect("SELECT CASE WHEN id=23 THEN visible ELSE FALSE END AS outcome FROM visibility_filter ORDER BY id", []string{"outcome"}, []string{"BOOL"}, [][]any{{false}, {true}, {true}, {false}}),
				referenceSelect("SELECT visible IS NULL AS absent FROM visibility_filter ORDER BY id", []string{"absent"}, []string{"BOOL"}, [][]any{{true}, {false}, {false}, {true}}),
			} {
				run(selected)
				run(referencePrepared(selected))
			}
			s := referenceSelect("SELECT id::bigint FROM visibility_filter WHERE visible", []string{"id"}, []string{"INT8"}, [][]any{{int64(23)}, {int64(23)}})
			run(s)
			run(referencePrepared(s))
			if backend == "Oracle" {
				run(referenceExec("DROP VIEW visibility_filter", 0))
				run(referenceExec("DROP TABLE visibility_ids", 0))
			} else if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReferenceVisibilityInquiryRelationPaths(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []string{"Oracle", "Native"} {
		t.Run(backend, func(t *testing.T) {
			db, rec := p.postgres, p.postgresRecord
			if backend == "Native" {
				db, rec = p.memory, p.memoryRecord
			}
			conn, err := db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer func() {
				for _, q := range []string{"SET search_path=public", "DROP SCHEMA pgv_a CASCADE", "DROP SCHEMA pgv_b CASCADE"} {
					if _, err := conn.ExecContext(p.ctx, q); err != nil {
						t.Error(q, err)
					}
				}
			}()
			run := func(s referenceStep) { t.Helper(); referenceRunStep(t, p.ctx, conn, rec, s) }
			for _, q := range []string{"CREATE SCHEMA pgv_a", "CREATE SCHEMA pgv_b", "CREATE TABLE pgv_a.pgv_visibility_candidates(id INT)", "CREATE TABLE pgv_b.pgv_visibility_candidates(id INT)", "CREATE SEQUENCE pgv_a.shared", "CREATE VIEW pgv_b.shared AS SELECT id FROM pgv_b.pgv_visibility_candidates"} {
				run(referenceExec(q, 0))
			}
			var candidates map[int64]bool
			discover := func(path string) int64 {
				t.Helper()
				run(referenceExec("SET search_path="+path, 0))
				rows, err := conn.QueryContext(p.ctx, "SELECT oid,pg_table_is_visible(oid) FROM pg_catalog.pg_class WHERE relname='pgv_visibility_candidates'")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var found int64
				seen := make(map[int64]bool)
				for rows.Next() {
					var oid int64
					var visible bool
					if err := rows.Scan(&oid, &visible); err != nil {
						t.Fatal(err)
					}
					if oid <= 0 || oid > 4294967295 {
						t.Fatal("invalid relation identity", oid)
					}
					if _, duplicate := seen[oid]; duplicate {
						t.Fatal("duplicate candidate identity", oid)
					}
					seen[oid] = visible
					if visible {
						if found != 0 {
							t.Fatal("multiple visible same-name relations")
						}
						found = oid
					}
				}
				if rows.Err() != nil || found == 0 {
					t.Fatal("missing visible relation", path, rows.Err())
				}
				if len(seen) != 2 {
					t.Fatal("expected exactly two relation candidates", seen)
				}
				if candidates != nil {
					for oid := range candidates {
						if _, exists := seen[oid]; !exists {
							t.Fatal("candidate identity bag changed", candidates, seen)
						}
					}
				}
				candidates = seen
				return found
			}
			a, b := discover("pgv_a,pgv_b"), discover("pgv_b,pgv_a")
			if a == 0 || b == 0 || a == b {
				t.Fatal("invalid independent relation identities", a, b)
			}
			stmt, err := conn.PrepareContext(p.ctx, "SELECT pg_table_is_visible($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, tc := range []struct {
				path string
				a, b bool
			}{{"pgv_a,pgv_b", true, false}, {"pgv_b,pgv_a", false, true}, {"public", false, false}} {
				run(referenceExec("SET search_path="+tc.path, 0))
				for _, object := range []struct {
					oid  int64
					want bool
				}{{a, tc.a}, {b, tc.b}} {
					run(referenceSelect("SELECT pg_table_is_visible($1)", []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{object.want}}, object.oid))
					var got any
					if err := stmt.QueryRowContext(p.ctx, object.oid).Scan(&got); err != nil || got != object.want {
						t.Fatal(got, object.want, err)
					}
				}
				run(referenceSelect("SELECT pg_table_is_visible('pgv_a.shared'::regclass),pg_table_is_visible('pgv_b.shared'::regclass)", []string{"pg_table_is_visible", "pg_table_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{tc.a, tc.b}}))
			}
			run(referenceExec("SET search_path=pgv_a,pgv_b", 0))
			run(referenceExec("ALTER TABLE pgv_a.pgv_visibility_candidates RENAME TO renamed", 0))
			run(referenceSelect("SELECT pg_table_is_visible($1)", []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{true}}, b))
			run(referenceExec("DROP TABLE pgv_a.renamed", 0))
			run(referenceSelect(fmt.Sprintf("SELECT pg_table_is_visible(%d::oid)", a), []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{nil}}))
		})
	}
}
