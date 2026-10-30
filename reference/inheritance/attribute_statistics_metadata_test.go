package reference_test

import (
 "database/sql"
 "stuntgres/reference/fixture"
)

func attributeStatsReadbackMetadata(s attributeStatsScenario){
 s.t.Helper()
 names:=[]string{"most_common_vals","histogram_bounds","most_common_elems","range_length_histogram","range_bounds_histogram"}
 base:="SELECT most_common_vals,histogram_bounds,most_common_elems,range_length_histogram,range_bounds_histogram FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND attname='a' AND NOT inherited"
 for _,empty:=range []bool{false,true}{
  q:=base;if empty{q+=" AND false"}
  var rows *sql.Rows;var err error
  if s.prepared{stmt,e:=s.conn.PrepareContext(s.p.Ctx,q);if e!=nil{s.t.Fatal(e)};defer stmt.Close();rows,err=stmt.QueryContext(s.p.Ctx)}else{rows,err=s.conn.QueryContext(s.p.Ctx,q)}
  if err!=nil{s.t.Fatal(err)}
  metadata,err:=rows.ColumnTypes();if err!=nil||len(metadata)!=len(names){rows.Close();s.t.Fatal(metadata,err)}
  for i,column:=range metadata{if column.Name()!=names[i]||column.DatabaseTypeName()!="ANYARRAY"{rows.Close();s.t.Fatalf("pg_stats pre-iteration metadata %d: %q/%q",i,column.Name(),column.DatabaseTypeName())}}
  if empty{if rows.Next()||rows.Err()!=nil{rows.Close();s.t.Fatal("empty pg_stats source returned a row",rows.Err())};rows.Close();continue}
  if !rows.Next(){rows.Close();s.t.Fatal("missing pg_stats row",rows.Err())}
  var values [5]sql.NullString
  if err=rows.Scan(&values[0],&values[1],&values[2],&values[3],&values[4]);err!=nil{rows.Close();s.t.Fatal(err)}
  if values[0]!=(sql.NullString{String:"{1,2}",Valid:true})||values[1]!=(sql.NullString{String:"[0:2]={3,5,9}",Valid:true})||values[2].Valid||values[3].Valid||values[4].Valid{rows.Close();s.t.Fatal("typed ANYARRAY readback",values)}
  if rows.Next()||rows.Err()!=nil{rows.Close();s.t.Fatal("pg_stats cardinality",rows.Err())};rows.Close()
 }
 s.run(fixture.Select("SELECT null_frac,avg_width,n_distinct FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND false",[]string{"null_frac","avg_width","n_distinct"},[]string{"FLOAT4","INT4","FLOAT4"},nil))
}
