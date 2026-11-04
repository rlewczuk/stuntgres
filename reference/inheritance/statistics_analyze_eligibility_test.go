package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// These public SQL cases cover the same eligibility rules on the unchanged
// full-native paths: local heap, ordinary inherited flavor and no-heap parent.
func TestReferenceStatisticsAnalyzeEligibility(t *testing.T) {
	for _, kind := range []string{"local", "inherited", "partition"} {
		t.Run(kind, func(t *testing.T) {
			withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
				create := "CREATE TABLE elig_t(id integer,a integer,b integer,label text)"
				if kind == "partition" {
					create += " PARTITION BY RANGE(id)"
				}
				s.run(fixture.Exec(create, 0))
				if kind == "partition" {
					s.run(fixture.Exec("CREATE TABLE elig_low PARTITION OF elig_t FOR VALUES FROM(0) TO(3)", 0))
					s.run(fixture.Exec("CREATE TABLE elig_high PARTITION OF elig_t FOR VALUES FROM(3) TO(5)", 0))
				}
				if kind == "inherited" {
					s.run(fixture.Exec("CREATE TABLE elig_child() INHERITS(elig_t)", 0))
					s.run(fixture.Exec("INSERT INTO elig_t VALUES(1,1,1,'x'),(2,1,1,'x')", 2))
					s.run(fixture.Exec("INSERT INTO elig_child VALUES(3,2,2,'y'),(4,2,2,'y')", 2))
				} else {
					s.run(fixture.Exec("INSERT INTO elig_t VALUES(1,1,1,'x'),(2,1,1,'x'),(3,2,2,'y'),(4,2,2,'y')", 4))
				}
				for _, column := range []string{"id", "a", "b", "label"} {
					s.run(fixture.Exec("ALTER TABLE elig_t ALTER "+column+" SET STATISTICS 1", 0))
				}
				s.run(fixture.Exec("CREATE STATISTICS elig_joint(ndistinct,dependencies,mcv) ON a,b FROM elig_t", 0))
				s.run(fixture.Exec("ALTER STATISTICS elig_joint SET STATISTICS 2", 0))
				s.run(fixture.Exec("CREATE STATISTICS elig_expression ON (a+b) FROM elig_t", 0))
				s.run(fixture.Exec("ANALYZE ONLY elig_t", 0))
				statisticsEligibilityJoint(s, kind, 1, 2)
				joint := statisticsEligibilityPayloads(t, s, "elig_joint")
				// The live groups now differ from the retained baseline. Disabling
				// a required scalar must preserve the complete compatible joint
				// payload (not merely reproduce the same ndistinct number).
				s.run(fixture.Exec("UPDATE elig_t SET b=id", 4))
				s.run(fixture.Select("SELECT a,b FROM elig_t ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(2), int64(3)}, {int64(2), int64(4)}}))
				s.run(fixture.Exec("ALTER TABLE elig_t ALTER a SET STATISTICS 0", 0))
				var notices []fixture.Diagnostic
				_, conn, rec := fixture.NoticeReferencePool(t, s.pair, s.pg, &notices)
				analyze := func(query string, warnings int) {
					t.Helper()
					notices = nil
					step := fixture.Exec(query, 0)
					step.Prepared = s.prepared
					fixture.RunStep(t, s.pair.Ctx, conn, rec, step)
					want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42P17", Message: `statistics object "public.elig_joint" could not be computed for relation "public.elig_t"`, Schema: "public", Table: "elig_t"}
					if len(notices) != warnings {
						t.Fatalf("notices=%+v want %d exact eligibility warnings", notices, warnings)
					}
					for _, notice := range notices {
						if notice != want {
							t.Fatalf("notice=%+v want=%+v", notice, want)
						}
					}
				}
				warnings := 1
				if kind == "inherited" {
					warnings = 2
				}
				// Missing simple-key admission is checked before the object's
				// own target0 skip, while other enabled scalars remain admitted.
				for _, target := range []string{"2", "0"} {
					s.run(fixture.Exec("ALTER STATISTICS elig_joint SET STATISTICS "+target, 0))
					analyze("ANALYZE ONLY elig_t", warnings)
					statisticsEligibilityJoint(s, kind, 1, 2)
					if got := statisticsEligibilityPayloads(t, s, "elig_joint"); !reflect.DeepEqual(got, joint) {
						t.Fatal("disabled required key replaced compatible joint payload", target, got, joint)
					}
				}
				s.run(fixture.Exec("ALTER STATISTICS elig_joint SET STATISTICS 2", 0))
				s.run(fixture.Exec("ALTER TABLE elig_t ALTER a SET STATISTICS 1", 0))
				analyze("ANALYZE ONLY elig_t", 0)
				statisticsEligibilityJoint(s, kind, 2, 4)
				joint = statisticsEligibilityPayloads(t, s, "elig_joint")
				expression := statisticsEligibilityPayloads(t, s, "elig_expression")
				for _, column := range []string{"id", "a", "b", "label"} {
					s.run(fixture.Exec("ALTER TABLE elig_t ALTER "+column+" SET STATISTICS 0", 0))
				}
				s.run(fixture.Exec("UPDATE elig_t SET a=9,b=90", 4))
				analyze("ANALYZE ONLY elig_t", 0)
				if got := statisticsEligibilityPayloads(t, s, "elig_joint"); !reflect.DeepEqual(got, joint) {
					t.Fatal("empty scalar admission replaced joint payload", got, joint)
				}
				if got := statisticsEligibilityPayloads(t, s, "elig_expression"); !reflect.DeepEqual(got, expression) {
					t.Fatal("empty scalar admission replaced old expression payload", got, expression)
				}
				s.run(fixture.Select("SELECT a+b AS value,count(*) FROM elig_t GROUP BY a+b", []string{"value", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(99), int64(4)}}))
				// A genuinely selected enabled label admits expression-only work
				// despite disabled/unselected expression inputs a and b. The joint
				// object stays ineligible and must retain its previous payload.
				s.run(fixture.Exec("ALTER TABLE elig_t ALTER label SET STATISTICS 1", 0))
				analyze("ANALYZE ONLY elig_t(label)", warnings)
				if got := statisticsEligibilityPayloads(t, s, "elig_joint"); !reflect.DeepEqual(got, joint) {
					t.Fatal("selected expression refresh replaced ineligible joint payload", got, joint)
				}
				var rows [][]any
				if kind != "partition" {
					rows = append(rows, []any{false, "{99}", "{1}"})
				}
				if kind != "local" {
					rows = append(rows, []any{true, "{99}", "{1}"})
				}
				s.run(fixture.Select("SELECT d.stxdinherit,(stxdexpr[1]).stavalues1::text AS values,(stxdexpr[1]).stanumbers1::text AS frequencies FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='elig_expression' ORDER BY d.stxdinherit", []string{"stxdinherit", "values", "frequencies"}, []string{"BOOL", "TEXT", "TEXT"}, rows))
			})
		})
	}
}

