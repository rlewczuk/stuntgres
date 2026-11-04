package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const slotsSelect = "SELECT slot_name,plugin,slot_type,datoid,database,temporary,active,active_pid,xmin,catalog_xmin,restart_lsn,confirmed_flush_lsn,wal_status,safe_wal_size,two_phase,two_phase_at,inactive_since,conflicting,invalidation_reason,failover,synced FROM pg_catalog.pg_replication_slots"

var slotsColumns = []string{"slot_name", "plugin", "slot_type", "datoid", "database", "temporary", "active", "active_pid", "xmin", "catalog_xmin", "restart_lsn", "confirmed_flush_lsn", "wal_status", "safe_wal_size", "two_phase", "two_phase_at", "inactive_since", "conflicting", "invalidation_reason", "failover", "synced"}
var slotsTypes = []string{"NAME", "NAME", "TEXT", "OID", "NAME", "BOOL", "BOOL", "INT4", "XID", "XID", "PG_LSN", "PG_LSN", "TEXT", "INT8", "BOOL", "PG_LSN", "TIMESTAMPTZ", "BOOL", "TEXT", "BOOL", "BOOL"}
var slotsTypeOIDs = []int64{19, 19, 25, 26, 19, 16, 16, 23, 28, 28, 3220, 3220, 25, 20, 16, 3220, 1184, 16, 25, 16, 16}

func slotsRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func slotsMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, slotsColumns) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 21 {
		t.Fatal(types, err)
	}
	for i, typ := range types {
		nullable, known := typ.Nullable()
		if typ.Name() != slotsColumns[i] || typ.DatabaseTypeName() != slotsTypes[i] || pg && (nullable || known) || !pg && (!nullable || !known) {
			t.Fatal("slot metadata", i, typ, nullable, known)
		}
	}
}

func slotsEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.Select(query, slotsColumns, slotsTypes, nil)
		s.Prepared = prepared
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) { slotsMetadata(t, r, b.PG) }
		slotsRun(t, p, b, e, s)
	}
}

func slotsInitial(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	owner := "postgres"
	if b.PG {
		owner = "stuntgres"
	}
	acl := "{" + owner + "=arwdDxtm/" + owner + ",=r/" + owner + "}"
	for _, q := range []string{
		"SELECT relacl::text AS v FROM pg_class WHERE oid='pg_catalog.pg_replication_slots'::regclass",
		"SELECT initprivs::text AS v FROM pg_init_privs WHERE classoid=1259 AND objoid='pg_catalog.pg_replication_slots'::regclass AND objsubid=0 AND privtype='i'",
	} {
		slotsRun(t, p, b, e, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{acl}}))
	}
	slotsRun(t, p, b, e, fixture.Select("SELECT proacl::text FROM pg_proc WHERE oid=3781", []string{"proacl"}, []string{"TEXT"}, [][]any{{nil}}))
	slotsRun(t, p, b, e, fixture.Select("SELECT initprivs::text FROM pg_init_privs WHERE classoid=1255 AND objoid=3781 AND objsubid=0 AND privtype='i'", []string{"initprivs"}, []string{"TEXT"}, nil))
}

