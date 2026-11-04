package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func autoExplainDMLCTEPresentation(t *testing.T) {
	for _, lazy := range []bool{false, true} {
		t.Run(fmt.Sprintf("lazy=%t", lazy), func(t *testing.T) {
			autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE ae_cte_source(id integer)", 0))
				run(fixture.Exec("INSERT INTO ae_cte_source VALUES(1),(2)", 2))
				run(fixture.Exec("CREATE TABLE ae_cte_sink(id integer)", 0))
				if lazy {
					run(fixture.Exec("CREATE FUNCTION ae_cte_before() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$", 0))
					run(fixture.Exec("CREATE TRIGGER ae_cte_before BEFORE INSERT ON ae_cte_sink FOR EACH STATEMENT EXECUTE FUNCTION ae_cte_before()", 0))
				}
				autoExplainConfigure(t, p, b, c, notices)
				const query = "WITH changed AS (INSERT INTO ae_cte_sink SELECT id FROM ae_cte_source WHERE id>=$1 RETURNING id) SELECT id FROM changed ORDER BY id"
				for _, analyzed := range []bool{false, true} {
					flag := "off"
					if analyzed {
						flag = "on"
					}
					run(fixture.Exec("SET auto_explain.log_analyze="+flag, 0))
					for _, argument := range []int64{1, 2, 1} {
						tx, err := c.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						func() {
							defer tx.Rollback()
							want := [][]any{{int64(1)}, {int64(2)}}
							if argument == 2 {
								want = [][]any{{int64(2)}}
							}
							fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Prepared(fixture.Select(query, []string{"id"}, []string{"INT4"}, want, argument)))
							parameters := fmt.Sprintf("$1 = '%d'", argument)
							autoExplainJSON(t, notices, query, &parameters, analyzed, float64(len(want)))
							fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SET LOCAL auto_explain.log_min_duration=-1", 0))
							fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT id FROM ae_cte_sink ORDER BY id", []string{"id"}, []string{"INT4"}, want))
							if err := tx.Rollback(); err != nil {
								t.Fatal(err)
							}
							autoExplainQuiet(t, notices)
						}()
					}
				}
				run(fixture.Exec("SET auto_explain.log_min_duration=-1", 0))
				run(fixture.Select("SELECT id FROM ae_cte_source ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				run(fixture.Select("SELECT id FROM ae_cte_sink ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
			})
		})
	}
}

func autoExplainDMLDuplicateCandidates(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, name := range []string{"ae_duplicate_update", "ae_duplicate_delete", "ae_duplicate_empty"} {
			run(fixture.Exec("CREATE TABLE "+name+"(id integer,v integer)", 0))
			if name != "ae_duplicate_empty" {
				run(fixture.Exec("INSERT INTO "+name+" VALUES(1,10),(2,20),(3,30)", 3))
			}
		}
		autoExplainConfigure(t, p, b, c, notices)
		run(fixture.Exec("SET auto_explain.log_buffers=on", 0))
		run(fixture.Exec("SET auto_explain.log_wal=on", 0))
		for _, query := range []string{
			"UPDATE ae_duplicate_update t SET v=99 FROM (VALUES(1),(1)) s(id) WHERE t.id=s.id",
			"DELETE FROM ae_duplicate_delete t USING (VALUES(1),(1)) s(id) WHERE t.id=s.id",
		} {
			run(fixture.Prepared(fixture.Exec(query, 1)))
			document := autoExplainJSON(t, notices, query, nil, true, 0)
			autoExplainMetric(t, b, document, 3, 1)
			if !b.PG {
				root := document["Plan"].(map[string]any)
				autoExplainDMLNode(t, root, "Logical Scan", 3, 1)
				autoExplainDMLNode(t, root, "Join", 6, 1)
				autoExplainDMLNode(t, root, "Filter", 2, 1)
				autoExplainDMLNode(t, root, "Values", 2, 1)
			}
		}
		for _, tc := range []struct {
			query  string
			target float64
		}{
			{"UPDATE ae_duplicate_update SET v=100 WHERE id<0", 3},
			{"UPDATE ae_duplicate_empty SET v=99 WHERE id<0", 0},
			{"DELETE FROM ae_duplicate_empty WHERE id<0", 0},
		} {
			run(fixture.Prepared(fixture.Exec(tc.query, 0)))
			document := autoExplainJSON(t, notices, tc.query, nil, true, 0)
			autoExplainMetric(t, b, document, tc.target, 0)
			if !b.PG {
				root := document["Plan"].(map[string]any)
				autoExplainDMLNode(t, root, "Logical Scan", tc.target, 1)
				autoExplainDMLNode(t, root, "Filter", 0, 1)
			}
		}
		run(fixture.Exec("SET auto_explain.log_min_duration=-1", 0))
		run(fixture.Select("SELECT id,v FROM ae_duplicate_update ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(99)}, {int64(2), int64(20)}, {int64(3), int64(30)}}))
		run(fixture.Select("SELECT id,v FROM ae_duplicate_delete ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}}))
		run(fixture.Select("SELECT id,v FROM ae_duplicate_empty ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, nil))
	})
}
