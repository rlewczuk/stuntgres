package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONEachPredecessorDemand(t *testing.T) {
	jsonEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE je_demand_source(id int,j json)", 0))
		run(fixture.Exec(`INSERT INTO je_demand_source VALUES(1,'{"a":1,"n":null}'),(2,'[]'),(3,'{}'),(4,NULL)`, 4))
		for _, function := range []string{"json_each", "json_each_text"} {
			for _, prepared := range []bool{false, true} {
				mode := "direct"
				if prepared {
					mode = "held_prepared"
				}
				t.Run(function+"/"+mode, func(t *testing.T) {
					query := "SELECT f.key,f.value FROM je_demand_source s," + function + "(s.j) f WHERE s.id=$1 ORDER BY f.key"
					var executor fixture.Executor = c
					if prepared {
						statement, err := c.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer statement.Close()
						executor = fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
					}
					nullValue := any("null")
					if function == "json_each_text" {
						nullValue = nil
					}
					want := [][]any{{"a", "1"}, {"n", nullValue}}
					for _, tc := range []struct {
						name string
						id   any
						rows [][]any
						bad  bool
					}{
						{"exclude_array", 1, want, false},
						{"select_array", 2, nil, true},
						{"recover_after_error", 1, want, false},
						{"null_qualification", nil, nil, false},
						{"recover_after_null", 1, want, false},
						{"empty_object", 3, nil, false},
						{"sql_null_document", 4, nil, false},
					} {
						t.Run(tc.name, func(t *testing.T) {
							if tc.bad {
								// Runtime root failure has no SQL position, portal-input
								// context, detail, hint or object fields, even when bound.
								jsonKeysError(t, p, b, executor, query, false, fixture.ControlsReferenceDiagnostic("22023", "cannot deconstruct an array as an object"), "execute", tc.id)
								return
							}
							s := fixture.Select(query, []string{"key", "value"}, []string{"TEXT", jsonEachType(function)}, tc.rows, tc.id)
							fixture.RunStep(t, p.Ctx, executor, b.Recorder, jsonEachWithMetadata(s, b.PG))
						})
					}
				})
			}
		}
	})
}

func TestReferenceJSONEachVolatileDemand(t *testing.T) {
	jsonEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE je_volatile_source(id int,j json)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO je_volatile_source VALUES(1,'{"a":1,"b":2,"c":3}')`, 1))
		for _, function := range []string{"json_each", "json_each_text"} {
			for _, prepared := range []bool{false, true} {
				mode := "direct"
				if prepared {
					mode = "prepared"
				}
				t.Run(function+"/"+mode, func(t *testing.T) {
					sequence := "je_demand_" + function + "_" + mode
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SEQUENCE "+sequence, 0))
					// The complete WHERE is left-dependent AND volatile. Moving
					// it whole to the predecessor would consume one nextval and
					// admit all three members. Post-expansion qualification consumes
					// three values and retains only ordinals one and two, as
					// confirmed against the pinned PostgreSQL 18.0 oracle.
					query := "SELECT f.key,f.value,n FROM je_volatile_source s," + function + "(s.j) WITH ORDINALITY AS f(key,value,n) WHERE s.id=$1 AND nextval('" + sequence + "') <= $2 ORDER BY n"
					s := fixture.Select(query, []string{"key", "value", "n"}, []string{"TEXT", jsonEachType(function), "INT8"}, [][]any{{"a", "1", int64(1)}, {"b", "2", int64(2)}}, 1, int64(2))
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, jsonEachWithMetadata(s, b.PG))
					state := fixture.Select("SELECT last_value,is_called FROM "+sequence, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(3), true}})
					state.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, state)
				})
			}
		}
	})
}
