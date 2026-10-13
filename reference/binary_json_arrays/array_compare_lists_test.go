package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayCompareListTruth(t *testing.T) {
	arrayCompareScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, left, list string
			in, notIn        any
		}{
			{"match", "1", "1,2", true, false},
			{"miss", "3", "1,2", false, true},
			{"singleton", "1", "1", true, false},
			{"singleton_miss", "2", "1", false, true},
			{"repeated", "1", "1,1,1", true, false},
			{"null_first_match", "1", "NULL,1", true, false},
			{"null_last_match", "1", "1,NULL", true, false},
			{"null_first_miss", "3", "NULL,1", nil, nil},
			{"null_last_miss", "3", "1,NULL", nil, nil},
			{"null_only", "1", "NULL", nil, nil},
			{"null_left", "NULL::INT", "1,2", nil, nil},
			{"all_null", "NULL", "NULL", nil, nil},
			{"null_left_mixed", "NULL::INT", "1,NULL", nil, nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := "SELECT " + tc.left + " IN (" + tc.list + ")," + tc.left + " NOT IN (" + tc.list + ")"
				arrayCompareBoth(t, p, b, c, arrayCompareBool(q, []any{tc.in, tc.notIn}))
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ac_list(id INT,x INT,y INT)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ac_list VALUES(1,1,2),(2,3,NULL),(3,NULL,1),(4,1,2),(5,3,NULL)", 5))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT l.x IN(l.y,1),l.x NOT IN(l.y,1) FROM ac_list AS l", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}, {nil, nil}, {nil, nil}, {true, false}, {nil, nil}}),
			fixture.Select("SELECT x IN($1,$2),x NOT IN($1,$2) FROM ac_list", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}, {nil, nil}, {nil, nil}, {true, false}, {nil, nil}}, 1, nil),
		} {
			s.Bag = true
			arrayCompareBoth(t, p, b, c, s)
		}
		for _, tc := range []struct {
			expr string
			ids  [][]any
		}{
			{"x IN(1,2)", [][]any{{int64(1)}, {int64(4)}}},
			{"x NOT IN(1,2)", [][]any{{int64(2)}, {int64(5)}}},
			{"x IN(1,NULL)", [][]any{{int64(1)}, {int64(4)}}},
			{"x NOT IN(1,NULL)", nil},
			{"NOT (x IN(1,2))", [][]any{{int64(2)}, {int64(5)}}},
			{"(x IN(1,2) AND y IN(2)) OR x IN(3)", [][]any{{int64(1)}, {int64(2)}, {int64(4)}, {int64(5)}}},
		} {
			arrayCompareBoth(t, p, b, c, fixture.Select("SELECT id FROM ac_list WHERE "+tc.expr+" ORDER BY id", []string{"id"}, []string{"INT4"}, tc.ids))
		}
		arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT (1=1) IN(TRUE,FALSE),NOT (3 IN(1,NULL)),1 IN(1+0,CAST(2 AS INT)),1 /* list */ NOT /* gap */ IN (2,3)", []any{true, nil, true, true}))
	})
}
