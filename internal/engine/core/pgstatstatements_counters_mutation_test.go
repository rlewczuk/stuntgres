package core_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestPgStatStatementsCountersMutationImage(t *testing.T) {
	e := engine.New()
	ctx := context.Background()
	integer,text := enginetest.Usage(e,sqltype.Int4).Type,enginetest.Usage(e,sqltype.Text).Type
	one,err := sqltype.Integer(integer,1)
	if err != nil { t.Fatal(err) }
	full := []sqltype.Value{one,sqltype.String(text,"a")}
	null := []sqltype.Value{one,sqltype.Null(text)}
	for _, tc := range []struct{ old,next []sqltype.Value; want uint64 }{
		{nil,full,72},{nil,null,40},{full,null,112},{null,nil,40},{nil,nil,0},
	} {
		old,next := &storage.RowVersion{Values:tc.old},&storage.RowVersion{Values:tc.next}
		if tc.old == nil { old = nil }
		if tc.next == nil { next = nil }
		got,err := core.StatementMutationImage(ctx,old,next)
		if err != nil || got != tc.want { t.Fatal("immutable image shape",got,tc.want,err) }
	}
	if image,err := core.StatementMutationImage(ctx,nil,&storage.RowVersion{}); err != nil || image != 24 { t.Fatal("zero-column accepted image confused with absent row",image,err) }
	wide := make([]sqltype.Value,255)
	for i := range wide { wide[i] = sqltype.String(text,"payload") }
	image,err := core.StatementMutationImage(ctx,nil,&storage.RowVersion{Values:wide})
	if err != nil || image != 8216 { t.Fatal("wide image",image,err) }
	enginetest.Run(t,e,"CREATE TABLE counter_shape(k integer)")
	read := e.CatalogRead()
	relation := read.MustRelation(enginetest.CatalogRef(t,e,read,"counter_shape").Address)
	for _, tc := range []struct{ bytes uint64; pages int64 }{{0,0},{1,1},{8191,1},{8192,1},{8193,2},{8216,2},{16384,2},{16385,3}} {
		c := core.StatementMutationCounters(read,relation,tc.bytes)
		wantRecords := int64(1)
		if tc.bytes == 0 { wantRecords = 0 }
		if c.SharedDirtied != tc.pages || c.WALBytes != tc.bytes || c.WALRecords != wantRecords { t.Fatal("per-event boundary arithmetic",tc,c) }
	}
	s := enginetest.StartSession(t,e,nil)
	pgssOwnerExec(t,e,s,"CREATE UNLOGGED TABLE counter_unlogged_shape(k integer)")
	pgssOwnerExec(t,e,s,"CREATE TEMP TABLE counter_temp_shape(k integer)")
	read = e.CatalogRead()
	seen := 0
	for _, descriptor := range read.Descriptors() {
		if r,ok := descriptor.(*catalog.Relation); ok && (r.Name() == "counter_unlogged_shape" || r.Name() == "counter_temp_shape") {
			c := core.StatementMutationCounters(read,r,72)
			if c.WALRecords != 0 || c.WALBytes != 0 || r.Name() == "counter_temp_shape" && (c.LocalDirtied != 1 || c.SharedDirtied != 0) || r.Name() == "counter_unlogged_shape" && (c.SharedDirtied != 1 || c.LocalDirtied != 0) { t.Fatal("actual persistence classification",r.Name(),c) }
			seen++
		}
	}
	if seen != 2 { t.Fatal("missing actual persistence descriptors",seen) }
	for i := range wide { wide[i] = sqltype.Null(text) }
	image,err = core.StatementMutationImage(ctx,nil,&storage.RowVersion{Values:wide})
	if err != nil || image != 56 { t.Fatal("wide NULL bitmap",image,err) }
	canceled,cancel := context.WithCancel(ctx)
	cancel()
	if _,err = core.StatementMutationImage(canceled,nil,&storage.RowVersion{Values:full}); !errors.Is(err,context.Canceled) { t.Fatal("sizing lost cancellation",err) }
	numeric := enginetest.Usage(e,sqltype.Numeric).Type
	for _, tc := range []struct{ n uint64; want string }{{0,"0"},{uint64(math.MaxInt64)+1,"9223372036854775808"},{math.MaxUint64,"18446744073709551615"}} {
		value,err := core.StatementWALBytes(ctx,numeric,tc.n)
		if err != nil { t.Fatal(err) }
		got,err := numeric.Output(ctx,value)
		if err != nil || got != tc.want || value.Type() != numeric { t.Fatal("exact unsigned NUMERIC",got,tc.want,err) }
	}
}

