package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceReplicationMessagesNullsStored(t *testing.T) {
	for _, binary := range []bool{false, true} {
		label := "text"
		message := "'x'::text"
		if binary {
			label = "bytea"
			message = `'\x00ff'::bytea`
		}
		t.Run(label, func(t *testing.T) {
			recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, call := range []string{
					"pg_logical_emit_message(NULL::boolean,'p'," + message + ")",
					"pg_logical_emit_message(true,NULL::text," + message + ")",
					"pg_logical_emit_message(true,'p',NULL::" + label + ")",
					"pg_logical_emit_message(true,'p'," + message + ",NULL::boolean)",
					"pg_logical_emit_message(true,'p'," + message + ",NULL)",
					"pg_logical_emit_message(NULL,'p'," + message + ")",
				} {
					replicationMessageRoutes(t, p, b, c, replicationMessageNull("SELECT "+call+" AS emitted"))
				}
				for _, tc := range []struct {
					call string
					arg  any
				}{
					{"pg_logical_emit_message($1::boolean,'p'," + message + ")", nil},
					{"pg_logical_emit_message(true,$1::text," + message + ")", nil},
					{"pg_logical_emit_message(true,'p',$1::" + label + ")", nil},
					{"pg_logical_emit_message(true,'p'," + message + ",$1::boolean)", nil},
				} {
					replicationMessageRoutes(t, p, b, c, replicationMessageNull("SELECT "+tc.call+" AS emitted", tc.arg))
				}
				if binary {
					replicationMessageRoutes(t, p, b, c, replicationMessageNull("SELECT pg_logical_emit_message(true,'p',$1::bytea) AS emitted", []byte(nil)))
				}
				run(fixture.Exec("CREATE TABLE replication_messages_nulls(t boolean,p text,m "+label+",f boolean)", 0))
				run(fixture.Exec("INSERT INTO replication_messages_nulls VALUES(NULL,'p',"+message+",false),(true,NULL,"+message+",false),(true,'p',NULL,false),(true,'p',"+message+",NULL)", 4))
				s := fixture.Select("SELECT pg_logical_emit_message(t,p,m,f) AS emitted FROM replication_messages_nulls", []string{"emitted"}, []string{"PG_LSN"}, [][]any{{nil}, {nil}, {nil}, {nil}})
				s.Bag = true
				replicationMessageRoutes(t, p, b, c, s)
				replicationMessageRoutes(t, p, b, c, fixture.Select("SELECT COALESCE(pg_logical_emit_message(true,'p',NULL::"+label+"),'0/1'::pg_lsn) AS emitted", []string{"emitted"}, []string{"PG_LSN"}, [][]any{{"0/1"}}))
				replicationMessageRoutes(t, p, b, c, fixture.Select("SELECT * FROM pg_logical_emit_message(true,'p',NULL::"+label+")", []string{"pg_logical_emit_message"}, []string{"PG_LSN"}, [][]any{{nil}}))
			})
		})
	}
	t.Run("stored-and-cancellation", func(t *testing.T) {
		recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE VIEW replication_messages_view AS SELECT pg_logical_emit_message(message=>NULL::text,prefix=>'p',transactional=>false) AS a,pg_logical_emit_message(flush=>NULL,message=>'\\x00'::bytea,prefix=>'p',transactional=>true) AS b", 0))
			s := fixture.Select("SELECT a,b FROM replication_messages_view", []string{"a", "b"}, []string{"PG_LSN", "PG_LSN"}, [][]any{{nil, nil}})
			replicationMessageRoutes(t, p, b, c, s)
			var definition string
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('replication_messages_view'::regclass)").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"message =>", "prefix =>", "transactional =>", "flush =>"} {
				if !strings.Contains(definition, name) {
					t.Fatal("stored actual input names", definition)
				}
			}
			if b.PG {
				return
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			replicationMessageRoutes(t, p, b, c, s)
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				t.Fatal(err)
			}
			if dump.String() != redump.String() {
				t.Fatalf("unstable named typed recipe\n%s\n%s", dump.String(), redump.String())
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := c.QueryContext(ctx, "SELECT pg_logical_emit_message(true,'p','x')")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled request published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary context error", err)
			}
			run(s)
		})
	})
}
