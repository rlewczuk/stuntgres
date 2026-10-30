package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// The fixture's initdb bootstrap role is stuntgres; native bootstrap is postgres.
// Both own fixed OID10, but ACL output correctly retains their actual names.
func recoveryControlOwnerACL(pg bool) string {
	if pg { return "{stuntgres=X/stuntgres}" }
	return "{postgres=X/postgres}"
}

func recoveryControlInitialPrivileges(pg bool) fixture.Step {
	acl := recoveryControlOwnerACL(pg)
	return fixture.Select("SELECT objoid::bigint AS oid,classoid::bigint AS class,objsubid,privtype::text AS kind,initprivs::text AS acl FROM pg_init_privs WHERE classoid=1255 AND objoid IN (1137,3071,3072,3073,3436) ORDER BY objoid", []string{"oid", "class", "objsubid", "kind", "acl"}, []string{"INT8", "INT8", "INT4", "TEXT", "TEXT"}, [][]any{
		{int64(3071), int64(1255), int64(0), "i", acl},
		{int64(3072), int64(1255), int64(0), "i", acl},
		{int64(3436), int64(1255), int64(0), "i", acl},
	})
}
func TestReferenceRecoveryControlCatalog(t *testing.T) {
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT oid::bigint AS oid,proname,pronamespace::bigint AS namespace,proowner::bigint AS owner,prolang::bigint AS language,prokind::text AS kind,prorettype::bigint AS result,proretset,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,procost,prorows,pronargdefaults,provariadic::bigint AS variadic,prosecdef,proleakproof,prosrc,proargtypes::text AS inputs,proallargtypes::text AS alltypes,proargmodes::text AS modes,proargnames::text AS names,proacl::text AS acl FROM pg_proc WHERE oid IN (1137,3071,3072,3073,3436) ORDER BY oid`
		names := []string{"oid", "proname", "namespace", "owner", "language", "kind", "result", "proretset", "proisstrict", "volatility", "parallel", "procost", "prorows", "pronargdefaults", "variadic", "prosecdef", "proleakproof", "prosrc", "inputs", "alltypes", "modes", "names", "acl"}
		types := []string{"INT8", "NAME", "INT8", "INT8", "INT8", "TEXT", "INT8", "BOOL", "BOOL", "TEXT", "TEXT", "FLOAT4", "FLOAT4", "INT2", "INT8", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}
		// Released pg_proc.dat plus system_functions.sql defaults and REVOKEs.
		acl := recoveryControlOwnerACL(b.PG)
		rows := [][]any{
			{int64(1137), "pg_get_wal_replay_pause_state", int64(11), int64(10), int64(12), "f", int64(25), false, true, "v", "s", float64(1), float64(0), int64(0), int64(0), false, false, "pg_get_wal_replay_pause_state", "", nil, nil, nil, nil},
			{int64(3071), "pg_wal_replay_pause", int64(11), int64(10), int64(12), "f", int64(2278), false, true, "v", "s", float64(1), float64(0), int64(0), int64(0), false, false, "pg_wal_replay_pause", "", nil, nil, nil, acl},
			{int64(3072), "pg_wal_replay_resume", int64(11), int64(10), int64(12), "f", int64(2278), false, true, "v", "s", float64(1), float64(0), int64(0), int64(0), false, false, "pg_wal_replay_resume", "", nil, nil, nil, acl},
			{int64(3073), "pg_is_wal_replay_paused", int64(11), int64(10), int64(12), "f", int64(16), false, true, "v", "s", float64(1), float64(0), int64(0), int64(0), false, false, "pg_is_wal_replay_paused", "", nil, nil, nil, nil},
			{int64(3436), "pg_promote", int64(11), int64(10), int64(12), "f", int64(16), false, true, "v", "s", float64(1), float64(0), int64(2), int64(0), false, false, "pg_promote", "16 23", nil, nil, "{wait,wait_seconds}", acl},
		}
		s := fixture.Select(q, names, types, rows)
		for _, step := range []fixture.Step{s, recoveryControlInitialPrivileges(b.PG), fixture.Select("SELECT pg_get_function_arguments(3436) AS arguments,pg_get_function_identity_arguments(3436) AS identity,pg_get_function_result(3436) AS result", []string{"arguments", "identity", "result"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"wait boolean DEFAULT true, wait_seconds integer DEFAULT 60", "wait boolean, wait_seconds integer", "boolean"}})} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
	})
}
