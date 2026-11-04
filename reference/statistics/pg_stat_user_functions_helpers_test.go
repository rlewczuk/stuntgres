package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const userFunctionSQL = "SELECT * FROM pg_catalog.pg_stat_user_functions WHERE funcname=$1"

func userFunctionGet(t *testing.T,p *fixture.Pair,c fixture.Executor,name string) xactFunctionRow {
	t.Helper(); rows,err:=c.QueryContext(p.Ctx,userFunctionSQL,name);if err!=nil{t.Fatal(name,err)};defer rows.Close()
	if !rows.Next(){t.Fatal("missing published function",name,rows.Err())};var r xactFunctionRow;if err:=rows.Scan(r.destinations()...);err!=nil{t.Fatal(err)}
	if rows.Next()||rows.Err()!=nil{t.Fatal("duplicate function identity",name,rows.Err())};return r
}
func userFunctionAbsent(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor,name string) {
	t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM pg_catalog.pg_stat_user_functions WHERE funcname=$1",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}},name))
}

// Physical retirement can precede PostgreSQL's asynchronous final report. A
// separate none-mode observer awaits real publication, never manufactures it.
func userFunctionAwait(t *testing.T,p *fixture.Pair,b fixture.Backend,name string,want int64) {
	t.Helper(); probe,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer probe.Close()
	fixture.StatisticsExec(t,p,b,probe,"SET stats_fetch_consistency=none",0)
	ctx,cancel:=context.WithTimeout(p.Ctx,fixture.Liveness);defer cancel();tick:=time.NewTicker(5*time.Millisecond);defer tick.Stop()
	var last sql.NullInt64
	for {
		err=probe.QueryRowContext(ctx,"SELECT calls FROM pg_catalog.pg_stat_user_functions WHERE funcname=$1",name).Scan(&last)
		if err!=nil&&err!=sql.ErrNoRows{t.Fatal("publication probe",name,last,err)}
		if err==nil&&last.Valid&&last.Int64==want{return}
		select{case <-ctx.Done():t.Fatal("publication deadline",name,last,want,ctx.Err());case <-tick.C:}
	}
}
func userFunctionPublish(t *testing.T,p *fixture.Pair,b fixture.Backend,name string,calls int,want int64) {
	t.Helper();w,retire:=fixture.StatisticsReportingConn(t,p,b);xactFunctionBegin(t,p,b,w)
	for range calls{xactFunctionCall(t,p,b,w,name,7)}
	fixture.StatisticsExec(t,p,b,w,"COMMIT",0);retire();userFunctionAwait(t,p,b,name,want)
}

// Shared membership is prepared before body execution; completed counts and
// timings remain backend-local until an actual reporting boundary.
func userFunctionUnpublished(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor,name string) {
	t.Helper();r:=userFunctionGet(t,p,c,name);xactFunctionCounts(t,b,r,0,0,0)
	if r.Total!=0||r.Self!=0{t.Fatal("unpublished invocation leaked timing",r)}
}

// The first native terminal command must own its first unthrottled report.
// A fresh recording pool has no Ping or setup commands before the caller's BEGIN.
func userFunctionFreshReporter(t *testing.T,p *fixture.Pair,b fixture.Backend)(*sql.Conn,func()) {
	t.Helper()
	if !b.PG{b.DB,b.Recorder=fixture.RecordedNativePool(t,p.NativeConnector)}
	return fixture.StatisticsReportingConn(t,p,b)
}
