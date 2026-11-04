package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const pgSeclabelsSelect = "SELECT objoid,classoid,objsubid,objtype,objnamespace,objname,provider,label FROM pg_catalog.pg_seclabels"
var pgSeclabelsColumns = []string{"objoid", "classoid", "objsubid", "objtype", "objnamespace", "objname", "provider", "label"}
var pgSeclabelsTypes = []string{"OID", "OID", "INT4", "TEXT", "OID", "TEXT", "TEXT", "TEXT"}

func pgSeclabelsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	pgSeclabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		t.Cleanup(func() {
			c.ExecContext(p.Ctx, "ROLLBACK")
			c.ExecContext(p.Ctx, "RESET ROLE")
			for _, name := range []string{"pg_seclabels", "pg_seclabel", "pg_shseclabel"} { c.ExecContext(p.Ctx, "GRANT SELECT ON pg_catalog."+name+" TO PUBLIC") }
		})
		body(t, p, b, c)
	})
}

func pgSeclabelsDescriptors(t *testing.T, b fixture.Backend, columns []*sql.ColumnType) {
	t.Helper()
	if len(columns) != 8 { t.Fatal("eight view descriptors", columns) }
	for i, column := range columns {
		if column.Name() != pgSeclabelsColumns[i] || column.DatabaseTypeName() != pgSeclabelsTypes[i] { t.Fatal("view descriptor", i, column) }
		nullable, known := column.Nullable()
		// pq has no nullable field in RowDescription. The native carrier does.
		if b.PG && known || !b.PG && (!known || !nullable) { t.Fatal("nullable view descriptor", i, nullable, known) }
	}
}

func pgSeclabelsMetadata(b fixture.Backend, q string, args ...any) fixture.Step {
	s := fixture.Select(q, pgSeclabelsColumns, pgSeclabelsTypes, nil, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes(); if err != nil { t.Fatal(err) }
		pgSeclabelsDescriptors(t, b, columns)
	}
	return s
}

func pgSeclabelsEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	for _, name := range []string{"pg_seclabels", "pg_seclabel", "pg_shseclabel"} {
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT count(*) FROM pg_catalog."+name, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
}

func TestReferencePGSeclabelsMetadata(t *testing.T) {
	pgSeclabelsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{pgSeclabelsSelect, "SELECT * FROM pg_catalog.pg_seclabels", "SELECT * FROM pg_seclabels", pgSeclabelsSelect+" WHERE FALSE", pgSeclabelsSelect+" LIMIT 0"} {
				s := pgSeclabelsMetadata(b, q); s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			for _, s := range []fixture.Step{
				fixture.Select("SELECT label,provider,objname,objnamespace,objtype,objsubid,classoid,objoid FROM pg_seclabels", []string{"label","provider","objname","objnamespace","objtype","objsubid","classoid","objoid"}, []string{"TEXT","TEXT","TEXT","OID","TEXT","INT4","OID","OID"}, nil),
				fixture.Select("SELECT provider,label FROM pg_seclabels", []string{"provider","label"}, []string{"TEXT","TEXT"}, nil),
				fixture.Select("SELECT count(*) FROM pg_seclabels", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
			} { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		}
		held := pgSeclabelHeld(t, p, c, pgSeclabelsSelect+" WHERE provider=$1")
		for _, arg := range []any{"missing", "selinux", nil, "missing"} { fixture.RunStep(t, p.Ctx, held, b.Recorder, pgSeclabelsMetadata(b, held.SQL, arg)) }
		rows, err := c.QueryContext(p.Ctx, pgSeclabelsSelect); if err != nil { t.Fatal(err) }; defer rows.Close()
		columns, err := rows.ColumnTypes(); if err != nil { t.Fatal(err) }
		names, err := rows.Columns(); if err != nil { t.Fatal(err) }
		if rows.Next() || rows.Err() != nil { t.Fatal("empty EOF", rows.Err()) }
		if err := rows.Close(); err != nil { t.Fatal(err) }
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgSeclabelsMetadata(b, held.SQL, "missing"))
		pgSeclabelsDescriptors(t, b, columns)
		if !reflect.DeepEqual(names, pgSeclabelsColumns) { t.Fatal("retained names", names) }
		pgSeclabelsEmpty(t, p, b, c)
	})
}

