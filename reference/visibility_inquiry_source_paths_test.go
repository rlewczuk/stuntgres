package reference_test

import (
	"database/sql"
	"testing"
)

// Discover the whole unordered candidate bag using the original DB.Query route.
func visibilitySourceCandidatePair(t *testing.T, db *sql.DB, name, first, second string) (int64, int64) {
	t.Helper()
	var bag map[int64]bool
	discover := func(path string) int64 {
		t.Helper()
		r, err := db.Exec("SET search_path=" + path)
		deleteReferenceCount(t, r, err, 0)
		rows, err := db.Query("SELECT oid,pg_table_is_visible(oid) FROM pg_catalog.pg_class WHERE relname='" + name + "'")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		seen := map[int64]bool{}
		var winner int64
		for rows.Next() {
			var id int64
			var visible bool
			if err := rows.Scan(&id, &visible); err != nil {
				t.Fatal(err)
			}
			if id <= 0 || id > 4294967295 {
				t.Fatal("invalid identity", id)
			}
			if _, exists := seen[id]; exists {
				t.Fatal("duplicate identity", id)
			}
			seen[id] = visible
			if visible {
				if winner != 0 {
					t.Fatal("multiple winners", seen)
				}
				winner = id
			}
		}
		if rows.Err() != nil || len(seen) != 2 || winner == 0 {
			t.Fatal("incomplete bag", seen, rows.Err())
		}
		for id := range bag {
			if _, exists := seen[id]; !exists {
				t.Fatal("candidate bag changed", bag, seen)
			}
		}
		bag = seen
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		return winner
	}
	a, b := discover(first), discover(second)
	if a == b {
		t.Fatal("same-name identities coincide", a)
	}
	return a, b
}

func TestReferenceVisibilityInquiryShadowingSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			db.SetMaxOpenConns(1)
			run := func(s referenceStep) {
				s.nonContext = true
				s.checkMetadata = visibilityBoolMetadata
				referenceRunStep(t, p.ctx, db, backend.recorder, s)
			}
			t.Cleanup(func() {
				for _, q := range []string{"SET search_path=public", "DROP SCHEMA IF EXISTS a CASCADE", "DROP SCHEMA IF EXISTS b CASCADE"} {
					if _, err := db.Exec(q); err != nil {
						t.Error(q, err)
					}
				}
			})
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TABLE a.t(id INT)", "CREATE TABLE b.t(id INT)", "CREATE SEQUENCE a.shared", "CREATE VIEW b.shared AS SELECT id FROM b.t", "CREATE SEQUENCE public.temp_s", "CREATE TEMP SEQUENCE temp_s"} {
				run(referenceExec(q, 0))
			}
			a, b := visibilitySourceCandidatePair(t, db, "t", "a,b", "b,a")
			held, err := db.Prepare("SELECT pg_table_is_visible($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			check := func(id int64, want any) {
				rows, err := held.Query(id)
				visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{want})
			}
			for _, tc := range []struct {
				path   string
				av, bv bool
			}{{"a,b", true, false}, {"b,a", false, true}, {"public", false, false}} {
				run(referenceExec("SET search_path="+tc.path, 0))
				check(a, tc.av)
				check(b, tc.bv)
			}
			for _, tc := range []struct {
				path, query string
				a, b        bool
			}{
				{"a,b", "SELECT pg_table_is_visible('a.shared'::regclass),pg_table_is_visible('b.shared'::regclass)", true, false},
				{"b,a", "SELECT pg_table_is_visible('a.shared'::regclass),pg_table_is_visible('b.shared'::regclass)", false, true},
				{"public", "SELECT pg_table_is_visible('public.temp_s'::regclass),pg_table_is_visible('pg_temp.temp_s'::regclass)", false, true},
				{"public,pg_temp", "SELECT pg_table_is_visible('public.temp_s'::regclass),pg_table_is_visible('pg_temp.temp_s'::regclass)", true, false},
			} {
				run(referenceExec("SET search_path="+tc.path, 0))
				run(referenceSelect(tc.query, []string{"pg_table_is_visible", "pg_table_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{tc.a, tc.b}}))
			}
			run(referenceExec("SET search_path=a,b", 0))
			run(referenceExec("ALTER TABLE a.t RENAME TO renamed", 0))
			check(b, true)
			run(referenceExec("ALTER TABLE a.renamed SET SCHEMA public", 0))
			check(a, false)
			run(referenceExec("SET search_path=public", 0))
			check(a, true)
			run(referenceExec("DROP TABLE public.renamed", 0))
			check(a, nil)
			run(referenceExec("CREATE TABLE a.t(id INT)", 0))
			recreated, live := visibilitySourceCandidatePair(t, db, "t", "a,b", "b,a")
			if recreated == a || recreated == b || live != b {
				t.Fatal("recreation identity relationship", recreated, live, a, b)
			}
			check(a, nil)
		})
	}
}
