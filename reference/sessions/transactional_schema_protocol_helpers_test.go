//go:build !noservertests

package reference_test

import (
	"fmt"
	"testing"
)

const transactionalSchemaWireFlyway = `CREATE TABLE "public"."flyway_schema_history" (
    "installed_rank" INT NOT NULL,
    "version" VARCHAR(50),
    "description" VARCHAR(200) NOT NULL,
    "type" VARCHAR(20) NOT NULL,
    "script" VARCHAR(1000) NOT NULL,
    "checksum" INTEGER,
    "installed_by" VARCHAR(100) NOT NULL,
    "installed_on" TIMESTAMP NOT NULL DEFAULT now(),
    "execution_time" INTEGER NOT NULL,
    "success" BOOLEAN NOT NULL
)`

func transactionalSchemaWireFlywayRoutes(t *testing.T, w, peer *serverWire) {
	t.Helper()
	serverWantCode(t, w.query(t, "SET TIME ZONE 'UTC'"), "")
	for _, extended := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			serverWantCode(t, w.query(t, "BEGIN"), "")
			if extended {
				name := fmt.Sprintf("flyway_%t", commit)
				frames := serverCycle(t, w, serverParse(name, transactionalSchemaWireFlyway), serverDescribe('S', name), serverBind(name, name, nil, nil), serverDescribe('P', name))
				serverWantTags(t, frames, "1tn2nZ")
				serverScalar(t, w, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='flyway_schema_history'", "0")
				serverWantTags(t, serverCycle(t, w, serverExecute(name, 0)), "CZ")
			} else {
				serverWantCode(t, w.query(t, transactionalSchemaWireFlyway), "")
			}
			serverWantCode(t, w.query(t, `INSERT INTO public.flyway_schema_history(installed_rank,description,type,script,installed_by,execution_time,success) VALUES(1,'first','SQL','V1.sql','tester',3,true)`), "")
			serverWantCode(t, w.query(t, `INSERT INTO public.flyway_schema_history VALUES(2,'2','second','SQL','V2.sql',9,'tester',DEFAULT,4,false)`), "")
			serverWantValues(t, w.query(t, `SELECT installed_rank,version,description,type,script,checksum,installed_by,installed_on=transaction_timestamp()::timestamp,execution_time,success FROM public.flyway_schema_history ORDER BY installed_rank`), [][]string{{"1", "<NULL>", "first", "SQL", "V1.sql", "<NULL>", "tester", "t", "3", "t"}, {"2", "2", "second", "SQL", "V2.sql", "9", "tester", "t", "4", "f"}})
			serverWantValues(t, w.query(t, `SELECT a.attname,t.typname,a.atttypmod,a.attnotnull FROM pg_attribute a JOIN pg_type t ON t.oid=a.atttypid WHERE a.attrelid='public.flyway_schema_history'::regclass AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`), [][]string{{"installed_rank", "int4", "-1", "t"}, {"version", "varchar", "54", "f"}, {"description", "varchar", "204", "t"}, {"type", "varchar", "24", "t"}, {"script", "varchar", "1004", "t"}, {"checksum", "int4", "-1", "f"}, {"installed_by", "varchar", "104", "t"}, {"installed_on", "timestamp", "-1", "t"}, {"execution_time", "int4", "-1", "t"}, {"success", "bool", "-1", "t"}})
			serverScalar(t, peer, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='flyway_schema_history'", "0")
			end := "ROLLBACK"
			if commit {
				end = "COMMIT"
			}
			serverWantCode(t, w.query(t, end), "")
			if commit {
				serverScalar(t, peer, "SELECT count(*) FROM public.flyway_schema_history", "2")
				serverWantCode(t, w.query(t, "DROP TABLE public.flyway_schema_history"), "")
			} else {
				serverScalar(t, peer, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='flyway_schema_history'", "0")
			}
			serverScalar(t, w, "SELECT 42", "42")
		}
	}
}
