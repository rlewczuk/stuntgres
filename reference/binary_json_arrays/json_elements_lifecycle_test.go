package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonElementsHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, f := range []string{"json_array_elements", "json_array_elements_text"} {
		for _, q := range []string{"SELECT * FROM " + f + "($1)", "SELECT * FROM pg_catalog." + f + "(from_json => $1)", "SELECT " + f + "($1)"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				n := any("null")
				if f == "json_array_elements_text" {
					n = nil
				}
				for _, tc := range []struct {
					input any
					rows  [][]any
					bad   bool
				}{
					{`[1,null]`, [][]any{{"1"}, {n}}, false}, {`[]`, nil, false}, {nil, nil, false}, {`{}`, nil, true}, {`[2]`, [][]any{{"2"}}, false},
				} {
					if tc.bad {
						fixture.JSONKeysError(t, p, b, held, q, false, fixture.ControlsReferenceDiagnostic("22023", "cannot call "+f+" on a non-array"), "execute", tc.input)
						continue
					}
					s := jsonElementsSelect(f, q, tc.rows, tc.input)
					if q == "SELECT "+f+"($1)" {
						s.Columns = []string{f}
					}
					s.Bag = true
					fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonElementsMetadata(s, b.PG))
				}
			}()
		}
	}
}

func TestReferenceJSONElementsParametersAndLifecycle(t *testing.T) {
	jsonElementsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonElementsConcurrent(t, p, b)
		jsonElementsHeld(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE jelements_atomic(id int,j json)", 0))
		run(fixture.Exec(`INSERT INTO jelements_atomic VALUES(1,'[1,null]'),(2,'true')`, 2))
		for _, f := range []string{"json_array_elements", "json_array_elements_text"} {
			dest := "jelements_dest_" + f
			run(fixture.Exec("CREATE TABLE "+dest+"(v "+jsonElementsType(f)+")", 0))
			q := "INSERT INTO " + dest + " SELECT e.value FROM jelements_atomic s," + f + "(s.j) e ORDER BY s.id"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22023", "cannot call "+f+" on a scalar"))
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT v FROM "+dest, []string{"v"}, []string{jsonElementsType(f)}, nil))
			}
			run(fixture.Prepared(fixture.Exec("INSERT INTO "+dest+" SELECT e.value FROM jelements_atomic s,"+f+"(s.j) e WHERE s.id=$1", 2, 1)))
			n := any("null")
			if f == "json_array_elements_text" {
				n = nil
			}
			s := fixture.Select("SELECT v FROM "+dest, []string{"v"}, []string{jsonElementsType(f)}, [][]any{{"1"}, {n}})
			s.Bag = true
			run(s)
			run(fixture.Prepared(fixture.Exec("INSERT INTO "+dest+" SELECT e.value FROM jelements_atomic s,"+f+"(s.j) e WHERE s.id=$1", 0, nil)))
			for _, prepared := range []bool{false, true} {
				seq := "jelements_demand_" + f
				if prepared {
					seq += "_prepared"
				}
				run(fixture.Exec("CREATE SEQUENCE "+seq, 0))
				s := fixture.Select("SELECT e.value,e.n FROM jelements_atomic s,"+f+"('[1,2,3]') WITH ORDINALITY AS e(value,n) WHERE s.id=$1 AND nextval('"+seq+"')<=$2 ORDER BY n", []string{"value", "n"}, []string{jsonElementsType(f), "INT8"}, [][]any{{"1", int64(1)}, {"2", int64(2)}}, 1, int64(2))
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT last_value,is_called FROM "+seq, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(3), true}}))
			}
		}
	})
}
