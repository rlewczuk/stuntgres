package reference_test

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

var backendMemoryNames = []string{"name", "ident", "type", "level", "path", "total_bytes", "total_nblocks", "free_bytes", "free_chunks", "used_bytes"}
var backendMemoryTypes = []string{"TEXT", "TEXT", "TEXT", "INT4", "_INT4", "INT8", "INT8", "INT8", "INT8", "INT8"}
var backendMemoryTypeIDs = []int64{25, 25, 25, 23, 1007, 20, 20, 20, 20, 20}
const backendMemoryView = "SELECT * FROM pg_catalog.pg_backend_memory_contexts"
const backendMemoryFunction = "SELECT * FROM pg_catalog.pg_get_backend_memory_contexts()"

// Public SQL expectations deliberately do not imitate PostgreSQL allocator sizes.
// Exact selected Go owner layouts are independently checked in private owner units.
type backendMemoryRow struct {
	Name, Type string
	Ident sql.NullString
	Level int64
	Path []int64
	Total, Blocks, Free, Chunks, Used sql.NullInt64
}

func backendMemoryMetadata(t *testing.T, rows *sql.Rows, pg, view bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, backendMemoryNames) { t.Fatal("memory columns", names, err) }
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 10 { t.Fatal("memory descriptors", columns, err) }
	for i, c := range columns {
		if c.Name() != backendMemoryNames[i] || c.DatabaseTypeName() != backendMemoryTypes[i] { t.Fatalf("column %d: %s %s", i, c.Name(), c.DatabaseTypeName()) }
		scan:=reflect.TypeFor[int64]()
		length,lengthKnown:=int64(0),false
		switch backendMemoryTypes[i] {
		case "TEXT":
			scan=reflect.TypeFor[string]()
			length,lengthKnown=math.MaxInt64,true
		case "INT4":
			if pg{scan=reflect.TypeFor[int32]()}
		case "_INT4":
			scan=reflect.TypeFor[string]()
			if pg{scan=reflect.TypeFor[any]()}
		}
		if c.ScanType()!=scan{t.Fatal("scan metadata",i,c.ScanType(),scan)}
		if actual,known:=c.Length();actual!=length||known!=lengthKnown{t.Fatal("length metadata",i,actual,known,length,lengthKnown)}
		n, known := c.Nullable()
		if pg && (n || known) { t.Fatal("pq nullability", i, n, known) }
		if !pg && view && (!known || !n) { t.Fatal("nullable view attribute", i, n, known) }
		if !pg && !view && (n || known) { t.Fatal("computed SRF nullability", i, n, known) }
		if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok { t.Fatal("unexpected decimal typmod", i, p, s, ok) }
	}
}

func backendMemoryConsume(t *testing.T, rows *sql.Rows, pg, view bool) []backendMemoryRow {
	t.Helper()
	defer rows.Close()
	backendMemoryMetadata(t, rows, pg, view)
	var result []backendMemoryRow
	for rows.Next() {
		var r backendMemoryRow
		var path pq.Int64Array
		if err := rows.Scan(&r.Name, &r.Ident, &r.Type, &r.Level, &path, &r.Total, &r.Blocks, &r.Free, &r.Chunks, &r.Used); err != nil { t.Fatal(err) }
		r.Path = append([]int64(nil), path...)
		result = append(result, r)
	}
	if err := rows.Err(); err != nil { t.Fatal(err) }
	backendMemoryTopology(t, result, pg)
	return result
}