func statisticsEligibilityJoint(s ndistinctScenario, kind string, local, complete int) {
	var rows [][]any
	if kind != "partition" {
		groups := complete
		if kind == "inherited" {
			groups = local
		}
		rows = append(rows, []any{false, fmt.Sprintf(`{"2, 3": %d}`, groups)})
	}
	if kind != "local" {
		rows = append(rows, []any{true, fmt.Sprintf(`{"2, 3": %d}`, complete)})
	}
	s.run(fixture.Select("SELECT d.stxdinherit,d.stxdndistinct::text AS distincts FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='elig_joint' ORDER BY d.stxdinherit", []string{"stxdinherit", "distincts"}, []string{"BOOL", "TEXT"}, rows))
}

type statisticsEligibilityPayload struct {
	inherited  bool
	distinct   sql.NullString
	dependency sql.NullString
	mcv        sql.NullString
	expression sql.NullString
}

func statisticsEligibilityPayloads(t *testing.T, s ndistinctScenario, name string) []statisticsEligibilityPayload {
	t.Helper()
	rows, err := s.conn.QueryContext(s.pair.Ctx, "SELECT d.stxdinherit,d.stxdndistinct::text,d.stxddependencies::text,d.stxdmcv::text,d.stxdexpr::text FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname=$1 ORDER BY d.stxdinherit", name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []statisticsEligibilityPayload
	for rows.Next() {
		var payload statisticsEligibilityPayload
		if err := rows.Scan(&payload.inherited, &payload.distinct, &payload.dependency, &payload.mcv, &payload.expression); err != nil {
			t.Fatal(err)
		}
		if name == "elig_expression" && (!payload.expression.Valid || payload.expression.String == "") {
			t.Fatal("missing genuine expression summary", payload)
		}
		if name == "elig_joint" && (!payload.distinct.Valid || !payload.dependency.Valid || !payload.mcv.Valid) {
			t.Fatal("missing genuine joint ndistinct/dependency/MCV payload", payload)
		}
		result = append(result, payload)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(result) == 0 {
		t.Fatal("missing collected payload", name)
	}
	return result
}
