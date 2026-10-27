package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRuleModePrepared(t *testing.T) {
	t.Run("mode-and-role", func(t *testing.T) {
		ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			c := ruleModeConn(t, p, b)
			run := transactionalRulesRun(t, p, b, c, nil)
			ruleModeSetup(run)
			run(fixture.Exec("CREATE TABLE events(id INT,v INT)", 0))
			run(fixture.Exec("CREATE RULE u AS ON UPDATE TO src DO ALSO INSERT INTO events VALUES(NEW.id,NEW.v)", 0))
			run(fixture.Exec("CREATE RULE d AS ON DELETE TO src DO ALSO INSERT INTO events VALUES(OLD.id,OLD.v)", 0))
			queries := []string{"INSERT INTO src VALUES($1,$2) RETURNING v AS out", "UPDATE src SET v=$2 WHERE id=$1 RETURNING v AS out", "DELETE FROM src WHERE id=$1 RETURNING v AS out"}
			h := transactionalRulesHandles(t, p, c, held, queries...)
			run = transactionalRulesRun(t, p, b, c, h)
			run(fixture.Exec("PREPARE rm_sql(INT,INT) AS INSERT INTO src VALUES($1,$2)", 0))
			id := int64(0)
			var audit, events [][]any
			for _, mode := range ruleModeCases {
				for _, name := range []string{"r", "u", "d"} {
					run(fixture.Exec("ALTER TABLE src "+mode.action+" "+name, 0))
				}
				for _, role := range []string{"origin", "replica", "local"} {
					run(fixture.Exec("SET session_replication_role="+role, 0))
					id++
					run(fixture.Select(queries[0], []string{"out"}, []string{"INT4"}, [][]any{{int64(10)}}, id, int64(10)))
					run(fixture.Select(queries[1], []string{"out"}, []string{"INT4"}, [][]any{{int64(11)}}, id, int64(11)))
					run(fixture.Select(queries[2], []string{"out"}, []string{"INT4"}, [][]any{{int64(11)}}, id))
					if ruleModeActive(mode.code, role) {
						audit = append(audit, []any{id, int64(10)})
						events = append(events, []any{id, int64(11)}, []any{id, int64(11)})
					}
					// SQL PREPARE is separately held on this physical owner.
					run(fixture.Exec("EXECUTE rm_sql("+ruleModeInt(id+100)+",20)", 1))
					if ruleModeActive(mode.code, role) {
						audit = append(audit, []any{id + 100, int64(20)})
					}
				}
			}
			s := ruleModeValueRows("sink1", audit...)
			s.Bag = true
			run(s)
			s = ruleModeValueRows("events", events...)
			s.Bag = true
			run(s)
			run(fixture.Exec("DEALLOCATE rm_sql", 0))
		})
	})
	for _, transition := range []string{"reset", "default", "reset-all", "set-config", "round-trip", "local-commit", "local-rollback", "savepoint", "routine-save"} {
		t.Run(transition, func(t *testing.T) {
			ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				c := ruleModeConn(t, p, b)
				run := transactionalRulesRun(t, p, b, c, nil)
				ruleModeSetup(run)
				q := "INSERT INTO src VALUES($1,$2) RETURNING v AS out"
				h := transactionalRulesHandles(t, p, c, held, q)
				run = transactionalRulesRun(t, p, b, c, h)
				insert := func(id int64) {
					run(fixture.Select(q, []string{"out"}, []string{"INT4"}, [][]any{{id * 10}}, id, id*10))
				}
				insert(1)
				// These role changes have no intervening DML. Equality of final setting
				// must not allow stale accepted plans to survive a real transition.
				switch transition {
				case "reset", "default", "reset-all", "set-config", "round-trip":
					run(fixture.Exec("SET session_replication_role=replica", 0))
					if transition != "round-trip" {
						insert(2)
					}
					switch transition {
					case "reset":
						run(fixture.Exec("RESET session_replication_role", 0))
					case "default":
						run(fixture.Exec("SET session_replication_role=DEFAULT", 0))
					case "reset-all":
						run(fixture.Exec("RESET ALL", 0))
					case "set-config":
						run(fixture.Select("SELECT set_config('session_replication_role','origin',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"origin"}}))
					case "round-trip":
						run(fixture.Exec("SET session_replication_role=origin", 0))
					}
				case "local-commit", "local-rollback":
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("SET LOCAL session_replication_role=replica", 0))
					insert(2)
					end := "COMMIT"
					if transition == "local-rollback" {
						end = "ROLLBACK"
					}
					run(fixture.Exec(end, 0))
				case "savepoint":
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("SAVEPOINT settings", 0))
					run(fixture.Exec("SET LOCAL session_replication_role=replica", 0))
					insert(2)
					run(fixture.Exec("ROLLBACK TO settings", 0))
					insert(3)
					run(fixture.Exec("COMMIT", 0))
				case "routine-save":
					run(fixture.Exec("CREATE FUNCTION rm_saved() RETURNS INT LANGUAGE SQL SET session_replication_role=replica AS $$ INSERT INTO src VALUES(2,20) RETURNING v $$", 0))
					run(fixture.Select("SELECT rm_saved()", []string{"rm_saved"}, []string{"INT4"}, [][]any{{int64(20)}}))
				}
				insert(4)
				want := [][]any{{int64(1), int64(10)}}
				if transition == "savepoint" {
					want = append(want, []any{int64(3), int64(30)})
				}
				want = append(want, []any{int64(4), int64(40)})
				run(ruleModeValueRows("sink1", want...))
				run(fixture.Select("SHOW session_replication_role", []string{"session_replication_role"}, []string{"TEXT"}, [][]any{{"origin"}}))
			})
		})
	}
	t.Run("eager-lifetime", func(t *testing.T) {
		ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := transactionalRulesRun(t, p, b, c, nil)
			ruleModeSetup(run)
			run(fixture.Exec("INSERT INTO src VALUES(1,10)", 1))
			q := "SELECT id,v FROM src ORDER BY id"
			h := transactionalRulesHandles(t, p, c, held, q)
			var rows *sql.Rows
			var err error
			if held {
				rows, err = h[q].QueryContext(p.Ctx)
			} else {
				rows, err = c.QueryContext(p.Ctx, q)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			// pq streams, native owns eager rows. A distinct session avoids a busy pq borrower.
			if _, err := peer.ExecContext(p.Ctx, "ALTER TABLE src DISABLE RULE r"); err != nil {
				t.Fatal(err)
			}
			columns, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(columns, []string{"id", "v"}) {
				t.Fatal(columns, err)
			}
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var id, v int64
			if err := rows.Scan(&id, &v); err != nil || id != 1 || v != 10 {
				t.Fatal(id, v, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			rows.Close()
			run(ruleModeCatalog("D"))
		})
	})
}

func ruleModeInt(n int64) string { return strconv.FormatInt(n, 10) }