func backendMemoryTopology(t *testing.T, rows []backendMemoryRow, pg bool) {
	t.Helper()
	paths := make(map[string]backendMemoryRow)
	ids := make(map[int64]bool)
	roots := 0
	for _, r := range rows {
		if r.Name == "" || r.Type == "" || r.Level < 1 || len(r.Path) != int(r.Level) || r.Path[0] != 1 { t.Fatal("invalid context identity", r) }
		id := r.Path[len(r.Path)-1]
		if id < 1 || ids[id] { t.Fatal("duplicate/nonpositive capture-local id", r) }
		ids[id] = true
		key := fmt.Sprint(r.Path)
		if _, exists := paths[key]; exists { t.Fatal("duplicate path", r) }
		paths[key] = r
		if r.Level == 1 { roots++; if r.Name != "TopMemoryContext" || !reflect.DeepEqual(r.Path, []int64{1}) { t.Fatal("root", r) } }
		if !r.Total.Valid || !r.Used.Valid || r.Total.Int64 < 0 || r.Used.Int64 < 0 { t.Fatal("missing/negative own bytes", r) }
		if pg {
			if !r.Blocks.Valid || !r.Free.Valid || !r.Chunks.Valid || r.Blocks.Int64 < 0 || r.Free.Int64 < 0 || r.Chunks.Int64 < 0 || r.Total.Int64 != r.Used.Int64+r.Free.Int64 { t.Fatal("PostgreSQL own allocator invariant", r) }
		} else {
			if r.Ident.Valid || r.Type != "GoObjectLayout" || r.Blocks.Valid || r.Free.Valid || r.Chunks.Valid || r.Total != r.Used { t.Fatal("truthful selected Go layout", r) }
		}
	}
	if roots != 1 { t.Fatal("one root required", roots, rows) }
	for _, r := range rows {
		if r.Level > 1 { if _, ok := paths[fmt.Sprint(r.Path[:len(r.Path)-1])]; !ok { t.Fatal("missing immediate parent", r) } }
	}
	if !pg {
		if len(rows) != 3 { t.Fatal("native ownership groups", rows) }
		for _, want := range []struct{name string; path []int64}{{"TopMemoryContext", []int64{1}}, {"PreparedStatements", []int64{1,2}}, {"PreparedDefinitions", []int64{1,2,3}}} {
			r, ok := paths[fmt.Sprint(want.path)]
			if !ok || r.Name != want.name { t.Fatal("native ownership path", want, rows) }
			if want.name != "PreparedDefinitions" && r.Total.Int64 <= 0 { t.Fatal("real owner has no layout", r) }
		}
	}
}

func backendMemoryRead(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string, prepared bool) []backendMemoryRow {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, prepErr := e.PrepareContext(p.Ctx, query)
		if prepErr != nil { t.Fatal(prepErr) }
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.Ctx)
	} else { rows, err = e.QueryContext(p.Ctx, query) }
	if err != nil { t.Fatal(query, err) }
	return backendMemoryConsume(t, rows, b.PG, strings.Contains(query, "FROM pg_catalog.pg_backend_memory_contexts"))
}

func backendMemoryHeld(t *testing.T, p *fixture.Pair, e fixture.Executor, query string) fixture.HeldStatement {
	t.Helper()
	stmt, err := e.PrepareContext(p.Ctx, query)
	if err != nil { t.Fatal("hold", query, err) }
	t.Cleanup(func(){ if err := stmt.Close(); err != nil { t.Error(err) } })
	return fixture.HeldStatement{Executor:e, Statement:stmt, SQL:query}
}

func backendMemoryOwn(rows []backendMemoryRow) map[string]int64 {
	result := make(map[string]int64)
	for _, r := range rows { result[r.Name] = r.Total.Int64 }
	return result
}

func backendMemorySameOwn(t *testing.T, got, want []backendMemoryRow) {
	t.Helper()
	if !reflect.DeepEqual(got, want) { t.Fatal("selected owners changed", got, want) }
}

func backendMemoryDelta(t *testing.T, before, after []backendMemoryRow, delta int64) int64 {
	t.Helper()
	a, z := backendMemoryOwn(before), backendMemoryOwn(after)
	if a["TopMemoryContext"] != z["TopMemoryContext"] || a["PreparedStatements"] != z["PreparedStatements"] { t.Fatal("child allocation charged to parent own bytes", a, z) }
	actual := z["PreparedDefinitions"]-a["PreparedDefinitions"]
	if actual <= 0 || (delta != 0 && actual != delta) { t.Fatal("one real uniform definition-layout charge", actual, delta) }
	return actual
}

func backendMemorySentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	fixture.RunStep(t,p.Ctx,e,b.Recorder,fixture.Select("SELECT n FROM backend_memory_sentinel",[]string{"n"},[]string{"INT4"},[][]any{{int64(42)}}))
}

func backendMemorySetupSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	pgSequenceExec(t,p,b,e,"CREATE TABLE backend_memory_sentinel(n integer)")
	fixture.RunStep(t,p.Ctx,e,b.Recorder,fixture.Exec("INSERT INTO backend_memory_sentinel VALUES(42)",1))
}

