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

func relationStatsNativeLifetimes(s relationStatsScenario,saved []byte){
 s.t.Helper()
 s.exec("CREATE TEMP TABLE rst_temp(a int)")
 s.run(fixture.Select("SELECT pg_restore_relation_stats('schemaname','pg_temp','relname','rst_temp','relpages',51,'reltuples',52.5::real,'relallvisible',53,'relallfrozen',54)",[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{true}}))
 tempRead:=fixture.Select("SELECT relpages,reltuples,relallvisible,relallfrozen FROM pg_class WHERE oid='pg_temp.rst_temp'::regclass",[]string{"relpages","reltuples","relallvisible","relallfrozen"},[]string{"INT4","FLOAT4","INT4","INT4"},[][]any{{int64(51),float64(52.5),int64(53),int64(54)}})
 s.run(tempRead)
 var baseline bytes.Buffer;if err:=stuntgres.Dump(s.p.Ctx,s.p.Native,&baseline);err!=nil{s.t.Fatal(err)}
 if strings.Contains(baseline.String(),"rst_temp"){s.t.Fatal("TEMP metadata escaped dump")}
 writer:=&fixture.CatalogControlGatedWriter{Started:make(chan struct{}),Release:make(chan struct{})}
 dumped:=make(chan error,1);go func(){dumped<-stuntgres.Dump(s.p.Ctx,s.p.Native,writer)}()
 var release sync.Once;joined:=false
 defer func(){release.Do(func(){close(writer.Release)});if !joined{<-dumped}}()
 select{case <-writer.Started:case <-s.p.Ctx.Done():s.t.Fatal(s.p.Ctx.Err())}
 s.restore("'reltuples',61.5::real",true)
 release.Do(func(){close(writer.Release)});err:=<-dumped;joined=true;if err!=nil{s.t.Fatal(err)}
 if !bytes.Equal(baseline.Bytes(),writer.Output.Bytes()){s.t.Fatal("external I/O changed frozen snapshot")}
 reader:=&fixture.CatalogControlGatedReader{Started:make(chan struct{}),Release:make(chan struct{})}
 ctx,cancel:=context.WithCancel(s.p.Ctx);defer cancel()
 restored:=make(chan error,1);go func(){restored<-stuntgres.Restore(ctx,s.p.Native,io.MultiReader(reader,bytes.NewReader(saved)))}()
 var releaseRead sync.Once;restoreJoined:=false
 defer func(){cancel();releaseRead.Do(func(){close(reader.Release)});if !restoreJoined{<-restored}}()
 select{case <-reader.Started:case <-s.p.Ctx.Done():s.t.Fatal(s.p.Ctx.Err())}
 s.restore("'reltuples',62.5::real",true)
 cancel();releaseRead.Do(func(){close(reader.Release)});err=<-restored;restoreJoined=true
 if !errors.Is(err,context.Canceled){s.t.Fatal(err)}
 s.stats(-17,62.5,-24,-27);s.run(tempRead)
 failure:=errors.New("relation statistics input failure")
 if err=stuntgres.Restore(s.p.Ctx,s.p.Native,io.MultiReader(bytes.NewReader(saved),attributeStatsFailedReader{failure}));!errors.Is(err,failure){s.t.Fatal(err)}
 s.stats(-17,62.5,-24,-27)
 // Native captured catalog rows retain their exact typed cells after replacement.
 old,err:=s.p.Native.QueryContext(s.p.Ctx,relationStatsRead("rst_t"));if err!=nil{s.t.Fatal(err)};defer old.Close()
 types,err:=old.ColumnTypes();if err!=nil||len(types)!=4{s.t.Fatal(types,err)}
 for i,want:=range []string{"INT4","FLOAT4","INT4","INT4"}{if types[i].DatabaseTypeName()!=want{s.t.Fatal(types[i])}}
 if err=stuntgres.Reset(s.p.Ctx,s.p.Native);err!=nil{s.t.Fatal(err)}
 s.run(tempRead)
 s.run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='rst_t' AND relnamespace='public'::regnamespace",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
 if err=stuntgres.Restore(s.p.Ctx,s.p.Native,bytes.NewReader(saved));err!=nil{s.t.Fatal(err)}
 s.run(tempRead);s.stats(-17,21.5,-24,-27)
 if !old.Next(){s.t.Fatal(old.Err())};var p,v,f int64;var n float64
 if err=old.Scan(&p,&n,&v,&f);err!=nil||p != -17||n!=62.5||v != -24||f != -27{s.t.Fatal(p,n,v,f,err)}
 if old.Next()||old.Err()!=nil{s.t.Fatal("retained result changed",old.Err())}
 s.exec("DROP TABLE rst_temp")
}
