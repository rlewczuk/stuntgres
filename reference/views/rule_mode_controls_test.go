package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRuleModeControls(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			p := fixture.NewScenario(t)
			var emitted []byte
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c := ruleModeConn(t, p, b)
					run := transactionalRulesRun(t, p, b, c, nil)
					tables := []string{"event_o", "event_d", "event_r", "\"event A\""}
					rules := []string{"r", "r", "r", "\"rule A\""}
					run(fixture.Exec("CREATE TABLE audit(id INT,v INT)", 0))
					run(fixture.Exec("CREATE TABLE obsolete(id INT,v INT)", 0))
					for i, mode := range ruleModeCases {
						table := tables[i]
						run(fixture.Exec("CREATE TABLE "+table+"(id INT,v INT)", 0))
						// Load data before defining rules: restore must not fire them again.
						run(fixture.Exec("INSERT INTO "+table+" VALUES(1,10)", 1))
						run(fixture.Exec("CREATE RULE r AS ON INSERT TO "+table+" DO ALSO INSERT INTO obsolete VALUES(NEW.id,NEW.v)", 0))
						run(fixture.Exec("ALTER TABLE "+table+" "+mode.action+" r", 0))
						run(fixture.Exec("CREATE OR REPLACE RULE r AS ON INSERT TO "+table+" DO ALSO INSERT INTO audit VALUES(NEW.id,NEW.v)", 0))
						if i == 3 {
							run(fixture.Exec("ALTER RULE r ON "+table+" RENAME TO \"rule A\"", 0))
						}
					}
					run(fixture.Exec("ALTER TABLE \"event A\" RENAME TO \"renamed A\"", 0))
					tables[3] = "\"renamed A\""
					run(fixture.Exec("DROP TABLE obsolete", 0))
					queries := make([]string, len(tables))
					for i, table := range tables {
						queries[i] = "INSERT INTO " + table + " VALUES($1,$2)"
					}
					h := transactionalRulesHandles(t, p, c, held, queries...)
					if !b.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						emitted = bytes.Clone(dump.Bytes())
						// Every failed replacement must preserve live data/catalog and held handles.
						readerErr := errors.New("rule mode reader failed")
						if err := stuntgres.Restore(p.Ctx, p.Native, transactionalViewsFailedReader{readerErr}); !errors.Is(err, readerErr) {
							t.Fatal(err)
						}
						ctx, cancel := context.WithCancel(p.Ctx)
						cancel()
						if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
						for _, suffix := range []string{"ALTER TABLE public.event_o DISABLE RULE missing;", "ALTER TABLE public.event_o ENABLE SOMETIMES RULE r;"} {
							if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(emitted)+"\n"+suffix)); err == nil {
								t.Fatal("late invalid mode ALTER accepted")
							}
							run(ruleModeValueRows("event_o", []any{int64(1), int64(10)}))
							run(ruleModeCount("audit", 0))
						}
						live := transactionalRulesRun(t, p, b, c, h)
						live(fixture.Exec(queries[0], 1, int64(90), int64(900)))
						live(ruleModeValueRows("audit", []any{int64(90), int64(900)}))
						retained, err := c.QueryContext(p.Ctx, "SELECT id,v FROM audit ORDER BY id")
						if err != nil {
							t.Fatal(err)
						}
						defer retained.Close()
						oldColumns, err := retained.Columns()
						if err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						run(fixture.Exec("CREATE TABLE sentinel(id INT)", 0))
						run(fixture.Exec("INSERT INTO sentinel VALUES(99)", 1))
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
							t.Fatal(err)
						}
						newColumns, err := retained.Columns()
						if err != nil || len(oldColumns) != 2 || len(newColumns) != 2 || oldColumns[0] != "id" || oldColumns[1] != "v" || newColumns[0] != "id" || newColumns[1] != "v" {
							t.Fatal(oldColumns, newColumns, err)
						}
						if !retained.Next() {
							t.Fatal(retained.Err())
						}
						var oldID, oldValue int64
						if err := retained.Scan(&oldID, &oldValue); err != nil || oldID != 90 || oldValue != 900 {
							t.Fatal(oldID, oldValue, err)
						}
						if retained.Next() || retained.Err() != nil {
							t.Fatal(retained.Err())
						}
						retained.Close()
						run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='sentinel' AND relnamespace='public'::regnamespace", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					} else if emitted != nil {
						// Replay the actual native script, not a synthetic stand-in.
						run(fixture.Exec("DROP TABLE event_o,event_d,event_r,\"renamed A\",audit CASCADE", 0))
						run(fixture.Exec(string(emitted), 0))
					}
					run = transactionalRulesRun(t, p, b, c, h)
					run(ruleModeCount("audit", 0))
					for i, table := range tables {
						run(ruleModeValueRows(table, []any{int64(1), int64(10)}))
						run(fixture.Select("SELECT ev_enabled::TEXT FROM pg_rewrite WHERE ev_class='"+table+"'::regclass AND rulename='"+strings.Trim(rules[i], "\"")+"'", []string{"ev_enabled"}, []string{"TEXT"}, [][]any{{ruleModeCases[i].code}}))
						run(fixture.Select("SELECT count(*) FROM (SELECT DISTINCT d.refobjid FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid WHERE d.classid='pg_rewrite'::regclass AND r.ev_class='"+table+"'::regclass AND d.refclassid='pg_class'::regclass AND d.refobjid='audit'::regclass) edges", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
					}
					var audit [][]any
					for j, role := range []string{"origin", "local", "replica"} {
						run(fixture.Exec("SET session_replication_role="+role, 0))
						for i, mode := range ruleModeCases {
							id := int64(10 + j*4 + i)
							run(fixture.Exec(queries[i], 1, id, id*10))
							if ruleModeActive(mode.code, role) {
								audit = append(audit, []any{id, id * 10})
							}
						}
					}
					run(ruleModeValueRows("audit", audit...))
					for i, table := range tables {
						rows := [][]any{{int64(1), int64(10)}}
						for j := range 3 {
							id := int64(10 + j*4 + i)
							rows = append(rows, []any{id, id * 10})
						}
						run(ruleModeValueRows(table, rows...))
					}
				})
			}
		})
	}
}
