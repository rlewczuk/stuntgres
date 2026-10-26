package reference_test

import (
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var shmemNames = []string{"pg_shmem_allocations", "pg_shmem_allocations_numa"}
var shmemColumns = [][]string{{"name", "off", "size", "allocated_size"}, {"name", "numa_node", "size"}}
var shmemTypes = [][]string{{"TEXT", "INT8", "INT8", "INT8"}, {"TEXT", "INT4", "INT8"}}
var shmemNative = [][][]any{
	{{"stuntgres simulated arena", int64(4096), int64(65537), int64(65600)}, {"<anonymous>", nil, int64(4096), int64(4096)}, {nil, int64(69696), int64(978880), int64(978880)}},
	{{"stuntgres simulated arena", int64(0), int64(69632)}, {"stuntgres simulated arena", nil, int64(0)}},
}

func shmemMetadata(t *testing.T, rows *sql.Rows, pg bool, index int) {
	t.Helper()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, shmemColumns[index]) { t.Fatalf("columns %v: %v", cols, err) }
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != len(cols) { t.Fatal(types, err) }
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i == 0 { scan = reflect.TypeFor[string]() }
		if pg && index == 1 && i == 1 { scan = reflect.TypeFor[int32]() }
		n, known := typ.Nullable()
		if typ.DatabaseTypeName() != shmemTypes[index][i] || typ.ScanType() != scan || known == pg || n != !pg { t.Fatalf("column %d: %s %v nullable=%v/%v", i, typ.DatabaseTypeName(), typ.ScanType(), n, known) }
	}
}

func shmemRead(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, index int, prepared, observe bool) {
	t.Helper()
	q := "SELECT * FROM pg_catalog." + shmemNames[index]
	if index == 1 && b.PG {
		var available bool
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT pg_catalog.pg_numa_available()").Scan(&available); err != nil { t.Fatal(err) }
		if !available {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "XX000", Message: "libnuma initialization failed or NUMA is not supported on this platform"})
			s.SelectRows = true
			if prepared { s = fixture.Prepared(s) }
			fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
			if observe { fmt.Fprintln(os.Stderr, "SHMEM postgres NUMA XX000: libnuma initialization failed or NUMA is not supported on this platform") }
			return
		}
	}
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, prepErr := e.PrepareContext(p.Ctx, q)
		if prepErr != nil { t.Fatal(prepErr) }
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.Ctx)
	} else { rows, err = e.QueryContext(p.Ctx, q) }
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	shmemMetadata(t, rows, b.PG, index)
	var got [][]any
	for rows.Next() {
		values := make([]any, len(shmemColumns[index])); dest := make([]any, len(values))
		for i := range values { dest[i] = &values[i] }
		if err := rows.Scan(dest...); err != nil { t.Fatal(err) }
		for i := range values { values[i] = fixture.Normalize(values[i]) }
		got = append(got, values)
	}
	if err := rows.Err(); err != nil { t.Fatal(err) }
	if !b.PG {
		if len(got) != len(shmemNative[index]) { t.Fatal("native cardinality", got) }
		for _, want := range shmemNative[index] {
			matches := 0; for _, row := range got { if reflect.DeepEqual(row, want) { matches++ } }
			if matches != 1 { t.Fatalf("want exactly one %v, got %v", want, got) }
		}
	} else if index == 0 {
		anonymous, free, named := 0, 0, 0
		for _, row := range got {
			size, ok := row[2].(int64); if !ok || size < 0 { t.Fatal(row) }
			allocated, ok := row[3].(int64); if !ok || allocated < size { t.Fatal(row) }
			switch row[0] {
			case nil: free++; if row[1] == nil || size != allocated { t.Fatal(row) }
			case "<anonymous>": anonymous++; if row[1] != nil || size != allocated { t.Fatal(row) }
			default: named++; if row[0] == "" || row[1] == nil { t.Fatal(row) }
			}
			if row[1] != nil && row[1].(int64) < 0 { t.Fatal(row) }
		}
		if anonymous != 1 || free != 1 || named == 0 { t.Fatal("actual allocation structure", got) }
	} else {
		groups := map[string]map[int64]bool{}; nulls := map[string]int{}
		allocated := map[string]int64{}
		allocationRows, err := b.DB.QueryContext(p.Ctx, "SELECT name,allocated_size FROM pg_catalog.pg_shmem_allocations WHERE name IS NOT NULL AND name <> '<anonymous>'")
		if err != nil { t.Fatal(err) }
		defer allocationRows.Close()
		for allocationRows.Next() {
			var name string
			var size int64
			if err := allocationRows.Scan(&name, &size); err != nil { t.Fatal(err) }
			allocated[name] = size
		}
		if err := allocationRows.Err(); err != nil { t.Fatal(err) }
		totals := map[string]int64{}
		for _, row := range got {
			name, ok := row[0].(string); if !ok || name == "<anonymous>" || name == "" { t.Fatal(row) }
			if row[2].(int64) < 0 { t.Fatal(row) }
			if _, exists := allocated[name]; !exists { t.Fatal("NUMA row lacks named allocation", row) }
			totals[name] += row[2].(int64)
			if groups[name] == nil { groups[name] = map[int64]bool{} }
			if row[1] == nil { nulls[name]++; continue }
			node := row[1].(int64); if node < 0 || groups[name][node] { t.Fatal(row) }; groups[name][node] = true
		}
		if len(groups) == 0 { t.Fatal("empty actual NUMA allocation bag") }
		for name, nodes := range groups { if nulls[name] != 1 || len(nodes) == 0 { t.Fatal(name, nodes, nulls) }; for n := int64(0); n < int64(len(nodes)); n++ { if !nodes[n] { t.Fatal(name, nodes) } } }
		if len(groups) != len(allocated) { t.Fatal("missing NUMA allocations", groups, allocated) }
		for name, bytes := range allocated {
			if totals[name] < bytes { t.Fatal("page coverage below allocated bytes", name, totals[name], bytes) }
		}
	}
	if observe { fmt.Fprintf(os.Stderr, "SHMEM %s %s rows=%v\n", b.Name, shmemNames[index], got) }
}

