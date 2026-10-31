package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var aiosNames = []string{"pid", "io_id", "io_generation", "state", "operation", "off", "length", "target", "handle_data_len", "raw_result", "result", "target_desc", "f_sync", "f_localmem", "f_buffered"}
var aiosTypes = []string{"INT4", "INT4", "INT8", "TEXT", "TEXT", "INT8", "INT8", "TEXT", "INT2", "INT4", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}
var aiosTypeIDs = []int64{23, 23, 20, 25, 25, 20, 20, 25, 21, 23, 25, 25, 16, 16, 16}
const aiosQuery = "SELECT * FROM pg_catalog.pg_aios"

func aiosMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, aiosNames) { t.Fatal("AIO columns", names, err) }
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 15 { t.Fatal(types, err) }
	for i, typ := range types {
		if typ.DatabaseTypeName() != aiosTypes[i] { t.Fatal(i, typ.DatabaseTypeName()) }
		if !pg { nullable, known := typ.Nullable(); if !known || !nullable { t.Fatal("nullable view attribute", i, nullable, known) } }
	}
}

func aiosRows(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	defer rows.Close()
	aiosMetadata(t, rows, pg)
	count := 0
	for rows.Next() {
		values := make([]any, 15); dest := make([]any, 15)
		for i := range values { dest[i] = &values[i] }
		if err := rows.Scan(dest...); err != nil { t.Fatal(err) }
		for i := range values { values[i] = fixture.Normalize(values[i]) }
		if !pg { t.Fatal("logical work fabricated an AIO handle", values) }
		if values[0] == nil || values[1] == nil || values[2] == nil { t.Fatal("missing handle identity", values) }
		state, ok := values[3].(string)
		if !ok || !strings.Contains("|HANDED_OUT|DEFINED|STAGED|SUBMITTED|COMPLETED_IO|COMPLETED_SHARED|COMPLETED_LOCAL|", "|"+state+"|") { t.Fatal("handle state", values) }
		if state == "HANDED_OUT" {
			for _, value := range values[4:] { if value != nil { t.Fatal("unprepared handle detail", values) } }
		} else {
			if values[4] != "invalid" && values[4] != "readv" && values[4] != "writev" { t.Fatal("operation", values) }
			if values[4] == "invalid" && (values[5] != nil || values[6] != nil) { t.Fatal("invalid operation extent", values) }
			if !strings.HasPrefix(state, "COMPLETED_") && values[9] != nil { t.Fatal("premature raw result", values) }
		}
		count++
	}
	if err := rows.Err(); err != nil { t.Fatal(err) }
	if !pg && count != 0 { t.Fatal(count) }
}

func aiosRead(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, prepared bool) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, prepErr := e.PrepareContext(p.Ctx, aiosQuery)
		if prepErr != nil { t.Fatal(prepErr) }
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.Ctx)
	} else { rows, err = e.QueryContext(p.Ctx, aiosQuery) }
	if err != nil { t.Fatal(err) }
	aiosRows(t, rows, b.PG)
}

func aiosCount(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		q := "SELECT count(*) FROM pg_catalog.pg_aios"
		if !b.PG {
			s := fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
		} else {
			var count int64
			var rows *sql.Rows
			var err error
			if prepared { stmt, prepErr := e.PrepareContext(p.Ctx, q); if prepErr != nil { t.Fatal(prepErr) }; defer stmt.Close(); rows, err = stmt.QueryContext(p.Ctx) } else { rows, err = e.QueryContext(p.Ctx, q) }
			if err != nil { t.Fatal(err) }
			if !rows.Next() { t.Fatal("missing aggregate", rows.Err()) }
			if err := rows.Scan(&count); err != nil { t.Fatal(err) }
			if rows.Next() || rows.Err() != nil { t.Fatal("aggregate cardinality", rows.Err()) }
			rows.Close()
			if count < 0 { t.Fatal("negative live inventory", count) }
		}
	}
}

func aiosSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT n FROM aios_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
}

func TestReferencePGAIOSEmptyInventory(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) {
		aiosCount(t, p, b, b.DB)
		pgSequenceExec(t, p, b, b.DB, "CREATE TABLE aios_sentinel(n integer)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO aios_sentinel VALUES(42)", 1))
		aiosSentinel(t, p, b, b.DB)
		for _, prepared := range []bool{false, true} { aiosRead(t, p, b, b.DB, prepared) }
		aiosCount(t, p, b, b.DB)
	}) }
}

func TestReferencePGAIOSMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) {
		for _, prefix := range []string{"", "pg_catalog."} {
			s := fixture.Select("SELECT * FROM "+prefix+"pg_aios LIMIT 0", aiosNames, aiosTypes, [][]any{})
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { aiosMetadata(t, rows, b.PG) }
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
		q := "SELECT c.relkind::text,c.relnamespace::bigint,t.typrelid=c.oid AS owns_row,a.typelem=t.oid AS owns_array FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_aios'::regclass"
		s := fixture.Select(q, []string{"relkind","relnamespace","owns_row","owns_array"}, []string{"TEXT","INT8","BOOL","BOOL"}, [][]any{{"v",int64(11),true,true}})
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s); fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		var attrs [][]any
		for i, name := range aiosNames { collation := int64(0); if aiosTypeIDs[i] == 25 { collation = 100 }; attrs = append(attrs, []any{int64(i+1),name,aiosTypeIDs[i],false,int64(-1),int64(0),collation}) }
		s = fixture.Select("SELECT attnum::integer,attname::text,atttypid::bigint,attnotnull,atttypmod::integer,attndims::integer,attcollation::bigint FROM pg_attribute WHERE attrelid='pg_catalog.pg_aios'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum","attname","atttypid","attnotnull","atttypmod","attndims","attcollation"}, []string{"INT4","TEXT","INT8","BOOL","INT4","INT4","INT8"}, attrs)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s); fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		aiosRoutineMetadata(t, p, b)
		aiosDefinitions(t, p, b)
		aiosRead(t, p, b, b.DB, false)
	}) }
}
