package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func searchVisibilityCheckReject(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, i int, k searchVisibilityClass) {
	t.Helper()
	a := searchVisibilityDiscover(t, p, c, k, "sv_a", "v")
	name := fmt.Sprintf("check_%d", i)
	query := fmt.Sprintf("INSERT INTO sv_off.%s VALUES(0),(%d)", name, a)
	s := fixture.ImplicitError(query, fixture.Diagnostic{
		Code: "23514", Message: fmt.Sprintf("new row for relation %q violates check constraint %q", name, "valid_missing"),
		Detail: fmt.Sprintf("Failing row contains (%d).", a), Schema: "sv_off", Table: name, Constraint: "valid_missing",
	})
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id::bigint FROM sv_off."+name, []string{"id"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func TestReferenceSearchVisibilityExpressions(t *testing.T) {
	searchVisibilityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		searchVisibilitySetup(t, p, b, c)
		searchVisibilityStored(t, p, b, c)
		searchVisibilityMatrix(t, p, b, c, true)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=sv_a,sv_b", 0))
		for i, k := range searchVisibilityClasses {
			a := searchVisibilityDiscover(t, p, c, k, "sv_a", "v")
			z := searchVisibilityDiscover(t, p, c, k, "sv_b", "v")
			table := fmt.Sprintf("sv_off.inputs_%d", i)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE "+table+"(seq integer,id oid)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO "+table+" VALUES(1,$1),(2,$1),(3,$2),(4,0),(5,NULL)", 5, a, z))
			call := "pg_catalog." + k.fn + "(id)"
			for _, prepared := range []bool{false, true} {
				run := func(s fixture.Step) {
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				s := fixture.Select("SELECT "+call+" AS visible FROM "+table+" ORDER BY seq", []string{"visible"}, []string{"BOOL"}, [][]any{{true}, {true}, {false}, {nil}, {nil}})
				s.CheckMetadata = fixture.VisibilityBoolMetadata
				run(s)
				for _, tc := range []struct {
					predicate string
					rows      [][]any
				}{
					{call, [][]any{{int64(1)}, {int64(2)}}},
					{"NOT (" + call + ")", [][]any{{int64(3)}}},
					{call + " IS NULL", [][]any{{int64(4)}, {int64(5)}}},
				} {
					run(fixture.Select("SELECT seq FROM "+table+" WHERE "+tc.predicate+" ORDER BY seq", []string{"seq"}, []string{"INT4"}, tc.rows))
				}
				run(fixture.Select("SELECT COALESCE("+call+",FALSE) AS visible FROM "+table+" ORDER BY seq", []string{"visible"}, []string{"BOOL"}, [][]any{{true}, {true}, {false}, {false}, {false}}))
				run(fixture.Select("SELECT CASE WHEN ("+call+" AND TRUE) OR FALSE THEN TRUE ELSE FALSE END AS visible FROM "+table+" ORDER BY seq", []string{"visible"}, []string{"BOOL"}, [][]any{{true}, {true}, {false}, {false}, {false}}))
				empty := fixture.Select("SELECT "+call+" AS visible FROM "+table+" WHERE seq=99", []string{"visible"}, []string{"BOOL"}, [][]any{})
				empty.CheckMetadata = fixture.VisibilityBoolMetadata
				run(empty)
			}
			searchVisibilityCheckReject(t, p, b, c, i, k)
		}
	})
}
