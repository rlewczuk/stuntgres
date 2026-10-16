package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_0 pg_proc.dat3958/3959 declare strict immutable JSON-input SRFs
// returning RECORD, with OUT key TEXT and value JSON/TEXT respectively.
// The OUT columns are record shape, not additional GRANT input formals.
func TestReferenceObjectGrantJSONEachAuthority(t *testing.T) {
	for _, name := range []string{"json_each", "json_each_text"} {
		t.Run(name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				target := "pg_catalog." + name + "(json)"
				defer objectGrantRoutineCleanup(t, p, c, []string{target})
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_json_each_source(id INT,j JSON)", 0))
				run(fixture.Exec(`INSERT INTO og_json_each_source VALUES(1,'{"a":1,"a":2,"n":null,"s":"x"}'),(2,'{}'),(3,NULL)`, 3))
				run(fixture.Exec("GRANT SELECT ON og_json_each_source TO og_object_reader", 0))
				valueType, other := "JSON", "json_each_text"
				var nullValue any = "null"
				stringValue := `"x"`
				if name == "json_each_text" {
					valueType, other = "TEXT", "json_each"
					nullValue, stringValue = nil, "x"
				}
				input := `{"a":1,"a":2,"n":null,"s":"x"}`
				pairs := [][]any{{"a", "1"}, {"a", "2"}, {"n", nullValue}, {"s", stringValue}}
				type consumer struct {
					step fixture.Step
					held fixture.HeldStatement
				}
				steps := []fixture.Step{
					fixture.Select("SELECT key,value FROM "+name+"($1::JSON)", []string{"key", "value"}, []string{"TEXT", valueType}, pairs, input),
					fixture.Select("SELECT (e).key AS key,(e).value AS value FROM "+name+"(from_json=>$1::JSON) AS e", []string{"key", "value"}, []string{"TEXT", valueType}, pairs, input),
					fixture.Select("SELECT ("+name+"($1::JSON)).key AS key,("+name+"($1::JSON)).value AS value", []string{"key", "value"}, []string{"TEXT", valueType}, pairs, input),
					fixture.Select("SELECT "+name+"('{\"a\":1}'::JSON) AS r", []string{"r"}, []string{"RECORD"}, [][]any{{"(a,1)"}}),
					fixture.Select("SELECT s.id,e.key,e.value FROM og_json_each_source s CROSS JOIN LATERAL "+name+"(s.j) AS e", []string{"id", "key", "value"}, []string{"INT4", "TEXT", valueType}, [][]any{{int64(1), "a", "1"}, {int64(1), "a", "2"}, {int64(1), "n", nullValue}, {int64(1), "s", stringValue}}),
				}
				consumers := make([]consumer, 0, len(steps))
				run(fixture.Exec("SET SESSION AUTHORIZATION og_object_reader", 0))
				for _, step := range steps {
					step.Bag = true
					stmt, err := c.PrepareContext(p.Ctx, step.Query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					consumers = append(consumers, consumer{step: step, held: fixture.HeldStatement{Executor: c, Statement: stmt, SQL: step.Query}})
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				phase := func(allowed bool) {
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						for _, tc := range consumers {
							step := tc.step
							if !allowed {
								step = fixture.ImplicitError(step.Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + name}, step.Args...)
								step.SelectRows, step.ErrorStage = true, "execute"
							}
							run(step)
							fixture.RunStep(t, p.Ctx, tc.held, b.Recorder, step)
						}
						// The other genuine JSON-input SRF has independent rights.
						run(fixture.Select("SELECT key FROM "+other+"('{\"ok\":7}'::JSON)", []string{"key"}, []string{"TEXT"}, [][]any{{"ok"}}))
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

func TestReferenceObjectGrantJSONEachAuthorityDemand(t *testing.T) {
	for _, name := range []string{"json_each", "json_each_text"} {
		t.Run(name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				target := "pg_catalog." + name + "(json)"
				defer objectGrantRoutineCleanup(t, p, c, []string{target})
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_json_each_null(id INT,j JSON)", 0))
				run(fixture.Exec("INSERT INTO og_json_each_null VALUES(1,NULL)", 1))
				run(fixture.Exec("GRANT SELECT ON og_json_each_null TO og_object_reader", 0))
				valueType := "JSON"
				if name == "json_each_text" {
					valueType = "TEXT"
				}
				// SRF initialization is not scalar strict-NULL constant folding:
				// a surviving executable SRF still needs EXECUTE even when its
				// input guarantees no output rows. FROM WHERE FALSE can instead
				// remove the whole SRF plan. Target ProjectSet remains distinct.
				steps := []fixture.Step{
					fixture.Select("SELECT key,value FROM "+name+"(NULL::JSON)", []string{"key", "value"}, []string{"TEXT", valueType}, nil),
					fixture.Select("SELECT key,value FROM "+name+"('{}'::JSON)", []string{"key", "value"}, []string{"TEXT", valueType}, nil),
					fixture.Select("SELECT key,value FROM "+name+"($1::JSON)", []string{"key", "value"}, []string{"TEXT", valueType}, nil, nil),
					fixture.Select("SELECT e.key,e.value FROM og_json_each_null s CROSS JOIN LATERAL "+name+"(s.j) AS e", []string{"key", "value"}, []string{"TEXT", valueType}, nil),
					fixture.Select("SELECT e.key,e.value FROM og_json_each_null s CROSS JOIN LATERAL "+name+"(s.j) AS e WHERE s.id=99", []string{"key", "value"}, []string{"TEXT", valueType}, nil),
					fixture.Select("SELECT "+name+"(NULL::JSON) AS r", []string{"r"}, []string{"RECORD"}, nil),
					fixture.Select("SELECT "+name+"('{\"a\":1}'::JSON) AS r WHERE FALSE", []string{"r"}, []string{"RECORD"}, nil),
				}
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
						for _, q := range []string{
							"SELECT key,value FROM " + name + "('{\"a\":1}'::JSON) WHERE FALSE",
							"SELECT e.key,e.value FROM og_json_each_null s CROSS JOIN LATERAL " + name + "(s.j) AS e WHERE FALSE",
						} {
							run(fixture.Select(q, []string{"key", "value"}, []string{"TEXT", valueType}, nil))
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
