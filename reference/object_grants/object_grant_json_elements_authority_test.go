package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantJSONElementsAuthority(t *testing.T) {
	for _, name := range []string{"json_array_elements", "json_array_elements_text"} {
		t.Run(name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				target := "pg_catalog." + name + "(json)"
				defer objectGrantRoutineCleanup(t, p, c, []string{target, "pg_catalog.upper(text)", "pg_catalog.json_array_length(json)"})
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_elements_source(id INT,j JSON)", 0))
				run(fixture.Exec(`INSERT INTO og_elements_source VALUES(1,'[1,1,null,"x"]'),(2,'[]'),(3,NULL)`, 3))
				run(fixture.Exec("GRANT SELECT ON og_elements_source TO og_object_reader", 0))
				valueType, other, otherType := "JSON", "json_array_elements_text", "TEXT"
				var nullValue any = "null"
				stringValue := `"x"`
				if name == "json_array_elements_text" {
					valueType, other, otherType = "TEXT", "json_array_elements", "JSON"
					nullValue, stringValue = nil, "x"
				}
				input := `[1,1,null,"x"]`
				values := [][]any{{"1"}, {"1"}, {nullValue}, {stringValue}}
				steps := []fixture.Step{
					fixture.Select("SELECT e.value,e.n FROM pg_catalog."+name+"(from_json=>$1::JSON) WITH ORDINALITY AS e(value,n) ORDER BY e.n", []string{"value", "n"}, []string{valueType, "INT8"}, [][]any{{"1", int64(1)}, {"1", int64(2)}, {nullValue, int64(3)}, {stringValue, int64(4)}}, input),
					fixture.Select("SELECT "+name+"($1::JSON) AS value", []string{"value"}, []string{valueType}, values, input),
					fixture.Select("SELECT s.id,e.value FROM og_elements_source s CROSS JOIN LATERAL "+name+"(s.j) AS e", []string{"id", "value"}, []string{"INT4", valueType}, [][]any{{int64(1), "1"}, {int64(1), "1"}, {int64(1), nullValue}, {int64(1), stringValue}}),
				}
				steps[1].Bag, steps[2].Bag = true, true
				held := make([]fixture.HeldStatement, len(steps))
				run(fixture.Exec("SET SESSION AUTHORIZATION og_object_reader", 0))
				for i, step := range steps {
					stmt, err := c.PrepareContext(p.Ctx, step.Query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: step.Query}
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				phase := func(allowed bool) {
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						for i, step := range steps {
							if !allowed {
								step = fixture.ImplicitError(step.Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + name}, step.Args...)
								step.SelectRows, step.ErrorStage = true, "execute"
							}
							run(step)
							fixture.RunStep(t, p.Ctx, held[i], b.Recorder, step)
						}
					})
				}
				upper := fixture.Select("SELECT upper($1::TEXT) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"OK"}}, "ok")
				length := fixture.Select("SELECT json_array_length($1::JSON) AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}, `[7,8]`)
				controls := func() {
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						run(fixture.Select("SELECT value FROM "+other+"('[7]'::JSON)", []string{"value"}, []string{otherType}, [][]any{{"7"}}))
						run(upper)
						run(length)
					})
				}
				phase(true)
				controls()
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
				phase(false)
				controls()
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
				phase(true)
				controls()

				// Independent current ACL controls: neither scalar routine is a
				// hidden authority dependency of this INTERNAL JSON SRF.
				for _, control := range []struct {
					target, name string
					step         fixture.Step
					unrelated    fixture.Step
				}{
					{"pg_catalog.upper(text)", "upper", upper, length},
					{"pg_catalog.json_array_length(json)", "json_array_length", length, upper},
				} {
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+control.target+" FROM PUBLIC", 0))
					phase(true)
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						s := fixture.ImplicitError(control.step.Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + control.name}, control.step.Args...)
						s.SelectRows, s.ErrorStage = true, "execute"
						run(s)
						run(control.unrelated)
					})
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+control.target+" TO PUBLIC", 0))
				}
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
				phase(false)
				controls()
			})
		})
	}
}

func TestReferenceObjectGrantJSONElementsAuthorityDemand(t *testing.T) {
	for _, name := range []string{"json_array_elements", "json_array_elements_text"} {
		t.Run(name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				target := "pg_catalog." + name + "(json)"
				defer objectGrantRoutineCleanup(t, p, c, []string{target})
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_elements_null(id INT,j JSON)", 0))
				run(fixture.Exec("INSERT INTO og_elements_null VALUES(1,NULL)", 1))
				run(fixture.Exec("GRANT SELECT ON og_elements_null TO og_object_reader", 0))
				valueType := "JSON"
				if name == "json_array_elements_text" {
					valueType = "TEXT"
				}
				// A zero-row cursor is not an absent SRF initialization. Only
				// statically dead FROM cases below are expected to prune it.
				cases := []struct {
					step   fixture.Step
					pruned bool
				}{
					{fixture.Select("SELECT value FROM "+name+"(NULL::JSON)", []string{"value"}, []string{valueType}, nil), false},
					{fixture.Select("SELECT value FROM "+name+"('[]'::JSON)", []string{"value"}, []string{valueType}, nil), false},
					{fixture.Select("SELECT value FROM "+name+"($1::JSON)", []string{"value"}, []string{valueType}, nil, nil), false},
					{fixture.Select("SELECT e.value FROM og_elements_null s CROSS JOIN LATERAL "+name+"(s.j) AS e", []string{"value"}, []string{valueType}, nil), false},
					{fixture.Select("SELECT e.value FROM og_elements_null s CROSS JOIN LATERAL "+name+"(s.j) AS e WHERE s.id=99", []string{"value"}, []string{valueType}, nil), false},
					{fixture.Select("SELECT "+name+"(NULL::JSON) AS value", []string{"value"}, []string{valueType}, nil), false},
					{fixture.Select("SELECT "+name+"('[1]'::JSON) AS value WHERE FALSE", []string{"value"}, []string{valueType}, nil), false},
					{fixture.Select("SELECT value FROM "+name+"('[1]'::JSON) WHERE FALSE", []string{"value"}, []string{valueType}, nil), true},
					{fixture.Select("SELECT e.value FROM og_elements_null s CROSS JOIN LATERAL "+name+"(s.j) AS e WHERE FALSE", []string{"value"}, []string{valueType}, nil), true},
				}
				held := make([]fixture.HeldStatement, len(cases))
				run(fixture.Exec("SET SESSION AUTHORIZATION og_object_reader", 0))
				for i, tc := range cases {
					stmt, err := c.PrepareContext(p.Ctx, tc.step.Query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.step.Query}
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				phase := func(allowed bool) {
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						for i, tc := range cases {
							step := tc.step
							if !allowed && !tc.pruned {
								step = fixture.ImplicitError(step.Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + name}, step.Args...)
								step.SelectRows, step.ErrorStage = true, "execute"
							}
							run(step)
							fixture.RunStep(t, p.Ctx, held[i], b.Recorder, step)
						}
					})
				}
				phase(true)
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
				phase(false)
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
				phase(true)
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
				phase(false)
			})
		})
	}
}
