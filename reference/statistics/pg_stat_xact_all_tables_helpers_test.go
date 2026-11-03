package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var xactTableNames = strings.Fields("relid schemaname relname seq_scan seq_tup_read idx_scan idx_tup_fetch n_tup_ins n_tup_upd n_tup_del n_tup_hot_upd n_tup_newpage_upd")
var xactTableTypes = strings.Fields("OID NAME NAME INT8 INT8 INT8 INT8 INT8 INT8 INT8 INT8 INT8")
const xactTableSelect = "SELECT * FROM pg_catalog.pg_stat_xact_all_tables WHERE relname=$1"

type xactTableRow struct {
	ID int64
	Schema, Name string
	Seq, Read int64
	Index, Fetch sql.NullInt64
	Inserts, Updates, Deletes, HOT, Newpage int64
}
func (r *xactTableRow) destinations() []any {
	return []any{&r.ID,&r.Schema,&r.Name,&r.Seq,&r.Read,&r.Index,&r.Fetch,&r.Inserts,&r.Updates,&r.Deletes,&r.HOT,&r.Newpage}
}
func xactTableGet(t *testing.T,p *fixture.Pair,c *sql.Conn,name string) xactTableRow {
	t.Helper();var r xactTableRow
	if err:=c.QueryRowContext(p.Ctx,xactTableSelect,name).Scan(r.destinations()...);err!=nil{t.Fatal(name,err)}
	return r
}
func xactTableCounts(t *testing.T,r xactTableRow,i,u,d int64) {
	t.Helper();if r.Inserts!=i||r.Updates!=u||r.Deletes!=d{t.Fatal("local executed tuple events",r,i,u,d)}
	if r.HOT<0||r.HOT>r.Updates||r.Newpage<0||r.Newpage>r.Updates{t.Fatal("invalid physical update counts",r)}
}
func xactTableIndex(t *testing.T,r xactTableRow,scans,fetches int64) {
	t.Helper();if !r.Index.Valid||!r.Fetch.Valid||r.Index.Int64!=scans||r.Fetch.Int64!=fetches{t.Fatal("local attached-index sum",r,scans,fetches)}
}
func xactTableMetadata(t *testing.T,columns []*sql.ColumnType,pg bool) {
	t.Helper();if len(columns)!=12{t.Fatal("xact column count",len(columns))}
	for i,c:=range columns {
		if c.Name()!=xactTableNames[i]||c.DatabaseTypeName()!=xactTableTypes[i]{t.Fatal("xact typed metadata",i,c.Name(),c.DatabaseTypeName())}
		nullable,known:=c.Nullable();if pg{if nullable||known{t.Fatal("pq nullability",i)}}else if !nullable||!known{t.Fatal("native view nullability",i)}
		if _,ok:=c.Length();ok{t.Fatal("unexpected length",i)};if _,_,ok:=c.DecimalSize();ok{t.Fatal("unexpected precision",i)}
		want:=reflect.TypeFor[int64]();if i==1||i==2{want=reflect.TypeFor[string]()};if pg&&i<3{want=reflect.TypeFor[any]()}
		if c.ScanType()!=want{t.Fatal("xact ScanType",i,c.ScanType(),want)}
	}
}
func xactTableEnd(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,end string) {
	t.Helper()
	if b.PG {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT pg_stat_force_next_flush()",[]string{"pg_stat_force_next_flush"},[]string{"VOID"},[][]any{{""}}))}
	fixture.StatisticsExec(t,p,b,c,end,0)
}
func xactTableDiagnostic(op string) fixture.Diagnostic {
	d:=tableStatDiagnostic(op);d.Message=strings.ReplaceAll(d.Message,"pg_stat_all_tables","pg_stat_xact_all_tables");return d
}
