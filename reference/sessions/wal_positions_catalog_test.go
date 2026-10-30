package reference_test

import (
 "database/sql"
 "strconv"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceWALPositionsCatalog(t *testing.T) {
 recoveryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		q := `SELECT oid::bigint AS oid,proname,pronamespace::bigint AS namespace,proowner::bigint AS owner,prolang::bigint AS language,prokind::text AS kind,prorettype::bigint AS result,proretset,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,procost,prorows,pronargdefaults,provariadic::bigint AS variadic,prosecdef,proleakproof,prosrc,proargtypes::text AS inputs,proallargtypes::text AS alltypes,proargmodes::text AS modes,proargnames::text AS names,proacl::text AS acl FROM pg_proc WHERE oid IN (2848,2849,2852,3098,3330,6305) ORDER BY oid`
		names := []string{"oid", "proname", "namespace", "owner", "language", "kind", "result", "proretset", "proisstrict", "volatility", "parallel", "procost", "prorows", "pronargdefaults", "variadic", "prosecdef", "proleakproof", "prosrc", "inputs", "alltypes", "modes", "names", "acl"}
		types := []string{"INT8", "NAME", "INT8", "INT8", "INT8", "TEXT", "INT8", "BOOL", "BOOL", "TEXT", "TEXT", "FLOAT4", "FLOAT4", "INT2", "INT8", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}
		acl := recoveryControlOwnerACL(b.PG)
		rows := [][]any{
			{int64(2848), "pg_switch_wal", int64(11), int64(10), int64(12), "f", int64(3220), false, true, "v", "s", float64(1), float64(0), int64(0), int64(0), false, false, "pg_switch_wal", "", nil, nil, nil, acl},
			{int64(2849), "pg_current_wal_lsn", int64(11), int64(10), int64(12), "f", int64(3220), false, true, "v", "s", float64(1), float64(0), int64(0), int64(0), false, false, "pg_current_wal_lsn", "", nil, nil, nil, nil},
			{int64(2852), "pg_current_wal_insert_lsn", int64(11), int64(10), int64(12), "f", int64(3220), false, true, "v", "s", float64(1), float64(0), int64(0), int64(0), false, false, "pg_current_wal_insert_lsn", "", nil, nil, nil, nil},
			{int64(3098), "pg_create_restore_point", int64(11), int64(10), int64(12), "f", int64(3220), false, true, "v", "s", float64(1), float64(0), int64(0), int64(0), false, false, "pg_create_restore_point", "25", nil, nil, nil, acl},
			{int64(3330), "pg_current_wal_flush_lsn", int64(11), int64(10), int64(12), "f", int64(3220), false, true, "v", "s", float64(1), float64(0), int64(0), int64(0), false, false, "pg_current_wal_flush_lsn", "", nil, nil, nil, nil},
			{int64(6305), "pg_log_standby_snapshot", int64(11), int64(10), int64(12), "f", int64(3220), false, true, "v", "s", float64(1), float64(0), int64(0), int64(0), false, false, "pg_log_standby_snapshot", "", nil, nil, nil, acl},
  }
  for _,step := range []fixture.Step{fixture.Select(q,names,types,rows),walPositionInitialPrivileges(b.PG)} {
   fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
   fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(step))
  }
  for _,oid := range []int{2848,2849,2852,3098,3330,6305} {
   args := ""; if oid == 3098 { args = "text" }
   id := strconv.Itoa(oid)
   step := fixture.Select("SELECT pg_get_function_arguments("+id+") AS arguments,pg_get_function_identity_arguments("+id+") AS identity,pg_get_function_result("+id+") AS result",[]string{"arguments","identity","result"},[]string{"TEXT","TEXT","TEXT"},[][]any{{args,args,"pg_lsn"}})
   replicationMessageRoutes(t,p,b,c,step)
  }
 })
}
