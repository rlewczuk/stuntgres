package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func explainAnalyzeDMLCTESources(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, lazy := range []bool{false, true} {
			t.Run(fmt.Sprintf("automatic=%t/lazy=%t", automatic, lazy), func(t *testing.T) {
				autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE ea_cte_source(id integer)", 0))
					run(fixture.Exec("INSERT INTO ea_cte_source VALUES(1),(2)", 2))
					run(fixture.Exec("CREATE TABLE ea_cte_sink(id integer)", 0))
					if lazy {
						run(fixture.Exec("CREATE FUNCTION ea_cte_before() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$", 0))
						run(fixture.Exec("CREATE TRIGGER ea_cte_before BEFORE INSERT ON ea_cte_sink FOR EACH STATEMENT EXECUTE FUNCTION ea_cte_before()", 0))
					}
					if automatic {
						autoExplainConfigure(t, p, b, c, notices)
					}
					const command = "WITH first AS (INSERT INTO ea_cte_sink SELECT id FROM ea_cte_source WHERE id>=$1 RETURNING id), second AS (UPDATE ea_cte_source SET id=id+10 WHERE id=1 RETURNING id) SELECT id FROM first UNION ALL SELECT id FROM first UNION ALL SELECT id FROM second"
					const query = "EXPLAIN (ANALYZE,FORMAT JSON,TIMING OFF,SUMMARY OFF,COSTS OFF) " + command
					held, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					autoExplainQuiet(t, notices)
					for _, argument := range []int64{1, 2, 1} {
						tx, err := c.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						func() {
							defer tx.Rollback()
							stmt := tx.StmtContext(p.Ctx, held)
							defer stmt.Close()
							rows, err := stmt.QueryContext(p.Ctx, argument)
							if err != nil {
								t.Fatal(err)
							}
							document, _ := explainJSONRows(t, rows)
							count := float64(5)
							if argument == 2 {
								count = 3
							}
							explainJSONActual(t, b, document, count, false, false)
							root := document[0]["Plan"].(map[string]any)
							explainJSONVisit(root, func(node map[string]any) {
								for _, key := range []string{"Actual Rows", "Actual Loops"} {
									if value, ok := node[key].(float64); !ok || value < 0 {
										t.Fatal("accepted CTE source actual missing", node)
									}
								}
							})
							if automatic {
								parameters := fmt.Sprintf("$1 = '%d'", argument)
								autoExplainJSON(t, notices, query, &parameters, true, count)
								fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SET LOCAL auto_explain.log_min_duration=-1", 0))
							}
							want := [][]any{{int64(1)}, {int64(2)}}
							if argument == 2 {
								want = [][]any{{int64(2)}}
							}
							fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT id FROM ea_cte_sink ORDER BY id", []string{"id"}, []string{"INT4"}, want))
							fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT id FROM ea_cte_source ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(11)}}))
							if err := tx.Rollback(); err != nil {
								t.Fatal(err)
							}
							autoExplainQuiet(t, notices)
						}()
					}
				})
			})
		}
	}
}

func explainAnalyzeDMLCurrentOf(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE ea_current(id integer,v integer)", 0))
					run(fixture.Exec("INSERT INTO ea_current VALUES(1,10),(2,20)", 2))
					tx, err := b.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
					step(fixture.Exec("DECLARE ea_cursor CURSOR FOR SELECT id FROM ea_current ORDER BY id FOR UPDATE", 0))
					step(fixture.Select("FETCH FORWARD 1 FROM ea_cursor", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
					for _, command := range []string{
						"UPDATE ea_current SET v=v+5 WHERE CURRENT OF ea_cursor",
						"DELETE FROM ea_current WHERE CURRENT OF ea_cursor",
					} {
						query := "EXPLAIN (ANALYZE,FORMAT JSON,TIMING OFF,SUMMARY OFF,COSTS OFF) " + command
						document, _ := explainJSONRows(t, mustExplainCapabilityRows(t, p, tx, query, prepared))
						explainJSONActual(t, b, document, 0, false, false)
						if !b.PG {
							autoExplainDMLNode(t, document[0]["Plan"].(map[string]any), "Cursor Locator", 1, 1)
						}
					}
					step(fixture.Select("SELECT id,v FROM ea_current ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}}))
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}