func TestReferencePGReplicationSlotsShapeIdentity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"slots_shape_actor"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, q := range []string{slotsSelect, "SELECT * FROM pg_replication_slots", "SELECT * FROM pg_catalog.pg_replication_slots LIMIT 0"} {
				slotsEmpty(t, p, b, b.DB, q)
			}
			count := fixture.Select("SELECT count(*) AS slots,count(restart_lsn) AS retained,count(confirmed_flush_lsn) AS confirmed FROM pg_catalog.pg_replication_slots", []string{"slots", "retained", "confirmed"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(0), int64(0), int64(0)}})
			slotsRun(t, p, b, b.DB, count)
			slotsRun(t, p, b, b.DB, fixture.Prepared(count))
			stmt, err := b.DB.PrepareContext(p.Ctx, slotsSelect)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: slotsSelect}
			slotsEmpty(t, p, b, held, slotsSelect)
			owner := "postgres"
			if b.PG {
				owner = "stuntgres"
			}
			var attrs [][]any
			var outTypes, outNames, outModes []string
			for i, name := range slotsColumns {
				collation := int64(0)
				if slotsTypeOIDs[i] == 19 {
					collation = 950
				} else if slotsTypeOIDs[i] == 25 {
					collation = 100
				}
				attrs = append(attrs, []any{int64(i + 1), name, slotsTypeOIDs[i], int64(-1), int64(0), collation, false, false})
				if name != "database" {
					outTypes = append(outTypes, strconv.FormatInt(slotsTypeOIDs[i], 10))
					outNames = append(outNames, name)
					outModes = append(outModes, "o")
				}
			}
			for _, s := range []fixture.Step{
				fixture.Select("SELECT relkind::text,relnamespace::bigint,relnatts::int4,relhasindex,relisshared,relowner::regrole::text FROM pg_class WHERE oid='pg_catalog.pg_replication_slots'::regclass", []string{"relkind", "relnamespace", "relnatts", "relhasindex", "relisshared", "relowner"}, []string{"TEXT", "INT8", "INT4", "BOOL", "BOOL", "TEXT"}, [][]any{{"v", int64(11), int64(21), false, false, owner}}),
				fixture.Select("SELECT attnum::int4,attname::text,atttypid::bigint,atttypmod::int4,attndims::int4,attcollation::bigint,attnotnull,attisdropped FROM pg_attribute WHERE attrelid='pg_catalog.pg_replication_slots'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attndims", "attcollation", "attnotnull", "attisdropped"}, []string{"INT4", "TEXT", "INT8", "INT4", "INT4", "INT8", "BOOL", "BOOL"}, attrs),
				fixture.Select("SELECT t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,c.reltype='pg_catalog.pg_replication_slots'::regtype AS nominal,t.typarray='pg_catalog.pg_replication_slots[]'::regtype AS array_link,t.typtype::text,t.typname::text FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_replication_slots'::regclass", []string{"relation_link", "element_link", "nominal", "array_link", "typtype", "typname"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{true, true, true, true, "c", "pg_replication_slots"}}),
				fixture.Select("SELECT oid::bigint,proname::text,prorettype::bigint,pronargs::int4,proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text,proretset,proisstrict,provolatile::text,proparallel::text,prorows,procost,pronargdefaults::int4,provariadic::bigint,prosecdef,proacl::text FROM pg_proc WHERE oid=3781", []string{"oid", "proname", "prorettype", "pronargs", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "proretset", "proisstrict", "provolatile", "proparallel", "prorows", "procost", "pronargdefaults", "provariadic", "prosecdef", "proacl"}, []string{"INT8", "TEXT", "INT8", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "FLOAT4", "FLOAT4", "INT4", "INT8", "BOOL", "TEXT"}, [][]any{{int64(3781), "pg_get_replication_slots", int64(2249), int64(0), "", "{" + strings.Join(outTypes, ",") + "}", "{" + strings.Join(outModes, ",") + "}", "{" + strings.Join(outNames, ",") + "}", true, false, "s", "s", float64(10), float64(1), int64(0), int64(0), false, nil}}),
			} {
				slotsRun(t, p, b, b.DB, s)
				slotsRun(t, p, b, b.DB, fixture.Prepared(s))
			}
			if !b.PG {
				slotsRun(t, p, b, b.DB, fixture.Select("SELECT 'pg_catalog.pg_replication_slots'::regclass::bigint AS relation,'pg_catalog.pg_replication_slots'::regtype::bigint AS rowtype,'pg_catalog.pg_replication_slots[]'::regtype::bigint AS arraytype", []string{"relation", "rowtype", "arraytype"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(12261), int64(12263), int64(12262)}}))
			}
			slotsInitial(t, p, b, b.DB)
			slotsRun(t, p, b, b.DB, fixture.Exec("CREATE ROLE slots_shape_actor", 0))
			slotsRun(t, p, b, b.DB, fixture.Select("SELECT has_table_privilege('slots_shape_actor','pg_catalog.pg_replication_slots','SELECT') AS can_select,has_function_privilege('slots_shape_actor','pg_catalog.pg_get_replication_slots()','EXECUTE') AS can_execute", []string{"can_select", "can_execute"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		})
	}
}
