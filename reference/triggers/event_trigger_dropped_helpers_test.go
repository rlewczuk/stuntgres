package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var droppedTriggerNames = []string{"et_do_drop", "et_do_end", "et_do_start", "et_do_disabled"}
var droppedColumns = []string{"classid", "objid", "objsubid", "original", "normal", "is_temporary", "object_type", "schema_name", "object_name", "object_identity", "address_names", "address_args"}
var droppedTypes = []string{"OID", "OID", "INT4", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "_TEXT", "_TEXT"}

const droppedContextMessage = "pg_event_trigger_dropped_objects() can only be called in a sql_drop event trigger function"

// Expected logical records come from the released dependency/identity contracts,
// not from helper output. Only generated OIDs are read from before-drop catalogs.
type droppedRecord struct {
	class, oid, sub             int64
	original, normal, temporary bool
	kind                        string
	schema, name                any
	identity                    string
	names, args                 []string
	lookup                      string
}

type droppedCase struct {
	t *testing.T
	p *fixture.Pair
	b fixture.Backend
	c *sql.Conn
}

func droppedScenario(t *testing.T, body func(droppedCase)) {
	t.Helper()
	eventTriggerScenario(t, droppedTriggerNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		body(droppedCase{t, p, b, c})
	})
}

func (d droppedCase) run(s fixture.Step) {
	d.t.Helper()
	fixture.RunStep(d.t, d.p.Ctx, d.c, d.b.Recorder, s)
}
func (d droppedCase) exec(queries ...string) {
	d.t.Helper()
	for _, q := range queries {
		d.run(fixture.Exec(q, 0))
	}
}
func droppedLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func droppedIdent(s string) string   { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// Same text-array quoting convention as the public object-address family.
func droppedArray(xs []string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		if x == "" || strings.EqualFold(x, "NULL") || strings.ContainsAny(x, " ,{}\"\\\t\n\r") {
			x = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(x) + `"`
		}
		parts[i] = x
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func (d droppedCase) setup() {
	d.exec(
		"CREATE TABLE public.et_do_audit(pass integer,classid oid,objid oid,objsubid integer,original boolean,normal boolean,is_temporary boolean,object_type text,schema_name text,object_name text,object_identity text,address_names text[],address_args text[])",
		"CREATE TABLE public.et_do_phase(phase text,n bigint,remaining bigint)",
		"CREATE FUNCTION public.et_do_read(p integer) RETURNS integer LANGUAGE SQL AS $$INSERT INTO public.et_do_audit SELECT p,d.* FROM pg_event_trigger_dropped_objects() d; SELECT 1$$",
		"CREATE FUNCTION public.et_do_drop_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT public.et_do_read(1) INTO n; SELECT public.et_do_read(2) INTO n; INSERT INTO public.et_do_phase SELECT 'drop',count(*),0 FROM public.et_do_audit WHERE pass=1; END$$",
		"CREATE FUNCTION public.et_do_end_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.et_do_phase SELECT 'end',count(*),0 FROM public.et_do_audit WHERE pass=1; END$$",
	)
}

func (d droppedCase) install(tags string) {
	eventTriggerCreate(d.t, d.p, d.b, d.c, "et_do_drop", "sql_drop", tags, "public.et_do_drop_fn")
	eventTriggerCreate(d.t, d.p, d.b, d.c, "et_do_end", "ddl_command_end", tags, "public.et_do_end_fn")
}

// Catalog absence is tested inside sql_drop, not inferred from a later SELECT.
// Columns use their real attnum; whole relations/types/defaults/constraints use
// their own public catalog OIDs. This also catches falsely reported survivors.
func (d droppedCase) absenceCallback() {
	present := []string{
		"(a.classid=1259 AND a.objsubid=0 AND EXISTS(SELECT 1 FROM pg_class c WHERE c.oid=a.objid))",
		"(a.classid=1259 AND a.objsubid>0 AND EXISTS(SELECT 1 FROM pg_attribute c WHERE c.attrelid=a.objid AND c.attnum=a.objsubid AND NOT c.attisdropped))",
	}
	for _, catalog := range []struct {
		class int
		name  string
	}{
		{1247, "pg_type"}, {2615, "pg_namespace"}, {2604, "pg_attrdef"},
		{2606, "pg_constraint"}, {1255, "pg_proc"}, {2618, "pg_rewrite"},
		{2620, "pg_trigger"}, {3256, "pg_policy"},
		{3600, "pg_ts_dict"}, {3602, "pg_ts_config"}, {3764, "pg_ts_template"},
		{2605, "pg_cast"},
	} {
		present = append(present, fmt.Sprintf("(a.classid=%d AND EXISTS(SELECT 1 FROM %s c WHERE c.oid=a.objid))", catalog.class, catalog.name))
	}
	d.exec("CREATE OR REPLACE FUNCTION public.et_do_drop_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT public.et_do_read(1) INTO n; SELECT public.et_do_read(2) INTO n; INSERT INTO public.et_do_phase SELECT 'drop',count(*),(SELECT count(*) FROM public.et_do_audit a WHERE a.pass=1 AND (" + strings.Join(present, " OR ") + ")) FROM public.et_do_audit WHERE pass=1; END$$")
}

func (d droppedCase) capture(rs []droppedRecord) []droppedRecord {
	d.t.Helper()
	for i := range rs {
		rows, err := d.c.QueryContext(d.p.Ctx, "SELECT oid::bigint FROM "+rs[i].lookup)
		if err != nil {
			d.t.Fatal(rs[i].identity, err)
		}
		if !rows.Next() {
			rows.Close()
			d.t.Fatal("missing pre-drop address", rs[i].identity)
		}
		if err := rows.Scan(&rs[i].oid); err != nil {
			rows.Close()
			d.t.Fatal(err)
		}
		if rs[i].oid <= 0 || rows.Next() {
			rows.Close()
			d.t.Fatal("nonunique pre-drop address", rs[i].identity)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			d.t.Fatal(err)
		}
		rows.Close()
	}
	return rs
}

func (r droppedRecord) row(postgres bool) []any {
	var class, oid any = r.class, r.oid
	if postgres {
		class, oid = fmt.Sprint(r.class), fmt.Sprint(r.oid)
	}
	return []any{class, oid, r.sub, r.original, r.normal, r.temporary, r.kind, r.schema, r.name, r.identity, droppedArray(r.names), droppedArray(r.args)}
}

func (d droppedCase) bag(pass int, rs []droppedRecord) {
	d.t.Helper()
	rows := make([][]any, len(rs))
	for i, r := range rs {
		rows[i] = r.row(d.b.PG)
	}
	s := fixture.Select(fmt.Sprintf("SELECT %s FROM public.et_do_audit WHERE pass=%d", strings.Join(droppedColumns, ","), pass), droppedColumns, droppedTypes, rows)
	s.Bag = true
	d.run(s)
}

func (d droppedCase) phases(n int) {
	d.t.Helper()
	d.run(fixture.Select("SELECT phase,n,remaining FROM public.et_do_phase ORDER BY phase", []string{"phase", "n", "remaining"}, []string{"TEXT", "INT8", "INT8"}, [][]any{{"drop", int64(n), int64(0)}, {"end", int64(n), int64(0)}}))
}
func (d droppedCase) clear() { d.exec("TRUNCATE public.et_do_audit", "TRUNCATE public.et_do_phase") }

func (d droppedCase) drop(q string, prepared bool) {
	d.t.Helper()
	if !prepared {
		d.run(fixture.Exec(q, 0))
		return
	}
	// Real held Prepare must neither execute DDL nor invoke callbacks.
	var before int64
	if err := d.c.QueryRowContext(d.p.Ctx, "SELECT count(*) FROM public.et_do_audit").Scan(&before); err != nil {
		d.t.Fatal(err)
	}
	stmt, err := d.c.PrepareContext(d.p.Ctx, q)
	if err != nil {
		d.t.Fatal("prepare", err)
	}
	defer stmt.Close()
	d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_audit", before))
	fixture.RunStep(d.t, d.p.Ctx, fixture.HeldStatement{Executor: d.c, Statement: stmt, SQL: q}, d.b.Recorder, fixture.Exec(q, 0))
}

func droppedRelationLookup(schema, name string) string {
	if schema == "pg_temp" {
		return "pg_class WHERE relname=" + droppedLiteral(name) + " AND relnamespace=pg_my_temp_schema()"
	}
	return "pg_class WHERE relname=" + droppedLiteral(name) + " AND relnamespace=(SELECT oid FROM pg_namespace WHERE nspname=" + droppedLiteral(schema) + ")"
}
func droppedTypeLookup(schema, name string) string {
	if schema == "pg_temp" {
		return "pg_type WHERE typname=" + droppedLiteral(name) + " AND typnamespace=pg_my_temp_schema()"
	}
	return "pg_type WHERE typname=" + droppedLiteral(name) + " AND typnamespace=(SELECT oid FROM pg_namespace WHERE nspname=" + droppedLiteral(schema) + ")"
}
func droppedQualified(schema, name string) string {
	quote := func(s string) string {
		if s != strings.ToLower(s) || strings.ContainsAny(s, " !\"-.") {
			return droppedIdent(s)
		}
		return s
	}
	return quote(schema) + "." + quote(name)
}
func droppedTable(schema, name string, pkDefault bool) []droppedRecord {
	identity := droppedQualified(schema, name)
	tmp := schema == "pg_temp"
	rs := []droppedRecord{
		{class: 1259, original: true, temporary: tmp, kind: "table", schema: schema, name: name, identity: identity, names: []string{schema, name}, lookup: droppedRelationLookup(schema, name)},
		{class: 1247, temporary: tmp, kind: "type", schema: schema, name: name, identity: identity, names: []string{identity}, lookup: droppedTypeLookup(schema, name)},
		{class: 1247, temporary: tmp, kind: "type", schema: schema, name: "_" + name, identity: identity + "[]", names: []string{identity + "[]"}, lookup: droppedTypeLookup(schema, "_"+name)},
	}
	if pkDefault {
		rel := "(SELECT oid FROM " + droppedRelationLookup(schema, name) + ")"
		for _, spec := range []struct {
			class                  int64
			kind, identity, lookup string
			names                  []string
			simple                 any
		}{
			{2604, "default value", "for " + identity + ".f2", "pg_attrdef WHERE adrelid=" + rel + " AND adnum=2", []string{schema, name, "f2"}, nil},
			{2606, "table constraint", name + "_f1_not_null on " + identity, "pg_constraint WHERE conrelid=" + rel + " AND conname=" + droppedLiteral(name+"_f1_not_null"), []string{schema, name, name + "_f1_not_null"}, nil},
			{2606, "table constraint", name + "_pkey on " + identity, "pg_constraint WHERE conrelid=" + rel + " AND conname=" + droppedLiteral(name+"_pkey"), []string{schema, name, name + "_pkey"}, nil},
			{1259, "index", droppedQualified(schema, name+"_pkey"), droppedRelationLookup(schema, name+"_pkey"), []string{schema, name + "_pkey"}, name + "_pkey"},
		} {
			rs = append(rs, droppedRecord{class: spec.class, temporary: tmp, kind: spec.kind, schema: schema, name: spec.simple, identity: spec.identity, names: spec.names, lookup: spec.lookup})
		}
	}
	return rs
}
func droppedSchema(schema, table string) []droppedRecord {
	rs := []droppedRecord{{class: 2615, original: true, kind: "schema", name: schema, identity: schema, names: []string{schema}, lookup: "pg_namespace WHERE nspname=" + droppedLiteral(schema)}}
	children := droppedTable(schema, table, false)
	children[0].original = false
	children[0].normal = true
	return append(rs, children...)
}
func droppedPayload(schema, table, name, kind, column string, original, normal bool) droppedRecord {
	rel := "(SELECT oid FROM " + droppedRelationLookup(schema, table) + ")"
	r := droppedRecord{class: 2606, original: original, normal: normal, kind: kind, schema: schema, identity: name + " on " + droppedQualified(schema, table), names: []string{schema, table, name}, lookup: "pg_constraint WHERE conrelid=" + rel + " AND conname=" + droppedLiteral(name)}
	if kind == "default value" {
		r.class = 2604
		r.identity = "for " + droppedQualified(schema, table) + "." + column
		r.names = []string{schema, table, column}
		r.lookup = "pg_attrdef WHERE adrelid=" + rel + " AND adnum=(SELECT attnum FROM pg_attribute WHERE attrelid=" + rel + " AND attname=" + droppedLiteral(column) + ")"
	}
	return r
}
func droppedColumn(schema, table, column string, sub int64) droppedRecord {
	identity := droppedQualified(schema, table) + "." + column
	if column != strings.ToLower(column) || strings.ContainsAny(column, " !\"-.") {
		identity = droppedQualified(schema, table) + "." + droppedIdent(column)
	}
	return droppedRecord{class: 1259, sub: sub, original: true, kind: "table column", schema: schema, identity: identity, names: []string{schema, table, column}, lookup: droppedRelationLookup(schema, table)}
}
func droppedProtocolDiagnostic(context string) fixture.Diagnostic {
	d := eventTriggerDiagnostic("39P03", droppedContextMessage)
	d.Context = context
	return d
}

func (d droppedCase) foreignKeyTriggers() []droppedRecord {
	d.t.Helper()
	rows, err := d.c.QueryContext(d.p.Ctx, "SELECT t.oid::bigint,t.tgname,c.relname FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE t.tgconstraint=(SELECT oid FROM pg_constraint WHERE conname='et_do_fk') AND t.tgisinternal ORDER BY t.tgname")
	if err != nil {
		d.t.Fatal(err)
	}
	defer rows.Close()
	var rs []droppedRecord
	for rows.Next() {
		var oid int64
		var name, table string
		if err := rows.Scan(&oid, &name, &table); err != nil {
			d.t.Fatal(err)
		}
		rs = append(rs, droppedRecord{class: 2620, oid: oid, kind: "trigger", schema: "public", identity: droppedIdent(name) + " on public." + table, names: []string{"public", table, name}, lookup: "pg_trigger WHERE oid=" + fmt.Sprint(oid)})
	}
	if err := rows.Err(); err != nil {
		d.t.Fatal(err)
	}
	if len(rs) != 4 {
		d.t.Fatalf("FK must own four real logical triggers, got %d", len(rs))
	}
	return rs
}