func TestReferencePGShmemAllocations(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) {
		for _, prepared := range []bool{false, true} { shmemRead(t, p, b, b.DB, 0, prepared, false) }
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT name FROM pg_shmem_allocations WHERE name=$1", []string{"name"}, []string{"TEXT"}, [][]any{{"<anonymous>"}}, "<anonymous>")
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		pgSequenceExec(t, p, b, b.DB, "CREATE TABLE shmem_work (n integer)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO shmem_work VALUES (7)", 1))
		shmemRead(t, p, b, b.DB, 0, false, false)
	}) }
}

func TestReferencePGShmemNUMA(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) { for _, prepared := range []bool{false, true} { shmemRead(t, p, b, b.DB, 1, prepared, false) } }) }
}

func TestReferencePGShmemShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) {
		shmemRoutineMetadata(t, p, b)
		shmemDefinitions(t, p, b)
		owner := "postgres"
		if b.PG { owner = "stuntgres" }
		for i, name := range shmemNames {
			for _, prefix := range []string{"", "pg_catalog."} {
				s := fixture.Select("SELECT * FROM "+prefix+name+" WHERE FALSE", shmemColumns[i], shmemTypes[i], [][]any{})
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { shmemMetadata(t, rows, b.PG, i) }
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s); fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
				projected := fixture.Select("SELECT size,name FROM "+prefix+name+" WHERE FALSE", []string{"size","name"}, []string{"INT8","TEXT"}, [][]any{})
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, projected)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(projected))
			}
			oid, rowtype, array := int64(12134), int64(12136), int64(12135); if i == 1 { oid, rowtype, array = 12138, 12140, 12139 }
			q := "SELECT c.oid::bigint,c.relkind::text,c.relnamespace::bigint,c.reltype::bigint,t.typrelid::bigint,t.typarray::bigint,a.typelem::bigint,r.rolname::text FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray JOIN pg_roles r ON r.oid=c.relowner WHERE c.relname=$1"
			s := fixture.Select(q, []string{"oid","relkind","relnamespace","reltype","typrelid","typarray","typelem","rolname"}, []string{"INT8","TEXT","INT8","INT8","INT8","INT8","INT8","TEXT"}, [][]any{{oid,"v",int64(11),rowtype,oid,array,rowtype,owner}}, name)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s); fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
			var attrs [][]any
			for j, col := range shmemColumns[i] { typ := int64(20); if j == 0 { typ = 25 }; if i == 1 && j == 1 { typ = 23 }; attrs = append(attrs, []any{int64(j+1), col, typ, false}) }
			s = fixture.Select("SELECT attnum::integer,attname::text,atttypid::bigint,attnotnull FROM pg_attribute WHERE attrelid=$1 AND attnum>0 ORDER BY attnum", []string{"attnum","attname","atttypid","attnotnull"}, []string{"INT4","TEXT","INT8","BOOL"}, attrs, oid)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
	}) }
}

func shmemRetained(t *testing.T, rows *sql.Rows, index int) {
	t.Helper()
	defer rows.Close()
	shmemMetadata(t, rows, false, index)
	var got [][]any
	for rows.Next() {
		row := make([]any, len(shmemColumns[index]))
		dest := make([]any, len(row))
		for i := range row { dest[i] = &row[i] }
		if err := rows.Scan(dest...); err != nil { t.Fatal(err) }
		got = append(got, row)
	}
	if rows.Err() != nil || len(got) != len(shmemNative[index]) { t.Fatal("retained cardinality", got, rows.Err()) }
	for _, want := range shmemNative[index] {
		count := 0
		for _, row := range got { if reflect.DeepEqual(row, want) { count++ } }
		if count != 1 { t.Fatal("retained values", got, want) }
	}
}
