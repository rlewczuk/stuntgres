package reference_test

import (
 "bytes"
 "context"
 "errors"
 "io"
 "strings"
 "sync"
 "stuntgres"
 "stuntgres/reference/fixture"
)

type attributeStatsFailedReader struct{err error}
func(r attributeStatsFailedReader) Read([]byte)(int,error){return 0,r.err}

func attributeStatsNativeLifetimes(s attributeStatsScenario){
 s.t.Helper()
 s.exec("CREATE TEMP TABLE ast_temp(a INT,arr INT[])")
 tempRestore:=func(column,extra string){q:=attributeStatsRestore(column,false,extra);q=strings.Replace(q,"'public'","'pg_temp'",1);q=strings.Replace(q,"'ast_t'","'ast_temp'",1);s.run(fixture.Select(q,[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))}
 tempRestore("a","'null_frac',.375::real,'n_distinct',7::real,'most_common_vals','{1,2}','most_common_freqs','{0.5,0.25}'::real[]")
 tempRestore("arr",`'most_common_vals','{"{1,2}","{3}"}','most_common_freqs','{0.5,0.25}'::real[]`)
 tempRead:=fixture.Select("SELECT attname,null_frac,n_distinct,most_common_vals::text FROM pg_stats WHERE schemaname=(SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()) AND tablename='ast_temp' ORDER BY attname",[]string{"attname","null_frac","n_distinct","most_common_vals"},[]string{"NAME","FLOAT4","FLOAT4","TEXT"},[][]any{{"a",float64(.375),float64(7),"{1,2}"},{"arr",float64(0),float64(0),`{"{1,2}","{3}"}`}})
 s.run(tempRead)
 var baseline bytes.Buffer;if err:=stuntgres.Dump(s.p.Ctx,s.p.Native,&baseline);err!=nil{s.t.Fatal(err)}
 if strings.Contains(baseline.String(),"ast_temp"){s.t.Fatal("session TEMP statistics escaped persistent dump")}
 // The capture is complete before the first writer callback. DML can finish
 // while external I/O is blocked, without changing the frozen dump.
 writer:=&fixture.CatalogControlGatedWriter{Started:make(chan struct{}),Release:make(chan struct{})}
 dumped:=make(chan error,1);go func(){dumped<-stuntgres.Dump(s.p.Ctx,s.p.Native,writer)}()
 var releaseDump sync.Once;dumpJoined:=false
 defer func(){releaseDump.Do(func(){close(writer.Release)});if !dumpJoined{<-dumped}}()
 select{case <-writer.Started:case <-s.p.Ctx.Done():s.t.Fatal(s.p.Ctx.Err())}
 s.run(fixture.Exec("INSERT INTO ast_t VALUES(100,10,'{10}','[100,101)')",1))
 s.run(tempRead)
 releaseDump.Do(func(){close(writer.Release)});err:=<-dumped;dumpJoined=true;if err!=nil{s.t.Fatal(err)}
 if !bytes.Equal(baseline.Bytes(),writer.Output.Bytes()){s.t.Fatal("external writer observed mutable statistics/catalog capture")}
 // Cancellation after reader entry must preserve the actual destination,
 // including writes completed while the input callback was blocked.
 reader:=&fixture.CatalogControlGatedReader{Started:make(chan struct{}),Release:make(chan struct{})}
 restoreCtx,cancel:=context.WithCancel(s.p.Ctx);defer cancel()
 restored:=make(chan error,1);go func(){restored<-stuntgres.Restore(restoreCtx,s.p.Native,io.MultiReader(reader,bytes.NewReader(baseline.Bytes())))}()
 var releaseRestore sync.Once;restoreJoined:=false
 defer func(){cancel();releaseRestore.Do(func(){close(reader.Release)});if !restoreJoined{<-restored}}()
 select{case <-reader.Started:case <-s.p.Ctx.Done():s.t.Fatal(s.p.Ctx.Err())}
 s.run(fixture.Exec("INSERT INTO ast_t VALUES(101,11,'{11}','[101,102)')",1))
 s.run(tempRead)
 cancel();releaseRestore.Do(func(){close(reader.Release)});err=<-restored;restoreJoined=true
 if !errors.Is(err,context.Canceled){s.t.Fatal("gated restore cancellation",err)}
 s.run(fixture.Select("SELECT count(*) FROM ast_t",[]string{"count"},[]string{"INT8"},[][]any{{int64(3)}}))
 inputFailure:=errors.New("attribute statistics input failure")
 if err=stuntgres.Restore(s.p.Ctx,s.p.Native,io.MultiReader(bytes.NewReader(baseline.Bytes()),attributeStatsFailedReader{inputFailure}));!errors.Is(err,inputFailure){s.t.Fatal("input I/O failure lost",err)}
 s.run(fixture.Select("SELECT count(*) FROM ast_t",[]string{"count"},[]string{"INT8"},[][]any{{int64(3)}}))
 s.run(tempRead)
 // A genuine typed ANYARRAY cursor is retained without iteration, then read
 // after both catalog replacements. Metadata and frozen values must survive.
 old,err:=s.p.Native.QueryContext(s.p.Ctx,"SELECT most_common_vals FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND attname='a' AND NOT inherited");if err!=nil{s.t.Fatal(err)};defer old.Close()
 metadata,err:=old.ColumnTypes();if err!=nil||len(metadata)!=1||metadata[0].Name()!="most_common_vals"||metadata[0].DatabaseTypeName()!="ANYARRAY"{s.t.Fatal(metadata,err)}
 if err=stuntgres.Reset(s.p.Ctx,s.p.Native);err!=nil{s.t.Fatal(err)}
 s.run(tempRead)
 if err=stuntgres.Restore(s.p.Ctx,s.p.Native,bytes.NewReader(baseline.Bytes()));err!=nil{s.t.Fatal(err)}
 s.run(tempRead)
 if !old.Next(){s.t.Fatal("retained statistics cursor lost",old.Err())};var value string
 if err=old.Scan(&value);err!=nil||value!="{1,2}"{s.t.Fatal(value,err)}
 if old.Next()||old.Err()!=nil{s.t.Fatal("retained cursor cardinality",old.Err())}
 if metadata[0].DatabaseTypeName()!="ANYARRAY"{s.t.Fatal("retained metadata changed")}
 s.exec("DROP TABLE ast_temp")
}
