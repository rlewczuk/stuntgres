package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const originStatusSelect = "SELECT local_id,external_id,remote_lsn,local_lsn FROM pg_catalog.pg_replication_origin_status"
const originStatusDefinition = " SELECT local_id,\n    external_id,\n    remote_lsn,\n    local_lsn\n   FROM pg_show_replication_origin_status() pg_show_replication_origin_status(local_id, external_id, remote_lsn, local_lsn);"

var originStatusColumns = []string{"local_id", "external_id", "remote_lsn", "local_lsn"}
var originStatusTypes = []string{"OID", "TEXT", "PG_LSN", "PG_LSN"}

func originStatusCleanup(t *testing.T, e fixture.Executor, queries ...string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		for _, query := range queries {
			if _, err := e.ExecContext(ctx, query); err != nil {
				t.Error(err)
			}
		}
	})
}

func originStatusMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, originStatusColumns) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 4 {
		t.Fatal(types, err)
	}
	for i, typ := range types {
		nullable, known := typ.Nullable()
		if typ.Name() != originStatusColumns[i] || typ.DatabaseTypeName() != originStatusTypes[i] || pg && (nullable || known) || !pg && (!nullable || !known) {
			t.Fatal("raw status metadata", i, typ, nullable, known)
		}
	}
}

