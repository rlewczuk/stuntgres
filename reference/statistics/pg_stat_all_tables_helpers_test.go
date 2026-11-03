package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

var tableStatNames = strings.Fields("relid schemaname relname seq_scan last_seq_scan seq_tup_read idx_scan last_idx_scan idx_tup_fetch n_tup_ins n_tup_upd n_tup_del n_tup_hot_upd n_tup_newpage_upd n_live_tup n_dead_tup n_mod_since_analyze n_ins_since_vacuum last_vacuum last_autovacuum last_analyze last_autoanalyze vacuum_count autovacuum_count analyze_count autoanalyze_count total_vacuum_time total_autovacuum_time total_analyze_time total_autoanalyze_time")
var tableStatTypes = strings.Fields("OID NAME NAME INT8 TIMESTAMPTZ INT8 INT8 TIMESTAMPTZ INT8 INT8 INT8 INT8 INT8 INT8 INT8 INT8 INT8 INT8 TIMESTAMPTZ TIMESTAMPTZ TIMESTAMPTZ TIMESTAMPTZ INT8 INT8 INT8 INT8 FLOAT8 FLOAT8 FLOAT8 FLOAT8")
const tableStatSelect = "SELECT * FROM pg_catalog.pg_stat_all_tables WHERE relname=$1"

type tableStatRow struct {
	ID int64
	Schema, Name string
	Seq int64
	LastSeq sql.NullTime
	Read int64
	Index sql.NullInt64
	LastIndex sql.NullTime
	Fetch sql.NullInt64
	Inserts, Updates, Deletes, HOT, Newpage, Live, Dead, Mods, Since int64
	LastVacuum, LastAutoVacuum, LastAnalyze, LastAutoAnalyze sql.NullTime
	Vacuum, AutoVacuum, Analyze, AutoAnalyze int64
	VacuumTime, AutoVacuumTime, AnalyzeTime, AutoAnalyzeTime float64
}

