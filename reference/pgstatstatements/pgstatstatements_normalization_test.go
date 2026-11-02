package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsNormalization(t *testing.T) {
	t.Run("first_text_and_literals", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			s.exec("CREATE TABLE pgss_part.pgss_norm_literal(k integer)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_norm_literal VALUES(1),(2)", 2))
			pattern := "SELECT /* pgss 雪 */ k AS pgss_literal FROM pgss_part.pgss_norm_literal%"
			s.counts(pattern, 0, 0, 0)
			for _, tc := range []struct {
				q string
				v int64
			}{
				{"SELECT /* pgss 雪 */ k AS pgss_literal FROM pgss_part.pgss_norm_literal WHERE k=1", 1},
				{"SELECT /* later */ k AS pgss_literal FROM pgss_part.pgss_norm_literal WHERE k=2", 2},
			} {
				s.work(fixture.Select(tc.q, []string{"pgss_literal"}, []string{"INT4"}, [][]any{{tc.v}}))
			}
			s.counts(pattern, 1, 2, 2)
			s.entry("SELECT /* pgss 雪 */ k AS pgss_literal FROM pgss_part.pgss_norm_literal WHERE k=$1", 2, 2)
		})
	})
	t.Run("direct_and_held_parameters", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			s.exec("CREATE TABLE pgss_part.pgss_norm_prepared(k integer)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_norm_prepared VALUES(1),(2)", 2))
			q := "SELECT k AS pgss_prepared FROM pgss_part.pgss_norm_prepared WHERE k=$1::integer"
			s.counts(q, 0, 0, 0)
			held, err := s.worker.PrepareContext(s.p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			// Parse/Describe is not an execution, even when retained for reuse.
			s.counts(q, 0, 0, 0)
			for _, value := range []int64{1, 2} {
				step := fixture.Select(q, []string{"pgss_prepared"}, []string{"INT4"}, [][]any{{value}}, value)
				s.work(step)
				fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.worker, Statement: held, SQL: q}, s.b.Recorder, step)
			}
			s.counts(q, 1, 4, 4)
			s.entry(q, 4, 4)
		})
	})
	t.Run("constant_kinds_and_parameter_numbering", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			s.exec("CREATE TABLE pgss_part.pgss_norm_constants(k integer)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_norm_constants VALUES(1)", 1))
			pattern := "SELECT $1::integer AS supplied,% FROM pgss_part.pgss_norm_constants"
			s.counts(pattern, 0, 0, 0)
			q1 := "SELECT $1::integer AS supplied,-7::integer AS negative,NULL::text AS absent,'雪'::text AS label FROM pgss_part.pgss_norm_constants"
			q2 := "SELECT $1::integer AS supplied,-9::integer AS negative,NULL::text AS absent,'été'::text AS label FROM pgss_part.pgss_norm_constants"
			s.work(fixture.Select(q1, []string{"supplied", "negative", "absent", "label"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, [][]any{{int64(3), int64(-7), nil, "雪"}}, int64(3)))
			s.work(fixture.Prepared(fixture.Select(q2, []string{"supplied", "negative", "absent", "label"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, [][]any{{int64(5), int64(-9), nil, "été"}}, int64(5))))
			s.counts(pattern, 1, 2, 2)
			s.entry("SELECT $1::integer AS supplied,-$2::integer AS negative,$3::text AS absent,$4::text AS label FROM pgss_part.pgss_norm_constants", 2, 2)
		})
	})
	t.Run("constant_lists", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			s.exec("CREATE TABLE pgss_part.pgss_norm_list(k integer)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_norm_list VALUES(1),(2),(3)", 3))
			pattern := "SELECT k AS pgss_list FROM pgss_part.pgss_norm_list%"
			s.counts(pattern, 0, 0, 0)
			s.work(fixture.Select("SELECT k AS pgss_list FROM pgss_part.pgss_norm_list WHERE k IN (1,2) ORDER BY k", []string{"pgss_list"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			s.work(fixture.Select("SELECT k AS pgss_list FROM pgss_part.pgss_norm_list WHERE k IN (1,2,3) ORDER BY k", []string{"pgss_list"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
			s.counts(pattern, 1, 2, 5)
			s.entry("SELECT k AS pgss_list FROM pgss_part.pgss_norm_list WHERE k IN ($1 /*, ... */) ORDER BY k", 2, 5)
		})
	})
	t.Run("negative_last_constant_list", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			s.exec("CREATE TABLE pgss_part.pgss_norm_negative_list(k integer)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_norm_negative_list VALUES(1),(-2),(-3)", 3))
			pattern := "SELECT k AS pgss_negative_list FROM pgss_part.pgss_norm_negative_list%"
			s.counts(pattern, 0, 0, 0)
			s.work(fixture.Select("SELECT k AS pgss_negative_list FROM pgss_part.pgss_norm_negative_list WHERE k IN (1,-2) ORDER BY k", []string{"pgss_negative_list"}, []string{"INT4"}, [][]any{{int64(-2)}, {int64(1)}}))
			s.work(fixture.Prepared(fixture.Select("SELECT k AS pgss_negative_list FROM pgss_part.pgss_norm_negative_list WHERE k IN (1,-2,-3) ORDER BY k", []string{"pgss_negative_list"}, []string{"INT4"}, [][]any{{int64(-3)}, {int64(-2)}, {int64(1)}})))
			s.counts(pattern, 1, 2, 5)
			s.entry("SELECT k AS pgss_negative_list FROM pgss_part.pgss_norm_negative_list WHERE k IN ($1 /*, ... */) ORDER BY k", 2, 5)
		})
	})
	t.Run("mixed_explicit_parameter_and_constant_list", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			s.exec("CREATE TABLE pgss_part.pgss_norm_mixed_params(k integer)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_norm_mixed_params VALUES(1),(2),(3)", 3))
			pattern := "SELECT k AS pgss_mixed FROM pgss_part.pgss_norm_mixed_params%"
			s.counts(pattern, 0, 0, 0)
			s.work(fixture.Select("SELECT k AS pgss_mixed FROM pgss_part.pgss_norm_mixed_params WHERE k IN (1,2) AND k > $1::integer ORDER BY k", []string{"pgss_mixed"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, int64(0)))
			s.work(fixture.Prepared(fixture.Select("SELECT k AS pgss_mixed FROM pgss_part.pgss_norm_mixed_params WHERE k IN (1,2,3) AND k > $1::integer ORDER BY k", []string{"pgss_mixed"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}, int64(0))))
			s.counts(pattern, 1, 2, 5)
			// A squashed list activates released source-order renumbering of
			// external parameters too, rather than preserving the original $1.
			s.entry("SELECT k AS pgss_mixed FROM pgss_part.pgss_norm_mixed_params WHERE k IN ($1 /*, ... */) AND k > $2::integer ORDER BY k", 2, 5)
		})
	})
	t.Run("comma_from_and_cross_join_structure", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			s.exec("CREATE TABLE pgss_part.pgss_norm_join(k integer)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_norm_join VALUES(7)", 1))
			queries := []string{
				"SELECT a.k AS pgss_join_left,b.k AS pgss_join_right FROM pgss_part.pgss_norm_join a, pgss_part.pgss_norm_join b",
				"SELECT a.k AS pgss_join_left,b.k AS pgss_join_right FROM pgss_part.pgss_norm_join a CROSS JOIN pgss_part.pgss_norm_join b",
			}
			pattern := "SELECT a.k AS pgss_join_left,b.k AS pgss_join_right FROM pgss_part.pgss_norm_join a%"
			s.counts(pattern, 0, 0, 0)
			for i, query := range queries {
				step := fixture.Select(query, []string{"pgss_join_left", "pgss_join_right"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}})
				if i == 1 {
					step = fixture.Prepared(step)
				}
				s.work(step)
				s.entry(query, 1, 1)
			}
			// PostgreSQL retains FROM-list versus explicit JoinExpr structure,
			// even though these two genuine queries return identical rows.
			s.counts(pattern, 2, 2, 2)
		})
	})
	t.Run("resolved_relations_aliases_and_types", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			for i, name := range []string{"pgss_norm_left", "pgss_norm_right"} {
				s.exec("CREATE TABLE pgss_part." + name + "(k integer)")
				s.run(fixture.Exec("INSERT INTO pgss_part."+name+" VALUES($1)", 1, int64(i+11)))
			}
			pattern := "SELECT % AS pgss_semantic FROM pgss_part.pgss_norm_%"
			s.counts(pattern, 0, 0, 0)
			cases := []struct {
				q, typ string
				value  int64
			}{
				{"SELECT a.k AS pgss_semantic FROM pgss_part.pgss_norm_left a", "INT4", 11},
				{"SELECT b.k AS pgss_semantic FROM pgss_part.pgss_norm_left b", "INT4", 11},
				{"SELECT a.k AS pgss_semantic FROM pgss_part.pgss_norm_right a", "INT4", 12},
				{"SELECT a.k::bigint AS pgss_semantic FROM pgss_part.pgss_norm_left a", "INT8", 11},
			}
			for _, tc := range cases {
				s.work(fixture.Select(tc.q, []string{"pgss_semantic"}, []string{tc.typ}, [][]any{{tc.value}}))
			}
			// PG18 hashes the effective unqualified reference name, not the
			// relation OID/schema. Both alias-a references deliberately merge.
			s.entry(cases[0].q, 2, 2)
			s.entry(cases[1].q, 1, 1)
			s.counts(cases[2].q, 0, 0, 0)
			s.entry(cases[3].q, 1, 1)
			s.counts(pattern, 3, 4, 4)
			s.both(fixture.Select("SELECT count(DISTINCT queryid) AS identities FROM pgss_part.pg_stat_statements WHERE query LIKE $1", []string{"identities"}, []string{"INT8"}, [][]any{{int64(3)}}, pattern))
			pattern = "SELECT k AS pgss_relation_name FROM pgss_part.pgss_norm_%"
			s.counts(pattern, 0, 0, 0)
			for i, name := range []string{"pgss_norm_left", "pgss_norm_right"} {
				q := "SELECT k AS pgss_relation_name FROM pgss_part." + name
				s.work(fixture.Select(q, []string{"pgss_relation_name"}, []string{"INT4"}, [][]any{{int64(i + 11)}}))
				s.entry(q, 1, 1)
			}
			s.counts(pattern, 2, 2, 2)
		})
	})
}
