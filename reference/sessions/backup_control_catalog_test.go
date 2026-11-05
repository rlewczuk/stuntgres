package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func backupCatalog(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := `SELECT oid::bigint AS oid,proname,pronamespace::bigint AS namespace,proowner::bigint AS owner,prolang::bigint AS language,prokind::text AS kind,prorettype::bigint AS result,proretset,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,procost,prorows,pronargdefaults,provariadic::bigint AS variadic,prosecdef,proleakproof,prosrc,proargtypes::text AS inputs,proallargtypes::text AS alltypes,proargmodes::text AS modes,proargnames::text AS names,proacl::text AS acl FROM pg_proc WHERE proname IN ('pg_backup_start','pg_backup_stop') ORDER BY oid`
	names := []string{"oid", "proname", "namespace", "owner", "language", "kind", "result", "proretset", "proisstrict", "volatility", "parallel", "procost", "prorows", "pronargdefaults", "variadic", "prosecdef", "proleakproof", "prosrc", "inputs", "alltypes", "modes", "names", "acl"}
	types := []string{"INT8", "NAME", "INT8", "INT8", "INT8", "TEXT", "INT8", "BOOL", "BOOL", "TEXT", "TEXT", "FLOAT4", "FLOAT4", "INT2", "INT8", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}
	rows := [][]any{
		{int64(2172), "pg_backup_start", int64(11), int64(10), int64(12), "f", int64(3220), false, true, "v", "r", float64(1), float64(0), int64(1), int64(0), false, false, "pg_backup_start", "25 16", nil, nil, "{label,fast}", recoveryControlOwnerACL(b.PG)},
		{int64(2739), "pg_backup_stop", int64(11), int64(10), int64(12), "f", int64(2249), false, true, "v", "r", float64(1), float64(0), int64(1), int64(0), false, false, "pg_backup_stop", "16", "{16,3220,25,25}", "{i,o,o,o}", "{wait_for_archive,lsn,labelfile,spcmapfile}", recoveryControlOwnerACL(b.PG)},
	}
	backupRoutes(t, p, b, c, fixture.Select(q, names, types, rows))
	backupRoutes(t, p, b, c, fixture.Select("SELECT oid::bigint AS oid,pg_get_function_arguments(oid) AS arguments,pg_get_function_identity_arguments(oid) AS identity,pg_get_function_result(oid) AS result FROM pg_proc WHERE oid IN (2172,2739) ORDER BY oid", []string{"oid", "arguments", "identity", "result"}, []string{"INT8", "TEXT", "TEXT", "TEXT"}, [][]any{
		{int64(2172), "label text, fast boolean DEFAULT false", "label text, fast boolean", "pg_lsn"},
		{int64(2739), "wait_for_archive boolean DEFAULT true, OUT lsn pg_lsn, OUT labelfile text, OUT spcmapfile text", "wait_for_archive boolean, OUT lsn pg_lsn, OUT labelfile text, OUT spcmapfile text", "record"},
	}))
}
