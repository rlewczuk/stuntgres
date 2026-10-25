package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantLagProviderAuthority(t *testing.T) {
	objectGrantWindowOffsetAuthority(t, "lag")
}

func TestReferenceObjectGrantLeadProviderAuthority(t *testing.T) {
	objectGrantWindowOffsetAuthority(t, "lead")
}

func objectGrantWindowOffsetAuthority(t *testing.T, name string) {
	t.Helper()
	for arity := 1; arity <= 3; arity++ {
		t.Run(strconv.Itoa(arity), func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_offset_source(id INT,v INT,s TEXT)", 0))
				run(fixture.Exec("INSERT INTO og_offset_source VALUES(1,10,'a'),(2,NULL,NULL),(3,30,'c')", 3))
				run(fixture.Exec("GRANT SELECT ON og_offset_source TO og_object_reader", 0))
				formal, args := "anyelement", "v"
				if arity == 2 {
					formal, args = "anyelement,integer", "v,1"
				}
				if arity == 3 {
					formal, args = "anycompatible,integer,anycompatible", "v,1,-1"
				}
				expr := name + "(" + args + ") OVER(ORDER BY id)"
				var boundary any
				if arity == 3 {
					boundary = int64(-1)
				}
				values := []any{boundary, int64(10), nil}
				if name == "lead" {
					values = []any{nil, int64(30), boundary}
				}
				vector := func(call, tail string, want []any, params ...any) fixture.Step {
					var rows [][]any
					for i, value := range want {
						rows = append(rows, []any{int64(i + 1), value})
					}
					return fixture.Select("SELECT id,"+call+" AS v FROM og_offset_source"+tail+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, rows, params...)
				}
				cases := []objectGrantProviderCase{
					{vector(expr, "", values), true, true},
					{vector(expr, " WHERE id>99", nil), true, true},
					{vector(expr, " WHERE FALSE", nil), true, true},
					{vector("CASE WHEN FALSE THEN "+expr+" ELSE 7 END", "", []any{int64(7), int64(7), int64(7)}), false, true},
				}
				nullArgs := "NULL::INTEGER"
				textArgs := "s"
				if arity >= 2 {
					nullArgs += ",1"
					textArgs += ",1"
				}
				if arity == 3 {
					nullArgs += ",NULL::INTEGER"
					textArgs += ",'z'::TEXT"
				}
				cases = append(cases, objectGrantProviderCase{vector(name+"("+nullArgs+") OVER(ORDER BY id)", "", []any{nil, nil, nil}), true, true})
				var textBoundary any
				if arity == 3 {
					textBoundary = "z"
				}
				textRows := [][]any{{int64(1), textBoundary}, {int64(2), "a"}, {int64(3), nil}}
				if name == "lead" {
					textRows = [][]any{{int64(1), nil}, {int64(2), "c"}, {int64(3), textBoundary}}
				}
				cases = append(cases, objectGrantProviderCase{fixture.Select("SELECT id,"+name+"("+textArgs+") OVER(ORDER BY id) AS v FROM og_offset_source ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, textRows), true, true})
				if arity >= 2 {
					parameterArgs := "v,$1::INTEGER"
					if arity == 3 {
						parameterArgs += ",-1"
					}
					parameterCall := name + "(" + parameterArgs + ") OVER(ORDER BY id)"
					cases = append(cases,
						objectGrantProviderCase{vector(parameterCall, "", []any{int64(10), nil, int64(30)}, int64(0)), true, true},
						objectGrantProviderCase{vector(parameterCall, "", []any{nil, nil, nil}, nil), true, true},
					)
				}
				if arity == 3 {
					cases = append(cases,
						objectGrantProviderCase{vector(name+"(v,0,1/(id-id)) OVER(ORDER BY id)", "", []any{int64(10), nil, int64(30)}), true, true},
						objectGrantProviderCase{vector(name+"(1/(id-id),100,id*100) OVER(ORDER BY id)", "", []any{int64(100), int64(200), int64(300)}), true, true},
						objectGrantProviderCase{vector(name+"(1/(id-id),NULL::INTEGER,1/(id-id)) OVER(ORDER BY id)", "", []any{nil, nil, nil}), true, true},
						objectGrantProviderCase{fixture.Select("SELECT "+name+"(1::SMALLINT,0,2::BIGINT) OVER() AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}), true, true},
					)
				}
				other := "lead"
				if name == "lead" {
					other = "lag"
				}
				otherValues := []any{nil, int64(30), boundary}
				if other == "lag" {
					otherValues = []any{boundary, int64(10), nil}
				}
				controls := []fixture.Step{vector(other+"("+args+") OVER(ORDER BY id)", "", otherValues)}
				// Same name, adjacent released overload remains independent.
				controlArgs := "v"
				if arity == 1 {
					controlArgs = "v,1"
				}
				controlValues := []any{nil, int64(10), nil}
				if name == "lead" {
					controlValues = []any{nil, int64(30), nil}
				}
				controls = append(controls, vector(name+"("+controlArgs+") OVER(ORDER BY id)", "", controlValues))
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog."+name+"("+formal+")", "function", name, cases, controls)
			})
		})
	}
}