func TestPgStatStatementsCountersMutation(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t,e,nil)
	for _, query := range []string{"SET pg_stat_statements.track='none'","CREATE TABLE counter_accept(k integer PRIMARY KEY,v text)","SET pg_stat_statements.track_utility=off","SET pg_stat_statements.track='top'"} { pgssOwnerExec(t,e,s,query) }
	for _, tc := range []struct{ query string; records int64; bytes uint64; stageRows int; deletion bool }{
		{"INSERT INTO counter_accept VALUES(1,'a'),(2,'b')",2,144,2,false},
		{"INSERT INTO counter_accept VALUES(3,'c') RETURNING k",1,72,1,false},
		{"UPDATE counter_accept AS accepted_update SET v=NULL WHERE k=1 RETURNING k",1,112,1,false},
		{"DELETE FROM counter_accept AS accepted_delete WHERE k=1 RETURNING k",1,40,0,true},
		{"UPDATE counter_accept AS accepted_empty SET v=NULL WHERE k=99",0,0,0,false},
	} {
		op,err := s.Acquire(context.Background())
		if err != nil { t.Fatal(err) }
		op.OriginalSQL,op.SQLAvailable = tc.query,true
		scope,err := e.StartStatement(op)
		if err != nil { op.Done(); t.Fatal(err) }
		result,err := scope.Execute(enginetest.Statement(t,tc.query),0,nil)
		if err == nil && result.Rows != nil {
			values := make([]sqltype.Value,len(result.Rows.Schema()))
			for {
				present,nextErr := result.Rows.Next(context.Background(),values)
				if nextErr != nil { err = nextErr; break }
				if !present { break }
			}
		}
		if err != nil { scope.Finish(err); op.Done(); t.Fatal(tc.query,err) }
		c := core.ScopeStatementCounters(scope)
		rows := 0
		for _, versions := range scope.Stage.Rows { rows += len(versions) }
		if c.WALRecords != tc.records || c.WALBytes != tc.bytes || c.SharedDirtied != tc.records || rows != tc.stageRows || tc.deletion && len(scope.Stage.Deletes) != 1 {
			t.Fatal("canonical accepted immutable window",tc,c,rows,len(scope.Stage.Deletes))
		}
		if result.Rows != nil { err = result.Rows.Close() }
		err = scope.Finish(err)
		op.Done()
		if err != nil { t.Fatal(err) }
	}
	// The real producer accepts its first immutable RETURNING version and then
	// rejects a duplicate. The phase must not publish that pending work.
	const failed = "INSERT INTO counter_accept VALUES(4,'d'),(2,'duplicate') RETURNING k"
	before := make(map[statistics.StatementKey]statistics.StatementEntry)
	for _, entry := range e.Cluster.StatementStatistics.Snapshot() { before[entry.Key] = entry }
	op,err := s.Acquire(context.Background())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	op.OriginalSQL,op.SQLAvailable = failed,true
	scope,err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	result,err := scope.Execute(enginetest.Statement(t,failed),0,nil)
	if err == nil || core.ScopeStatementCounters(scope).WALBytes != 72 { t.Fatal("real late duplicate/accepted prefix missing",core.ScopeStatementCounters(scope),err) }
	if result.Rows != nil { result.Rows.Close() }
	if err = scope.Finish(err); err == nil { t.Fatal("failed producer lost terminal failure") }
	for _, entry := range e.Cluster.StatementStatistics.Snapshot() {
		if previous,ok := before[entry.Key]; !ok || previous != entry { t.Fatal("failed accepted prefix became successful work",entry) }
	}
}
