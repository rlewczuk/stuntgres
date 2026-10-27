package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRuleModeDDL(t *testing.T) {
	ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		c := ruleModeConn(t, p, b)
		run := transactionalRulesRun(t, p, b, c, nil)
		run(fixture.Exec("CREATE TABLE src(id INT PRIMARY KEY,v INT)", 0))
		run(fixture.Exec("CREATE TABLE sink1(id INT,v INT)", 0))
		queries := []string{
			"ALTER TABLE public.src DISABLE RULE \"select\"",
			"ALTER TABLE public.src ENABLE REPLICA RULE \"select\"",
			"ALTER TABLE public.src ENABLE ALWAYS RULE \"select\"",
			"ALTER TABLE public.src ENABLE RULE \"select\"",
		}
		h := transactionalRulesHandles(t, p, c, held, queries...)
		run(fixture.Exec("CREATE RULE \"select\" AS ON INSERT TO src DO ALSO INSERT INTO sink1 VALUES(NEW.id,NEW.v)", 0))
		run = transactionalRulesRun(t, p, b, c, h)
		for i, q := range queries {
			for range 2 {
				s := fixture.Exec(q, 0)
				run(s)
				run(fixture.Select("SELECT ev_enabled::TEXT FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='select'", []string{"ev_enabled"}, []string{"TEXT"}, [][]any{{[]string{"D", "R", "A", "O"}[i]}}))
			}
		}
		run(fixture.Exec("ALTER TABLE src DISABLE RULE \"select\", ENABLE ALWAYS RULE \"select\", ENABLE RULE \"select\"", 0))
		run(fixture.Exec("INSERT INTO src VALUES(1,10)", 1))
		run(ruleModeValueRows("sink1", []any{int64(1), int64(10)}))
	})
	t.Run("plain-consumers", func(t *testing.T) {
		ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			ruleModeSetup(run)
			q := "INSERT INTO src VALUES($1,$2)"
			var x fixture.Executor = b.DB
			if held {
				stmt, err := b.DB.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				x = fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			}
			var audit [][]any
			for i, mode := range ruleModeCases {
				s := fixture.Exec("ALTER TABLE src "+mode.action+" r", 0)
				s.NonContext = true
				s.Prepared = held
				run(s)
				id := int64(i + 1)
				s = fixture.Exec(q, 1, id, id*10)
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
				if ruleModeActive(mode.code, "origin") {
					audit = append(audit, []any{id, id * 10})
				}
				run(ruleModeValueRows("sink1", audit...))
			}
		})
	})
	for _, partitioned := range []bool{false, true} {
		t.Run(map[bool]string{false: "inheritance", true: "partition"}[partitioned], func(t *testing.T) {
			ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				run := func(s fixture.Step) { ruleRun(t, p, b, held, s) }
				parent := "CREATE TABLE parent(id INT)"
				child := "CREATE TABLE child() INHERITS(parent)"
				if partitioned {
					parent += " PARTITION BY RANGE(id)"
					child = "CREATE TABLE child PARTITION OF parent FOR VALUES FROM(0) TO(100)"
				}
				run(fixture.Exec(parent, 0))
				run(fixture.Exec(child, 0))
				run(fixture.Exec("CREATE RULE r AS ON INSERT TO parent DO NOTHING", 0))
				run(fixture.Exec("CREATE RULE r AS ON INSERT TO child DO NOTHING", 0))
				run(fixture.Exec("ALTER TABLE parent DISABLE RULE r", 0))
				run(fixture.Select("SELECT c.relname,r.ev_enabled::TEXT FROM pg_rewrite r JOIN pg_class c ON c.oid=r.ev_class WHERE c.relname IN ('parent','child') AND r.rulename='r' ORDER BY c.relname", []string{"relname", "ev_enabled"}, []string{"NAME", "TEXT"}, [][]any{{"child", "O"}, {"parent", "D"}}))
				run(fixture.Exec("ALTER TABLE ONLY parent ENABLE ALWAYS RULE r", 0))
				run(fixture.Select("SELECT ev_enabled::TEXT FROM pg_rewrite WHERE ev_class='child'::regclass AND rulename='r'", []string{"ev_enabled"}, []string{"TEXT"}, [][]any{{"O"}}))
			})
		})
	}
}

func TestReferenceRuleModeMatrix(t *testing.T) {
	for _, mode := range ruleModeCases {
		for _, role := range []string{"origin", "local", "replica"} {
			t.Run(mode.code+"/"+role, func(t *testing.T) {
				ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
					c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := transactionalRulesRun(t, p, b, c, nil)
					ruleModeSetup(run)
					run(fixture.Exec("CREATE TABLE events(id INT,v INT)", 0))
					run(fixture.Exec("CREATE RULE u AS ON UPDATE TO src DO ALSO INSERT INTO events VALUES(NEW.id,NEW.v)", 0))
					run(fixture.Exec("CREATE RULE d AS ON DELETE TO src DO ALSO INSERT INTO events VALUES(OLD.id,OLD.v)", 0))
					for _, name := range []string{"r", "u", "d"} {
						run(fixture.Exec("ALTER TABLE src "+mode.action+" "+name, 0))
					}
					run(fixture.Exec("SET session_replication_role="+role, 0))
					queries := []string{"INSERT INTO src VALUES($1,$2)", "UPDATE src SET v=$2 WHERE id=$1", "DELETE FROM src WHERE id=$1"}
					run = transactionalRulesRun(t, p, b, c, transactionalRulesHandles(t, p, c, held, queries...))
					for i, q := range queries {
						args := []any{int64(1), int64(10 + i)}
						if i == 2 {
							args = args[:1]
						}
						s := fixture.Exec(q, 1, args...)
						run(s)
						if i == 0 {
							run(ruleModeValueRows("src", []any{int64(1), int64(10)}))
						}
						if i == 1 {
							run(ruleModeValueRows("src", []any{int64(1), int64(11)}))
						}
					}
					empty := ruleModeValueRows("src")
					empty.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						columns, err := rows.ColumnTypes()
						if err != nil || len(columns) != 2 {
							t.Fatal(columns, err)
						}
						for i, column := range columns {
							nullable, known := column.Nullable()
							if b.PG {
								if known || column.ScanType() != reflect.TypeOf(int32(0)) {
									t.Fatal(column.Name(), nullable, known, column.ScanType())
								}
							} else if !known || nullable != (i == 1) || column.ScanType() != reflect.TypeOf(int64(0)) {
								t.Fatal(column.Name(), nullable, known, column.ScanType())
							}
						}
					}
					run(empty)
					var inserts, changes [][]any
					if ruleModeActive(mode.code, role) {
						inserts = [][]any{{int64(1), int64(10)}}
						changes = [][]any{{int64(1), int64(11)}, {int64(1), int64(11)}}
					}
					run(ruleModeValueRows("sink1", inserts...))
					run(ruleModeValueRows("events", changes...))
					other := transactionalRulesRun(t, p, b, peer, nil)
					other(fixture.Select("SHOW session_replication_role", []string{"session_replication_role"}, []string{"TEXT"}, [][]any{{"origin"}}))
					other(fixture.Exec("INSERT INTO src VALUES(2,20)", 1))
					if ruleModeActive(mode.code, "origin") {
						inserts = append(inserts, []any{int64(2), int64(20)})
					}
					run(ruleModeValueRows("sink1", inserts...))
				})
			})
		}
	}
}
