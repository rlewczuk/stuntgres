package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceReplicationMessagesCatalogMetadata(t *testing.T) {
	const role = "replication_messages_catalog_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE "+role, 0))
		q := `SELECT oid::bigint AS oid,proname,pronamespace::bigint AS namespace,proowner::bigint AS owner,prolang::bigint AS language,prokind::text AS kind,prorettype::bigint AS result,proretset,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,procost,prorows,pronargdefaults,provariadic::bigint AS variadic,prosecdef,proleakproof,prosrc,proargtypes::text AS inputs,proallargtypes::text AS alltypes,proargmodes::text AS modes,proargnames::text AS names,proacl::text AS acl FROM pg_proc WHERE oid IN (3577,3578,6344) ORDER BY oid`
		names := []string{"oid", "proname", "namespace", "owner", "language", "kind", "result", "proretset", "proisstrict", "volatility", "parallel", "procost", "prorows", "pronargdefaults", "variadic", "prosecdef", "proleakproof", "prosrc", "inputs", "alltypes", "modes", "names", "acl"}
		types := []string{"INT8", "NAME", "INT8", "INT8", "INT8", "TEXT", "INT8", "BOOL", "BOOL", "TEXT", "TEXT", "FLOAT4", "FLOAT4", "INT2", "INT8", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}
		rows := [][]any{
			{int64(3577), "pg_logical_emit_message", int64(11), int64(10), int64(12), "f", int64(3220), false, true, "v", "u", float64(1), float64(0), int64(1), int64(0), false, false, "pg_logical_emit_message_text", "16 25 25 16", nil, nil, "{transactional,prefix,message,flush}", nil},
			{int64(3578), "pg_logical_emit_message", int64(11), int64(10), int64(12), "f", int64(3220), false, true, "v", "u", float64(1), float64(0), int64(1), int64(0), false, false, "pg_logical_emit_message_bytea", "16 25 17 16", nil, nil, "{transactional,prefix,message,flush}", nil},
			{int64(6344), "pg_sync_replication_slots", int64(11), int64(10), int64(12), "f", int64(2278), false, true, "v", "u", float64(1), float64(0), int64(0), int64(0), false, false, "pg_sync_replication_slots", "", nil, nil, nil, nil},
		}
		s := fixture.Select(q, names, types, rows)
		run(s)
		run(fixture.Prepared(s))
		for i, identity := range replicationMessageIdentities {
			s := fixture.Select("SELECT has_function_privilege('"+role+"','"+identity+"','EXECUTE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{true}})
			run(s)
			run(fixture.Prepared(s))
			oid := []string{"3577", "3578", "6344"}[i]
			args, result := "", "void"
			if i < 2 {
				message := "text"
				if i == 1 {
					message = "bytea"
				}
				args = "transactional boolean, prefix text, message " + message + ", flush boolean DEFAULT false"
				result = "pg_lsn"
			}
			s = fixture.Select("SELECT pg_get_function_arguments("+oid+") AS arguments,pg_get_function_result("+oid+") AS result", []string{"arguments", "result"}, []string{"TEXT", "TEXT"}, [][]any{{args, result}})
			run(s)
			run(fixture.Prepared(s))
		}
		parameters := fixture.Select(`SELECT specific_name,ordinal_position,parameter_mode,parameter_name,data_type,udt_name,parameter_default FROM information_schema.parameters WHERE specific_schema='pg_catalog' AND specific_name IN ('pg_logical_emit_message_3577','pg_logical_emit_message_3578','pg_sync_replication_slots_6344') ORDER BY specific_name,ordinal_position`, []string{"specific_name", "ordinal_position", "parameter_mode", "parameter_name", "data_type", "udt_name", "parameter_default"}, []string{"NAME", "INT4", "VARCHAR", "NAME", "VARCHAR", "NAME", "VARCHAR"}, [][]any{
			{"pg_logical_emit_message_3577", int64(1), "IN", "transactional", "boolean", "bool", nil}, {"pg_logical_emit_message_3577", int64(2), "IN", "prefix", "text", "text", nil}, {"pg_logical_emit_message_3577", int64(3), "IN", "message", "text", "text", nil}, {"pg_logical_emit_message_3577", int64(4), "IN", "flush", "boolean", "bool", "false"},
			{"pg_logical_emit_message_3578", int64(1), "IN", "transactional", "boolean", "bool", nil}, {"pg_logical_emit_message_3578", int64(2), "IN", "prefix", "text", "text", nil}, {"pg_logical_emit_message_3578", int64(3), "IN", "message", "bytea", "bytea", nil}, {"pg_logical_emit_message_3578", int64(4), "IN", "flush", "boolean", "bool", "false"},
		})
		run(parameters)
		run(fixture.Prepared(parameters))
		for _, aliases := range []bool{false, true} {
			q := `SELECT pg_logical_emit_message(true,'p','x'),pg_logical_emit_message(false,'p','\x00'::bytea,true),pg_sync_replication_slots() WHERE FALSE`
			names := []string{"pg_logical_emit_message", "pg_logical_emit_message", "pg_sync_replication_slots"}
			if aliases {
				q = `SELECT pg_logical_emit_message(true,'p','x') AS a,pg_logical_emit_message(false,'p','\x00'::bytea,true) AS b,pg_sync_replication_slots() AS c WHERE FALSE`
				names = []string{"a", "b", "c"}
			}
			s := fixture.Select(q, names, []string{"PG_LSN", "PG_LSN", "VOID"}, nil)
			s.CheckMetadata = replicationMessagesMetadata(b.PG, names)
			replicationMessageRoutes(t, p, b, c, s)
		}
	})
}