func pgSeclabelsShape(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	steps := []fixture.Step{
		fixture.Select("SELECT n.oid::bigint,c.relname::text,c.relkind::text,c.relpersistence::text,c.relisshared,c.relhasindex,c.relrowsecurity,c.relforcerowsecurity,c.reloptions IS NULL AS no_options,c.relnatts,c.relowner=boot.relowner AS bootstrap_owner FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_class boot ON boot.oid=1259 WHERE c.oid='pg_catalog.pg_seclabels'::regclass", []string{"oid","relname","relkind","relpersistence","relisshared","relhasindex","relrowsecurity","relforcerowsecurity","no_options","relnatts","bootstrap_owner"}, []string{"INT8","TEXT","TEXT","TEXT","BOOL","BOOL","BOOL","BOOL","BOOL","INT2","BOOL"}, [][]any{{int64(11),"pg_seclabels","v","p",false,false,false,false,true,int64(8),true}}),
		fixture.Select("SELECT t.typname::text,t.typtype::text,t.typrelid=c.oid AS owns_relation,c.reltype='pg_catalog.pg_seclabels'::regtype AS nominal,a.typname::text,a.typelem=t.oid AS owns_element,t.typarray=a.oid AS owns_array FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_seclabels'::regclass", []string{"typname","typtype","owns_relation","nominal","typname","owns_element","owns_array"}, []string{"TEXT","TEXT","BOOL","BOOL","TEXT","BOOL","BOOL"}, [][]any{{"pg_seclabels","c",true,true,"_pg_seclabels",true,true}}),
		fixture.Select("SELECT viewname::text FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND viewname='pg_seclabels'", []string{"viewname"}, []string{"TEXT"}, [][]any{{"pg_seclabels"}}),
		fixture.Select("SELECT tablename::text FROM pg_catalog.pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_seclabels'", []string{"tablename"}, []string{"TEXT"}, nil),
		fixture.Select("SELECT check_option,is_updatable,is_insertable_into,is_trigger_updatable,is_trigger_deletable,is_trigger_insertable_into FROM information_schema.views WHERE table_schema='pg_catalog' AND table_name='pg_seclabels'", []string{"check_option","is_updatable","is_insertable_into","is_trigger_updatable","is_trigger_deletable","is_trigger_insertable_into"}, []string{"VARCHAR","VARCHAR","VARCHAR","VARCHAR","VARCHAR","VARCHAR"}, [][]any{{"NONE","NO","NO","NO","NO","NO"}}),
		fixture.Select("SELECT has_table_privilege(c.relowner,c.oid,'SELECT') AS owner_select,has_table_privilege(c.relowner,c.oid,'INSERT') AS owner_insert,has_table_privilege(c.relowner,c.oid,'UPDATE') AS owner_update,has_table_privilege(c.relowner,c.oid,'DELETE') AS owner_delete,c.relacl IS NOT NULL AS explicit_acl FROM pg_catalog.pg_class c WHERE c.oid='pg_catalog.pg_seclabels'::regclass", []string{"owner_select","owner_insert","owner_update","owner_delete","explicit_acl"}, []string{"BOOL","BOOL","BOOL","BOOL","BOOL"}, [][]any{{true,true,true,true,true}}),
	}
	var attrs, info [][]any
	for i, name := range pgSeclabelsColumns {
		typ, coll, udt := int64(26), int64(0), "oid"
		if i == 2 { typ, udt = 23, "int4" }
		if i == 3 || i >= 5 { typ, coll, udt = 25, 100, "text" }
		if i >= 5 { coll = 950 }
		attrs = append(attrs, []any{int64(i+1),name,typ,false,int64(-1),int64(0),coll})
		info = append(info, []any{int64(i+1),name,"YES",udt})
	}
	steps = append(steps,
		fixture.Select("SELECT attnum::int4,attname::text,atttypid::bigint,attnotnull,atttypmod,attndims,attcollation::bigint FROM pg_catalog.pg_attribute WHERE attrelid='pg_catalog.pg_seclabels'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum","attname","atttypid","attnotnull","atttypmod","attndims","attcollation"}, []string{"INT4","TEXT","INT8","BOOL","INT4","INT2","INT8"}, attrs),
		fixture.Select("SELECT ordinal_position,column_name::text,is_nullable::text,udt_name::text FROM information_schema.columns WHERE table_schema='pg_catalog' AND table_name='pg_seclabels' ORDER BY ordinal_position", []string{"ordinal_position","column_name","is_nullable","udt_name"}, []string{"INT4","TEXT","TEXT","TEXT"}, info))
	if !b.PG { steps = append(steps, fixture.Select("SELECT c.oid::bigint,c.reltype::bigint,t.typarray::bigint FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_seclabels'::regclass", []string{"oid","reltype","typarray"}, []string{"INT8","INT8","INT8"}, [][]any{{int64(12099),int64(12101),int64(12100)}})) }
	for _, s := range steps { fixture.RunStep(t,p.Ctx,c,b.Recorder,s); fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s)) }
}

func TestReferencePGSeclabelsShape(t *testing.T) { pgSeclabelsScenario(t, func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) { pgSeclabelsShape(t,p,b,c) }) }