func originStatusRows(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string, want [][]any, args ...any) {
	t.Helper()
	// OID normalization is confined to its ordinary public adapter helper;
	// raw descriptors are asserted independently before scanning any values.
	for _, prepared := range []bool{false, true} {
		executor := e
		var stmt *sql.Stmt
		if prepared {
			var err error
			stmt, err = e.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			executor = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: query}
		}
		rows, err := executor.QueryContext(p.Ctx, query, args...)
		if err != nil {
			if stmt != nil {
				stmt.Close()
			}
			t.Fatal(err)
		}
		originStatusMetadata(t, rows, b.PG)
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		s := fixture.Select(query, originStatusColumns, originStatusTypes, want, args...)
		fixture.OidReferenceRows(t, p.Ctx, b, executor, s)
		if stmt != nil {
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func originStatusAdvance(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, name, lsn string) {
	t.Helper()
	originStateRun(t, p, b, e, fixture.Exec("SELECT pg_replication_origin_advance($1::text,$2::pg_lsn)", 1, name, lsn))
}

func originStatusInitial(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	owner := "postgres"
	if b.PG {
		owner = "stuntgres"
	}
	for _, tc := range []struct{ query, acl string }{
		{"SELECT initprivs::text AS v FROM pg_init_privs WHERE classoid=1255 AND objoid=6014 AND objsubid=0 AND privtype='i'", "{" + owner + "=X/" + owner + "}"},
		{"SELECT initprivs::text AS v FROM pg_init_privs WHERE classoid=1259 AND objoid='pg_catalog.pg_replication_origin_status'::regclass AND objsubid=0 AND privtype='i'", "{" + owner + "=arwdDxtm/" + owner + "}"},
	} {
		originStateRun(t, p, b, e, originStateScalar(tc.query, "TEXT", tc.acl))
	}
}

func TestReferencePGReplicationOriginStatusShapeIdentity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			originStateCleanup(t, b.DB, "status_shape")
			for _, query := range []string{originStatusSelect, "SELECT * FROM pg_replication_origin_status", "SELECT * FROM pg_catalog.pg_replication_origin_status LIMIT 0"} {
				originStatusRows(t, p, b, b.DB, query, nil)
			}
			owner := "postgres"
			if b.PG {
				owner = "stuntgres"
			}
			steps := []fixture.Step{
				fixture.Select("SELECT relkind::text,relnamespace::bigint,relnatts::int4,relhasindex,relisshared,relowner::regrole::text FROM pg_class WHERE oid='pg_catalog.pg_replication_origin_status'::regclass", []string{"relkind", "relnamespace", "relnatts", "relhasindex", "relisshared", "relowner"}, []string{"TEXT", "INT8", "INT4", "BOOL", "BOOL", "TEXT"}, [][]any{{"v", int64(11), int64(4), false, false, owner}}),
				fixture.Select("SELECT relacl::text AS v FROM pg_class WHERE oid='pg_catalog.pg_replication_origin_status'::regclass", []string{"v"}, []string{"TEXT"}, [][]any{{"{" + owner + "=arwdDxtm/" + owner + "}"}}),
				fixture.Select("SELECT attnum::int4,attname::text,atttypid::bigint,atttypmod::int4,attndims::int4,attcollation::bigint,attnotnull,attisdropped FROM pg_attribute WHERE attrelid='pg_catalog.pg_replication_origin_status'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attndims", "attcollation", "attnotnull", "attisdropped"}, []string{"INT4", "TEXT", "INT8", "INT4", "INT4", "INT8", "BOOL", "BOOL"}, [][]any{{int64(1), "local_id", int64(26), int64(-1), int64(0), int64(0), false, false}, {int64(2), "external_id", int64(25), int64(-1), int64(0), int64(100), false, false}, {int64(3), "remote_lsn", int64(3220), int64(-1), int64(0), int64(0), false, false}, {int64(4), "local_lsn", int64(3220), int64(-1), int64(0), int64(0), false, false}}),
				fixture.Select("SELECT t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,c.reltype='pg_catalog.pg_replication_origin_status'::regtype AS nominal,t.typarray='pg_catalog.pg_replication_origin_status[]'::regtype AS array_link,t.typtype::text,t.typname::text FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_replication_origin_status'::regclass", []string{"relation_link", "element_link", "nominal", "array_link", "typtype", "typname"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{true, true, true, true, "c", "pg_replication_origin_status"}}),
				fixture.Select("SELECT pg_get_viewdef('pg_catalog.pg_replication_origin_status'::regclass,false) AS nonpretty,pg_get_viewdef('pg_catalog.pg_replication_origin_status'::regclass,true) AS pretty,definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_replication_origin_status'", []string{"nonpretty", "pretty", "definition"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{originStatusDefinition, originStatusDefinition, originStatusDefinition}}),
				fixture.Select("SELECT oid::bigint,proname::text,prorettype::bigint,pronargs::int4,proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text,proretset,proisstrict,provolatile::text,proparallel::text,prorows,procost,pronargdefaults::int4,provariadic::bigint,prosecdef,proacl::text FROM pg_proc WHERE oid=6014", []string{"oid", "proname", "prorettype", "pronargs", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "proretset", "proisstrict", "provolatile", "proparallel", "prorows", "procost", "pronargdefaults", "provariadic", "prosecdef", "proacl"}, []string{"INT8", "TEXT", "INT8", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "FLOAT4", "FLOAT4", "INT4", "INT8", "BOOL", "TEXT"}, [][]any{{int64(6014), "pg_show_replication_origin_status", int64(2249), int64(0), "", "{26,25,3220,3220}", "{o,o,o,o}", "{local_id,external_id,remote_lsn,local_lsn}", true, false, "v", "r", float64(100), float64(1), int64(0), int64(0), false, "{" + owner + "=X/" + owner + "}"}}),
			}
			for _, s := range steps {
				originStateRun(t, p, b, b.DB, s)
				originStateRun(t, p, b, b.DB, fixture.Prepared(s))
			}
			if !b.PG {
				originStateRun(t, p, b, b.DB, fixture.Select("SELECT 'pg_catalog.pg_replication_origin_status'::regclass::bigint AS relation,'pg_catalog.pg_replication_origin_status'::regtype::bigint AS rowtype,'pg_catalog.pg_replication_origin_status[]'::regtype::bigint AS arraytype", []string{"relation", "rowtype", "arraytype"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(12343), int64(12345), int64(12344)}}))
			}
			originStatusInitial(t, p, b, b.DB)
			id := originStateCreate(t, p, b, b.DB, "status_shape")
			originStatusAdvance(t, p, b, b.DB, "status_shape", "0/2")
			originStatusRows(t, p, b, b.DB, originStatusSelect, [][]any{{id, "status_shape", "0/2", "0/0"}})
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			originStatusCleanup(t, c, "RESET search_path", "RESET quote_all_identifiers")
			originStateRun(t, p, b, c, fixture.Exec("SET search_path=public,pg_catalog", 0))
			originStateRun(t, p, b, c, fixture.Exec("CREATE FUNCTION public.pg_show_replication_origin_status() RETURNS integer LANGUAGE SQL AS 'SELECT 1'", 0))
			qualified := " SELECT local_id,\n    external_id,\n    remote_lsn,\n    local_lsn\n   FROM pg_catalog.pg_show_replication_origin_status() pg_show_replication_origin_status(local_id, external_id, remote_lsn, local_lsn);"
			originStateRun(t, p, b, c, originStateScalar("SELECT pg_get_viewdef('pg_catalog.pg_replication_origin_status'::regclass) AS v", "TEXT", qualified))
			originStatusRows(t, p, b, c, originStatusSelect, [][]any{{id, "status_shape", "0/2", "0/0"}})
			originStateRun(t, p, b, c, fixture.Exec("SET quote_all_identifiers=on", 0))
			quoted := " SELECT \"local_id\",\n    \"external_id\",\n    \"remote_lsn\",\n    \"local_lsn\"\n   FROM \"pg_catalog\".\"pg_show_replication_origin_status\"() \"pg_show_replication_origin_status\"(\"local_id\", \"external_id\", \"remote_lsn\", \"local_lsn\");"
			display := originStateScalar("SELECT pg_get_viewdef('pg_catalog.pg_replication_origin_status'::regclass) AS v", "TEXT", quoted)
			originStateRun(t, p, b, c, display)
			originStateRun(t, p, b, c, fixture.Prepared(display))
		})
	}
}