func TestReferenceBackendMemoryRows(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name,func(t *testing.T){
		c := fixture.SequenceCoreConn(t,p.Ctx,b.DB)
		for _, query := range []string{backendMemoryView, backendMemoryFunction, "SELECT * FROM pg_catalog.pg_get_backend_memory_contexts() AS m"} {
			for _, prepared := range []bool{false,true} { backendMemoryRead(t,p,b,c,query,prepared) }
			held := backendMemoryHeld(t,p,c,query)
			backendMemoryRead(t,p,b,held,query,false)
			backendMemoryRead(t,p,b,held,query,false)
		}
		// Expansion evaluates a record SRF, not a metadata-only pg_proc row.
		// PostgreSQL can evaluate members separately: do not compare transient
		// paths/counters from different member evaluations as one capture.
		q := "SELECT (pg_catalog.pg_get_backend_memory_contexts()).*"
		for _, prepared := range []bool{false,true} {
			var rows *sql.Rows
			var err error
			if prepared { s, e := c.PrepareContext(p.Ctx,q); if e != nil { t.Fatal(e) }; defer s.Close(); rows,err=s.QueryContext(p.Ctx) } else { rows,err=c.QueryContext(p.Ctx,q) }
			if err != nil { t.Fatal(err) }
			backendMemoryMetadata(t,rows,b.PG,false)
			count:=0
			for rows.Next(){ var name,typ string; var ident sql.NullString; var level int64; var path pq.Int64Array; var counters [5]sql.NullInt64
				if err:=rows.Scan(&name,&ident,&typ,&level,&path,&counters[0],&counters[1],&counters[2],&counters[3],&counters[4]);err!=nil{t.Fatal(err)}
				if name=="" || typ=="" || level<1 || len(path)<1 || path[0]!=1{t.Fatal("expanded record members",name,typ,level,path)};count++ }
			if rows.Err()!=nil || count==0 {t.Fatal("expanded SRF rows",count,rows.Err())};rows.Close()
		}
		q = "SELECT pg_catalog.pg_get_backend_memory_contexts() AS context"
		for _, prepared := range []bool{false,true} {
			var rows *sql.Rows; var err error
			if prepared {s,e:=c.PrepareContext(p.Ctx,q);if e!=nil{t.Fatal(e)};defer s.Close();rows,err=s.QueryContext(p.Ctx)}else{rows,err=c.QueryContext(p.Ctx,q)}
			if err!=nil{t.Fatal(err)}
			cols,e:=rows.ColumnTypes();if e!=nil||len(cols)!=1||cols[0].Name()!="context"||cols[0].DatabaseTypeName()!="RECORD"{t.Fatal("record metadata",cols,e)}
			var captured []backendMemoryRow
			for rows.Next(){
				var value string
				if err:=rows.Scan(&value);err!=nil{t.Fatal(err)}
				captured=append(captured,backendMemoryRecord(t,value))
			}
			if rows.Err()!=nil{t.Fatal(rows.Err())};rows.Close()
			backendMemoryTopology(t,captured,b.PG)
		}
		// Scalar member selection is another genuine target-list SRF consumer.
		q="SELECT (pg_catalog.pg_get_backend_memory_contexts()).level AS depth"
		rows,err:=c.QueryContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer rows.Close()
		count:=0;for rows.Next(){var depth int64;if err:=rows.Scan(&depth);err!=nil||depth<1{t.Fatal(depth,err)};count++};if count==0||rows.Err()!=nil{t.Fatal(strconv.Itoa(count),rows.Err())}
	}) }
}

func backendMemoryRecord(t *testing.T,value string) backendMemoryRow {
	t.Helper()
	if !strings.HasPrefix(value,"(")||!strings.HasSuffix(value,")"){t.Fatal("record carrier",value)}
	fields,err:=csv.NewReader(strings.NewReader(value[1:len(value)-1])).Read()
	if err!=nil||len(fields)!=10{t.Fatal("ten record fields",value,fields,err)}
	level,err:=strconv.ParseInt(fields[3],10,64);if err!=nil{t.Fatal(err)}
	var path pq.Int64Array
	if err:=path.Scan(fields[4]);err!=nil{t.Fatal(err)}
	r:=backendMemoryRow{Name:fields[0],Ident:sql.NullString{String:fields[1],Valid:fields[1]!=""},Type:fields[2],Level:level,Path:append([]int64(nil),path...)}
	for i,dst:=range []*sql.NullInt64{&r.Total,&r.Blocks,&r.Free,&r.Chunks,&r.Used}{
		if fields[i+5]!=""{n,err:=strconv.ParseInt(fields[i+5],10,64);if err!=nil{t.Fatal(err)};*dst=sql.NullInt64{Int64:n,Valid:true}}
	}
	return r
}
