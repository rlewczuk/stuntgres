package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePLpgSQLTxControlChainAndFrames(t *testing.T) {
	for _, command := range []string{"COMMIT", "ROLLBACK"} {
		for _, ending := range []string{"", " AND CHAIN", " AND NO CHAIN"} {
			t.Run(command+ending, func(t *testing.T) {
				proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					resultsExec(t, p, b, c, "SET default_transaction_isolation TO 'read committed'", "SET default_transaction_read_only TO off", "SET default_transaction_deferrable TO off",
						"CREATE PROCEDURE tx_modes(OUT tx_isolation text,OUT ro text,OUT def text) LANGUAGE plpgsql AS $$BEGIN ROLLBACK;SET TRANSACTION ISOLATION LEVEL REPEATABLE READ,READ ONLY,DEFERRABLE;"+command+ending+";tx_isolation:=current_setting('transaction_isolation');ro:=current_setting('transaction_read_only');def:=current_setting('transaction_deferrable');END$$")
					isolation, ro, def := "read committed", "off", "off"
					if ending == " AND CHAIN" {
						isolation, ro, def = "repeatable read", "on", "on"
					}
					proceduresBoth(t, p, b, c, "CALL tx_modes(NULL,NULL,NULL)", []string{"tx_isolation", "ro", "def"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{isolation, ro, def}})
					resultsExec(t, p, b, c, "CREATE PROCEDURE tx_values(INOUT x integer,OUT a integer[],OUT note text) LANGUAGE plpgsql AS $$BEGIN a:=ARRAY[1,NULL,3];note:=NULL;x:=x+1;"+command+ending+";IF x<>11 OR a[1]<>1 OR a[3]<>3 OR note IS NOT NULL THEN RAISE EXCEPTION 'lost frame';END IF;x:=x-2;END$$")
					proceduresBoth(t, p, b, c, "CALL tx_values(10,NULL,NULL)", []string{"x", "a", "note"}, []string{"INT4", "_INT4", "TEXT"}, [][]any{{int64(9), "{1,NULL,3}", nil}})
				})
			})
		}
	}
	proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsExec(t, p, b, c, "CREATE TABLE tx_log(v integer)",
			"CREATE PROCEDURE tx_child() LANGUAGE plpgsql AS $$BEGIN INSERT INTO tx_log VALUES(2);CREATE TABLE tx_discard(v integer);ROLLBACK;INSERT INTO tx_log VALUES(3);COMMIT;END$$",
			"CREATE PROCEDURE tx_parent() LANGUAGE plpgsql AS $$BEGIN INSERT INTO tx_log VALUES(1);CALL tx_child();IF EXISTS(SELECT 1 FROM tx_log WHERE v IN(1,2)) OR EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname='tx_discard') THEN RAISE EXCEPTION 'stale transaction';END IF;INSERT INTO tx_log VALUES(4);END$$")
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("CALL tx_parent()", 0)
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			txControlRows(t, p, b, c, 3, 4)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 2))
		}
	})
	proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsExec(t, p, b, c, "SET application_name TO 'baseline'", "CREATE PROCEDURE tx_environment(OUT local_cleared boolean,OUT session_kept boolean,OUT same_statement boolean,OUT ordered_transaction boolean) LANGUAGE plpgsql AS $$DECLARE root_statement timestamptz:=statement_timestamp();old_transaction timestamptz:=transaction_timestamp();BEGIN SET application_name TO 'session';SET LOCAL application_name TO 'local';COMMIT;local_cleared:=current_setting('application_name')='session';same_statement:=statement_timestamp()=root_statement;ordered_transaction:=transaction_timestamp()>=old_transaction;SET LOCAL application_name TO 'temporary';ROLLBACK;session_kept:=current_setting('application_name')='session';END$$")
		proceduresBoth(t, p, b, c, "CALL tx_environment(NULL,NULL,NULL,NULL)", []string{"local_cleared", "session_kept", "same_statement", "ordered_transaction"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}})
		resultsExec(t, p, b, c, "RESET application_name")
	})
}
