package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var statioTableNames = []string{"relid", "schemaname", "relname", "heap_blks_read", "heap_blks_hit", "idx_blks_read", "idx_blks_hit", "toast_blks_read", "toast_blks_hit", "tidx_blks_read", "tidx_blks_hit"}
var statioTableTypes = []string{"OID", "NAME", "NAME", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8"}
const statioTableProbe = "SELECT * FROM pg_catalog.pg_statio_all_tables WHERE relid=$1::oid"

type statioTableRow struct {
	oid int64
	schema, name string
	blocks [8]sql.NullInt64
}

func statioTableMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, statioTableNames) { t.Fatal("eleven released columns", names, err) }
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 11 { t.Fatal(types, err) }
	for i, typ := range types {
		want := reflect.TypeFor[int64]()
		if i == 1 || i == 2 { want = reflect.TypeFor[string]() }
		if pg && i < 3 { want = reflect.TypeFor[any]() }
		if typ.Name() != names[i] || typ.DatabaseTypeName() != statioTableTypes[i] || typ.ScanType() != want { t.Errorf("column %d: %v/%v/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType()) }
		if nullable, known := typ.Nullable(); nullable != !pg || known != !pg { t.Errorf("nullable %d: %v/%v", i, nullable, known) }
		if n, ok := typ.Length(); n != 0 || ok { t.Errorf("length %d: %d/%v", i, n, ok) }
		if p, s, ok := typ.DecimalSize(); p != 0 || s != 0 || ok { t.Errorf("precision %d: %d/%d/%v", i, p, s, ok) }
	}
}

func statioTableScan(t *testing.T, rows *sql.Rows, pg bool) statioTableRow {
	t.Helper()
	var raw [11]any
	var dest [11]any
	for i := range raw { dest[i] = &raw[i] }
	if err := rows.Scan(dest[:]...); err != nil { t.Fatal(err) }
	for i, v := range raw {
		if v == nil { if i < 5 { t.Fatalf("unexpected NULL at %d", i) }; continue }
		want := reflect.TypeFor[int64]()
		if i == 1 || i == 2 { want = reflect.TypeFor[string]() }
		if pg && i < 3 { want = reflect.TypeFor[[]byte]() }
		if reflect.TypeOf(v) != want { t.Fatalf("raw %d: %T want %v", i, v, want) }
	}
	var r statioTableRow
	var id sql.NullInt64
	if err := id.Scan(raw[0]); err != nil || !id.Valid || id.Int64 <= 0 { t.Fatal(id, err) }
	r.oid = id.Int64
	r.schema = fixture.Normalize(raw[1]).(string)
	r.name = fixture.Normalize(raw[2]).(string)
	for i := range r.blocks { if err := r.blocks[i].Scan(raw[i+3]); err != nil || (r.blocks[i].Valid && r.blocks[i].Int64 < 0) { t.Fatal("nonnegative typed counter", i, raw[i+3], err) } }
	return r
}

func statioTableRows(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, args ...any) []statioTableRow {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, q, args...)
	if err != nil { t.Fatal(q, err) }
	defer rows.Close()
	statioTableMetadata(t, rows, b.PG)
	got := []statioTableRow{}
	for rows.Next() { got = append(got, statioTableScan(t, rows, b.PG)) }
	if err := rows.Err(); err != nil { t.Fatal(err) }
	return got
}

func statioTableOne(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, oid int64) statioTableRow {
	t.Helper()
	r := statioTableRows(t, p, b, e, statioTableProbe, oid)
	if len(r) != 1 || r[0].oid != oid { t.Fatal("actual table identity", oid, r) }
	return r[0]
}

func statioTableHeap(t *testing.T, b fixture.Backend, r statioTableRow, hit int64) {
	t.Helper()
	if !b.PG && (r.blocks[0] != (sql.NullInt64{Valid:true}) || r.blocks[1] != (sql.NullInt64{Int64:hit,Valid:true})) { t.Fatal("resident logical source formula", r, hit) }
}

func statioTableAbsentToast(t *testing.T, r statioTableRow) {
	t.Helper()
	for _, i := range []int{4,5} { if r.blocks[i].Valid { t.Fatal("absent TOAST relation yields direct NULL", r) } }
	for _, i := range []int{6,7} { if r.blocks[i].Valid { t.Fatal("empty TOAST index SUM NULL", r) } }
}

func TestReferencePGStatioAllTablesShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) {
		statioSequenceExec(t,p,b,b.DB,"CREATE TABLE statio_table_shape(k integer)")
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO statio_table_shape VALUES(1),(2)",2))
		oid := statioSequenceOID(t,p,b.DB,"public.statio_table_shape")
		const byName="SELECT * FROM pg_catalog.pg_statio_all_tables WHERE relname=$1"
		for _, q := range []string{statioTableProbe,byName,"SELECT * FROM pg_statio_all_tables WHERE false","SELECT * FROM pg_statio_all_tables WHERE relname='statio_missing'"} {
			for _, prepared := range []bool{false,true} {
				var e fixture.Executor = b.DB
				if prepared { stmt,err:=b.DB.PrepareContext(p.Ctx,q); if err!=nil {t.Fatal(err)}; defer stmt.Close(); e=fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:q} }
				var args []any; if q==statioTableProbe {args=[]any{oid}}
				if q==byName {args=[]any{"statio_table_shape"}}
				got:=statioTableRows(t,p,b,e,q,args...)
				if q==statioTableProbe || q==byName { if len(got)!=1 || got[0].oid!=oid || got[0].schema!="public" || got[0].name!="statio_table_shape" {t.Fatal(got)}; statioTableAbsentToast(t,got[0]) } else if len(got)!=0 {t.Fatal(got)}
			}
		}
		// An absent relation identity is not a valid relation with zero activity:
		// the released left join feeds NULL to strict TOAST statistics functions.
		fixture.PgDescriptionExecutorRows(t,p,b,b.DB,fixture.Select("SELECT reltoastrelid FROM pg_class WHERE oid=$1::oid",[]string{"reltoastrelid"},[]string{"OID"},[][]any{{int64(0)}},oid))
		nulls:=fixture.Select("SELECT heap_blks_read IS NOT NULL AS heap_read,heap_blks_hit IS NOT NULL AS heap_hit,idx_blks_read IS NULL AS idx_read,idx_blks_hit IS NULL AS idx_hit,toast_blks_read IS NULL AS toast_read,toast_blks_hit IS NULL AS toast_hit,tidx_blks_read IS NULL AS tidx_read,tidx_blks_hit IS NULL AS tidx_hit FROM pg_statio_all_tables WHERE relid=$1::oid",[]string{"heap_read","heap_hit","idx_read","idx_hit","toast_read","toast_hit","tidx_read","tidx_hit"},[]string{"BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true,true,true,true,true}},oid)
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,nulls)
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Prepared(nulls))
		attrs:=[][]any{}
		for i,name:=range statioTableNames { typ:=int64(20); collation:=int64(0); if i==0 {typ=26}; if i==1||i==2 {typ=19;collation=950}; attrs=append(attrs,[]any{int64(i+1),name,typ,int64(-1),collation,false,false}) }
		s:=fixture.Select("SELECT attnum,attname,atttypid,atttypmod,attcollation,attnotnull,attisdropped FROM pg_attribute WHERE attrelid='pg_catalog.pg_statio_all_tables'::regclass AND attnum>0 ORDER BY attnum",[]string{"attnum","attname","atttypid","atttypmod","attcollation","attnotnull","attisdropped"},[]string{"INT2","NAME","OID","INT4","OID","BOOL","BOOL"},attrs)
		fixture.PgDescriptionExecutorRows(t,p,b,b.DB,s); fixture.PgDescriptionExecutorRows(t,p,b,b.DB,fixture.Prepared(s))
		q:="SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link,c.relowner=t.typowner AS owner_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_statio_all_tables'::regclass"
		s=fixture.Select(q,[]string{"relkind","relnatts","relation_link","array_link","element_link","owner_link"},[]string{"CHAR","INT2","BOOL","BOOL","BOOL","BOOL"},[][]any{{"v",int64(11),true,true,true,true}})
		fixture.PgDescriptionExecutorRows(t,p,b,b.DB,s); fixture.PgDescriptionExecutorRows(t,p,b,b.DB,fixture.Prepared(s))
		if !b.PG { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_statio_all_tables'::regclass",[]string{"oid","reltype","typarray"},[]string{"OID","OID","OID"},[][]any{{int64(12174),int64(12176),int64(12175)}})) }
		const definition = ` SELECT c.oid AS relid,
    n.nspname AS schemaname,
    c.relname,
    (pg_stat_get_blocks_fetched(c.oid) - pg_stat_get_blocks_hit(c.oid)) AS heap_blks_read,
    pg_stat_get_blocks_hit(c.oid) AS heap_blks_hit,
    i.idx_blks_read,
    i.idx_blks_hit,
    (pg_stat_get_blocks_fetched(t.oid) - pg_stat_get_blocks_hit(t.oid)) AS toast_blks_read,
    pg_stat_get_blocks_hit(t.oid) AS toast_blks_hit,
    x.idx_blks_read AS tidx_blks_read,
    x.idx_blks_hit AS tidx_blks_hit
   FROM ((((pg_class c
     LEFT JOIN pg_class t ON ((c.reltoastrelid = t.oid)))
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
     LEFT JOIN LATERAL ( SELECT (sum((pg_stat_get_blocks_fetched(pg_index.indexrelid) - pg_stat_get_blocks_hit(pg_index.indexrelid))))::bigint AS idx_blks_read,
            (sum(pg_stat_get_blocks_hit(pg_index.indexrelid)))::bigint AS idx_blks_hit
           FROM pg_index
          WHERE (pg_index.indrelid = c.oid)) i ON (true))
     LEFT JOIN LATERAL ( SELECT (sum((pg_stat_get_blocks_fetched(pg_index.indexrelid) - pg_stat_get_blocks_hit(pg_index.indexrelid))))::bigint AS idx_blks_read,
            (sum(pg_stat_get_blocks_hit(pg_index.indexrelid)))::bigint AS idx_blks_hit
           FROM pg_index
          WHERE (pg_index.indrelid = t.oid)) x ON (true))
  WHERE (c.relkind = ANY (ARRAY['r'::"char", 't'::"char", 'm'::"char"]));`
		s=fixture.Select("SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_statio_all_tables'",[]string{"definition"},[]string{"TEXT"},[][]any{{definition}})
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Prepared(s))
	}) }
}