func (r *tableStatRow) destinations() []any {
	return []any{&r.ID,&r.Schema,&r.Name,&r.Seq,&r.LastSeq,&r.Read,&r.Index,&r.LastIndex,&r.Fetch,&r.Inserts,&r.Updates,&r.Deletes,&r.HOT,&r.Newpage,&r.Live,&r.Dead,&r.Mods,&r.Since,&r.LastVacuum,&r.LastAutoVacuum,&r.LastAnalyze,&r.LastAutoAnalyze,&r.Vacuum,&r.AutoVacuum,&r.Analyze,&r.AutoAnalyze,&r.VacuumTime,&r.AutoVacuumTime,&r.AnalyzeTime,&r.AutoAnalyzeTime}
}
func tableStatRead(ctx context.Context,c *sql.Conn,name string)(tableStatRow,error){var r tableStatRow;err:=c.QueryRowContext(ctx,tableStatSelect,name).Scan(r.destinations()...);return r,err}
func tableStatGet(t *testing.T,p *fixture.Pair,c *sql.Conn,name string)tableStatRow{t.Helper();r,err:=tableStatRead(p.Ctx,c,name);if err!=nil{t.Fatal(name,err)};return r}
func (r tableStatRow) equal(other tableStatRow)bool{
	a,b:=r,other
	for _,pair:=range [][2]*sql.NullTime{{&a.LastSeq,&b.LastSeq},{&a.LastIndex,&b.LastIndex},{&a.LastVacuum,&b.LastVacuum},{&a.LastAutoVacuum,&b.LastAutoVacuum},{&a.LastAnalyze,&b.LastAnalyze},{&a.LastAutoAnalyze,&b.LastAutoAnalyze}}{
		if pair[0].Valid!=pair[1].Valid||pair[0].Valid&&!pair[0].Time.Equal(pair[1].Time){return false};*pair[0]=sql.NullTime{};*pair[1]=sql.NullTime{}
	}
	return a==b
}
func tableStatAwait(t *testing.T,p *fixture.Pair,b fixture.Backend,name,label string,accept func(tableStatRow)bool)tableStatRow{
	t.Helper();probe:=fixture.IdentityConnection(t,p.Ctx,b.DB)
	fixture.StatisticsExec(t,p,b,probe,"SET stats_fetch_consistency=none",0)
	ctx,cancel:=context.WithTimeout(p.Ctx,fixture.Liveness);defer cancel()
	tick:=time.NewTicker(time.Millisecond);defer tick.Stop()
	for{r,err:=tableStatRead(ctx,probe,name);if err!=nil{t.Fatal("ALL publication",name,err)};if accept(r){return r};select{case<-ctx.Done():t.Fatal("ALL publication expired",name,label,r,ctx.Err());case<-tick.C:}}
}
func tableStatCreate(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,name string){t.Helper();fixture.StatisticsExec(t,p,b,c,"CREATE TABLE "+name+"(n integer,payload text)",0);fixture.StatisticsExec(t,p,b,c,"ALTER TABLE "+name+" SET(autovacuum_enabled=false)",0)}
func tableStatSeed(t *testing.T,p *fixture.Pair,b fixture.Backend,name string){
	t.Helper();w,retire:=fixture.StatisticsReportingConn(t,p,b)
	fixture.StatisticsExec(t,p,b,w,"INSERT INTO "+name+" VALUES(1,'a'),(2,NULL),(3,'c')",3);retire()
	tableStatAwait(t,p,b,name,"published inserts3",func(r tableStatRow)bool{return r.Inserts==3})
}
func tableStatSeqPrefer(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){t.Helper();for _,q:=range []string{"SET enable_seqscan=on","SET enable_indexscan=off","SET enable_bitmapscan=off","SET enable_indexonlyscan=off"}{fixture.StatisticsExec(t,p,b,c,q,0)}}
func tableStatSeqPlan(t *testing.T,b fixture.Backend,method string){t.Helper();want:="Access Method: Full Scan";if b.PG{want="Seq Scan"};if !strings.Contains(method,want){t.Fatal("actual sequential access missing",method)}}
func tableStatAutomatic(t *testing.T,r tableStatRow){t.Helper();if r.AutoVacuum!=0||r.AutoAnalyze!=0||r.LastAutoVacuum.Valid||r.LastAutoAnalyze.Valid||r.AutoVacuumTime!=0||r.AutoAnalyzeTime!=0{t.Fatal("invented automatic maintenance",r)};for _,v:=range []float64{r.VacuumTime,r.AutoVacuumTime,r.AnalyzeTime,r.AutoAnalyzeTime}{if v<0||math.IsNaN(v)||math.IsInf(v,0){t.Fatal("invalid elapsed milliseconds",v)}}}
func tableStatIndexWant(t *testing.T,r tableStatRow,scans,fetches int64){t.Helper();if !r.Index.Valid||!r.Fetch.Valid||r.Index.Int64!=scans||r.Fetch.Int64!=fetches||(scans>0)!=r.LastIndex.Valid{t.Fatal("attached-index aggregate",r,scans,fetches)}}
func tableStatMetadata(t *testing.T,columns []*sql.ColumnType,pg bool){
	t.Helper();if len(columns)!=30{t.Fatal("ALL column count",len(columns))}
	for i,c:=range columns{
		if c.Name()!=tableStatNames[i]||c.DatabaseTypeName()!=tableStatTypes[i]{t.Fatal("ALL typed metadata",i,c.Name(),c.DatabaseTypeName())}
		nullable,known:=c.Nullable();if pg{if nullable||known{t.Fatal("pq invented nullability",i)}}else if !nullable||!known{t.Fatal("native nullable view metadata",i)}
		if _,ok:=c.Length();ok{t.Fatal("unexpected length",i)};if _,_,ok:=c.DecimalSize();ok{t.Fatal("unexpected precision",i)}
		want:=reflect.TypeFor[int64]()
		switch tableStatTypes[i]{case "OID":if pg{want=reflect.TypeFor[any]()};case "NAME":want=reflect.TypeFor[string]();if pg{want=reflect.TypeFor[any]()};case "TIMESTAMPTZ":want=reflect.TypeFor[time.Time]();case "FLOAT8":want=reflect.TypeFor[float64]()}
		if c.ScanType()!=want{t.Fatal("ALL adapter ScanType",i,c.ScanType(),want)}
	}
}
func tableStatDiagnostic(op string)fixture.Diagnostic{
	verb:=map[string]string{"INSERT":"insert into","UPDATE":"update","DELETE":"delete from"}[op]
	action:=map[string]string{"INSERT":"inserting into","UPDATE":"updating","DELETE":"deleting from"}[op]
	return fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"55000",Message:fmt.Sprintf("cannot %s view %q",verb,"pg_stat_all_tables"),Detail:"Views containing GROUP BY are not automatically updatable.",Hint:"To enable "+action+" the view, provide an INSTEAD OF "+op+" trigger or an unconditional ON "+op+" DO INSTEAD rule."}
}

// SQL index construction is itself a real sequential heap consumer. Establish
// its independently specified published totals before testing later access.
func tableStatBuild(t *testing.T,p *fixture.Pair,b fixture.Backend,name string,scans,tuples int64,commands ...string)tableStatRow {
	t.Helper()
	writer,retire:=fixture.StatisticsReportingConn(t,p,b)
	for _,q:=range commands { fixture.StatisticsExec(t,p,b,writer,q,0) }
	retire()
	return tableStatAwait(t,p,b,name,"published utility heap scans",func(r tableStatRow)bool{return r.Seq==scans&&r.Read==tuples})
}
