package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func explainJSONSecurityLocator(t *testing.T) {
	t.Run("security", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ej_policy_reader"})
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step) }
				for _, sql := range []string{
					"CREATE ROLE ej_policy_reader", "GRANT USAGE ON SCHEMA public TO ej_policy_reader",
					"CREATE TABLE ej_policy(id integer,v integer)", "GRANT SELECT,UPDATE,DELETE ON ej_policy TO ej_policy_reader",
					"ALTER TABLE ej_policy ENABLE ROW LEVEL SECURITY", "CREATE POLICY ej_visible ON ej_policy USING(id=1) WITH CHECK(v>0)",
				} {
					run(fixture.Exec(sql, 0))
				}
				run(fixture.Exec("INSERT INTO ej_policy VALUES(1,10),(2,20)", 2))
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				if _, err := conn.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION ej_policy_reader"); err != nil {
					t.Fatal(err)
				}
				defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				for _, prepared := range []bool{false, true} {
					query := "EXPLAIN (FORMAT JSON,VERBOSE,COSTS false) UPDATE ej_policy SET v=99 RETURNING id"
					var document explainJSONDocument
					if prepared {
						stmt, err := conn.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						rows, err := stmt.QueryContext(p.Ctx)
						if err != nil {
							stmt.Close()
							t.Fatal(err)
						}
						document, _ = explainJSONRows(t, rows)
						stmt.Close()
					} else {
						document, _ = explainJSONQuery(t, p, conn, query)
					}
					explainJSONOrdinary(t, b, document)
					explainJSONFind(t, document, "Relation Name", "ej_policy")
					if !b.PG {
						target := explainJSONFind(t, document, "Node Type", "Update")
						checks := explainJSONList(t, target, "Row Security Checks", 1)
						explainJSONContains(t, checks, "v", ">", "0")
						scan := explainJSONFind(t, document, "Node Type", "Logical Scan")
						explainJSONContains(t, scan["Row Security Filter"], "id", "1")
					}
					run(fixture.Select("SELECT id,v FROM ej_policy ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
				}
			})
		}
	})
	t.Run("locator", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE ej_cursor(id integer,v integer)", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO ej_cursor VALUES(1,10),(2,20)", 2))
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, step) }
				run(fixture.Exec("DECLARE ej_current CURSOR FOR SELECT id,v FROM ej_cursor ORDER BY id FOR UPDATE", 0))
				run(fixture.Select("FETCH NEXT FROM ej_current", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
				for _, prepared := range []bool{false, true} {
					rows := mustExplainCapabilityRows(t, p, tx, "EXPLAIN (FORMAT JSON,VERBOSE,COSTS false) UPDATE ej_cursor SET v=99 WHERE CURRENT OF ej_current RETURNING id", prepared)
					document, _ := explainJSONRows(t, rows)
					if !b.PG {
						root := explainJSONFind(t, document, "Node Type", "Update")
						if root["Current Of"] != "ej_current" {
							t.Fatal(root)
						}
						explainJSONFind(t, document, "Node Type", "Cursor Locator")
					} else {
						explainJSONFind(t, document, "Relation Name", "ej_cursor")
					}
					run(fixture.Select("SELECT id,v FROM ej_cursor ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
				}
				run(fixture.Select("FETCH NEXT FROM ej_current", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}}))
			})
		}
	})
	t.Run("planned-sources", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				for _, sql := range []string{
					"CREATE TABLE ej_parent(id integer)", "CREATE TABLE ej_low(CHECK(id<10)) INHERITS(ej_parent)", "CREATE TABLE ej_high(CHECK(id>=10)) INHERITS(ej_parent)", "SET constraint_exclusion=on",
				} {
					if _, err := conn.ExecContext(p.Ctx, sql); err != nil {
						t.Fatal(err)
					}
				}
				for _, prepared := range []bool{false, true} {
					rows := mustExplainCapabilityRows(t, p, conn, "EXPLAIN (FORMAT JSON,VERBOSE) SELECT id FROM ej_parent WHERE id<5", prepared)
					document, _ := explainJSONRows(t, rows)
					if b.PG {
						explainJSONContains(t, document, "ej_parent")
						continue
					}
					scan := explainJSONFind(t, document, "Relation Name", "ej_parent")
					explainJSONList(t, scan, "Planned Sources", 3)
					pruned := explainJSONList(t, scan, "Pruned Sources", 1)
					if pruned[0].(map[string]any)["Relation Name"] != "ej_high" {
						t.Fatal(pruned)
					}
					filter := explainJSONFind(t, document, "Node Type", "Filter")
					if filter["Access Method"] != "Full Scan" {
						t.Fatal(filter)
					}
					if _, ok := filter["CPU Cost"].(float64); !ok {
						t.Fatal(filter)
					}
				}
			})
		}
	})
	t.Run("rewrite-nothing", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				for _, sql := range []string{"CREATE TABLE ej_nothing(id integer)", "CREATE RULE ej_discard AS ON INSERT TO ej_nothing DO INSTEAD NOTHING"} {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(sql, 0))
				}
				for _, options := range []string{
					"FORMAT JSON,COSTS false",
					"FORMAT JSON,COSTS false,SUMMARY true",
					"FORMAT JSON,COSTS false,ANALYZE,SUMMARY true",
					"FORMAT JSON,COSTS false,ANALYZE,SUMMARY false",
				} {
					query := "EXPLAIN (" + options + ") INSERT INTO ej_nothing VALUES(1)"
					for _, prepared := range []bool{false, true} {
						explainJSONEmptyRewriteRows(t, b, mustExplainCapabilityRows(t, p, b.DB, query, prepared))
					}
					held, err := b.DB.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					func() {
						defer held.Close()
						for range 2 {
							rows, err := held.QueryContext(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							explainJSONEmptyRewriteRows(t, b, rows)
						}
					}()
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id FROM ej_nothing", []string{"id"}, []string{"INT4"}, nil))
			})
		}
	})
}
