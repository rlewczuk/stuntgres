package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVisibilityInquiryFixedAndTyped(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(fmt.Sprint(source), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db, rec := backend.DB, backend.Recorder
					run := func(s fixture.Step) {
						t.Helper()
						s.CheckMetadata = fixture.VisibilityBoolMetadata
						s.NonContext = source
						fixture.RunStep(t, p.Ctx, db, rec, s)
					}
					for _, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
						var held *sql.Stmt
						var err error
						if source {
							held, err = db.Prepare("SELECT pg_catalog." + fn + "($1)")
						} else {
							held, err = db.PrepareContext(p.Ctx, "SELECT pg_catalog."+fn+"($1)")
						}
						if err != nil {
							t.Fatal(err)
						}
						for _, arg := range []any{nil, int64(0), int64(4294967295)} {
							s := fixture.Select("SELECT pg_catalog."+fn+"($1)", []string{fn}, []string{"BOOL"}, [][]any{{nil}}, arg)
							run(s)
							if !source {
								run(fixture.Prepared(s))
							}
							var rows *sql.Rows
							if source {
								rows, err = held.Query(arg)
							} else {
								rows, err = held.QueryContext(p.Ctx, arg)
							}
							visibilityInquiryRows(t, rows, err, fn, []any{nil})
						}
						if err := held.Close(); err != nil {
							t.Fatal(err)
						}
					}
					if source {
						for _, tc := range []struct {
							fn   string
							oid  int64
							want any
						}{
							{"pg_table_is_visible", 1259, true}, {"pg_type_is_visible", 23, true}, {"pg_type_is_visible", 705, true}, {"pg_type_is_visible", 2278, true},
							{"pg_collation_is_visible", 100, true}, {"pg_collation_is_visible", 811, true}, {"pg_collation_is_visible", 6411, true},
							{"pg_conversion_is_visible", 4402, true}, {"pg_conversion_is_visible", 4518, true}, {"pg_conversion_is_visible", 4531, true},
							{"pg_conversion_is_visible", 4450, nil}, {"pg_conversion_is_visible", 4451, nil}, {"pg_table_is_visible", 23, nil},
							{"pg_type_is_visible", 950, nil}, {"pg_collation_is_visible", 4402, nil}, {"pg_conversion_is_visible", 950, nil},
						} {
							rows, err := db.Query("SELECT pg_catalog."+tc.fn+"($1)", tc.oid)
							visibilityInquiryRows(t, rows, err, tc.fn, []any{tc.want})
						}
					}
					run(fixture.Select("SELECT pg_catalog.pg_table_is_visible($1)", []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{true}}, int64(1259)))
					for _, id := range []int{16, 17, 18, 19, 1003, 20, 21, 23, 25, 26, 1042, 1043, 700, 701, 1082, 1083, 1114, 1184, 1186, 1266, 1700, 2950, 2205, 2278, 705} {
						s := fixture.Select("SELECT pg_type_is_visible($1)", []string{"pg_type_is_visible"}, []string{"BOOL"}, [][]any{{true}}, id)
						run(s)
						if !source {
							run(fixture.Prepared(s))
						}
					}
					for _, id := range []int{100, 950, 951, 962, 963, 811, 6411} {
						s := fixture.Select("SELECT pg_collation_is_visible($1)", []string{"pg_collation_is_visible"}, []string{"BOOL"}, [][]any{{true}}, id)
						run(s)
						if !source {
							run(fixture.Prepared(s))
						}
					}
					for id := 4402; id <= 4531; id++ {
						var want any = true
						if id == 4450 || id == 4451 {
							want = nil
						}
						s := fixture.Select("SELECT pg_conversion_is_visible($1)", []string{"pg_conversion_is_visible"}, []string{"BOOL"}, [][]any{{want}}, id)
						run(s)
						if !source {
							run(fixture.Prepared(s))
						}
					}
					for _, tc := range []struct {
						fn string
						id int
					}{{"pg_table_is_visible", 23}, {"pg_type_is_visible", 950}, {"pg_collation_is_visible", 4402}, {"pg_conversion_is_visible", 950}} {
						run(fixture.Select("SELECT "+tc.fn+"($1)", []string{tc.fn}, []string{"BOOL"}, [][]any{{nil}}, tc.id))
					}
					run(fixture.Exec("CREATE TABLE visibility_ids(id OID)", 0))
					run(fixture.Exec("INSERT INTO visibility_ids VALUES(23),(23),(0),(NULL)", 4))
					run(fixture.Exec("CREATE VIEW visibility_filter AS SELECT id,pg_type_is_visible(id) AS visible FROM visibility_ids", 0))
					for _, selected := range []fixture.Step{
						fixture.Select("SELECT visible,count(*) FROM visibility_filter GROUP BY visible ORDER BY visible", []string{"visible", "count"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(2)}, {nil, int64(2)}}),
						fixture.Select("SELECT COALESCE(visible,FALSE) FROM visibility_filter ORDER BY id", []string{"coalesce"}, []string{"BOOL"}, [][]any{{false}, {true}, {true}, {false}}),
						fixture.Select("SELECT CASE WHEN id=23 THEN visible ELSE FALSE END AS outcome FROM visibility_filter ORDER BY id", []string{"outcome"}, []string{"BOOL"}, [][]any{{false}, {true}, {true}, {false}}),
						fixture.Select("SELECT visible IS NULL AS absent FROM visibility_filter ORDER BY id", []string{"absent"}, []string{"BOOL"}, [][]any{{true}, {false}, {false}, {true}}),
					} {
						run(selected)
						if !source {
							run(fixture.Prepared(selected))
						}
					}
					s := fixture.Select("SELECT id::bigint FROM visibility_filter WHERE visible", []string{"id"}, []string{"INT8"}, [][]any{{int64(23)}, {int64(23)}})
					run(s)
					if !source {
						run(fixture.Prepared(s))
					}
					if backend.PG {
						run(fixture.Exec("DROP VIEW visibility_filter", 0))
						run(fixture.Exec("DROP TABLE visibility_ids", 0))
					} else if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestReferenceVisibilityInquiryRelationPaths(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db, rec := backend.DB, backend.Recorder
			conn, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			t.Cleanup(func() {
				for _, q := range []string{"SET search_path=public", "DROP SCHEMA IF EXISTS pgv_a CASCADE", "DROP SCHEMA IF EXISTS pgv_b CASCADE"} {
					if _, err := db.ExecContext(p.Ctx, q); err != nil {
						t.Error(q, err)
					}
				}
			})
			run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, conn, rec, s) }
			for _, q := range []string{"CREATE SCHEMA pgv_a", "CREATE SCHEMA pgv_b", "CREATE TABLE pgv_a.pgv_visibility_candidates(id INT)", "CREATE TABLE pgv_b.pgv_visibility_candidates(id INT)", "CREATE SEQUENCE pgv_a.shared", "CREATE VIEW pgv_b.shared AS SELECT id FROM pgv_b.pgv_visibility_candidates"} {
				run(fixture.Exec(q, 0))
			}
			var candidates map[int64]bool
			discover := func(path string) int64 {
				t.Helper()
				run(fixture.Exec("SET search_path="+path, 0))
				rows, err := conn.QueryContext(p.Ctx, "SELECT oid,pg_table_is_visible(oid) FROM pg_catalog.pg_class WHERE relname='pgv_visibility_candidates'")
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
			stmt, err := conn.PrepareContext(p.Ctx, "SELECT pg_table_is_visible($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, tc := range []struct {
				path string
				a, b bool
			}{{"pgv_a,pgv_b", true, false}, {"pgv_b,pgv_a", false, true}, {"public", false, false}} {
				run(fixture.Exec("SET search_path="+tc.path, 0))
				for _, object := range []struct {
					oid  int64
					want bool
				}{{a, tc.a}, {b, tc.b}} {
					run(fixture.Select("SELECT pg_table_is_visible($1)", []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{object.want}}, object.oid))
					var got any
					if err := stmt.QueryRowContext(p.Ctx, object.oid).Scan(&got); err != nil || got != object.want {
						t.Fatal(got, object.want, err)
					}
					rows, err := stmt.QueryContext(p.Ctx, object.oid)
					visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{object.want})
				}
				run(fixture.Select("SELECT pg_table_is_visible('pgv_a.shared'::regclass),pg_table_is_visible('pgv_b.shared'::regclass)", []string{"pg_table_is_visible", "pg_table_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{tc.a, tc.b}}))
			}
			run(fixture.Exec("SET search_path=pgv_a,pgv_b", 0))
			run(fixture.Exec("ALTER TABLE pgv_a.pgv_visibility_candidates RENAME TO renamed", 0))
			run(fixture.Select("SELECT pg_table_is_visible($1)", []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{true}}, b))
			rows, err := stmt.QueryContext(p.Ctx, b)
			visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{true})
			run(fixture.Exec("ALTER TABLE pgv_a.renamed SET SCHEMA public", 0))
			rows, err = stmt.QueryContext(p.Ctx, a)
			visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{false})
			run(fixture.Exec("SET search_path=public", 0))
			rows, err = stmt.QueryContext(p.Ctx, a)
			visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{true})
			run(fixture.Exec("DROP TABLE public.renamed", 0))
			run(fixture.Select(fmt.Sprintf("SELECT pg_table_is_visible(%d::oid)", a), []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{nil}}))
			run(fixture.Exec("CREATE TABLE pgv_a.pgv_visibility_candidates(id INT)", 0))
			candidates = nil
			recreated := discover("pgv_a,pgv_b")
			if recreated == a || recreated == b {
				t.Fatal("recreated relation reused identity", recreated, a, b)
			}
			rows, err = stmt.QueryContext(p.Ctx, a)
			visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{nil})
		})
	}
}
