package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestResourceTemporaryAccessBoundaries(t *testing.T) {
	e:=engine.New()
	owner:=sessionStart(t,e,nil)
	peer:=sessionStart(t,e,nil)
	sessionSQL(t,owner,"CREATE TEMP TABLE resource_boundary(v integer CHECK(v>0))")
	sessionSQL(t,owner,"SET temp_buffers=2048")
	executeError:=func(s *session.Session,query,code,message string) {
		t.Helper(); op,err:=s.Acquire(context.Background()); if err!=nil { t.Fatal(err) }
		_,err=e.Execute(op,enginetest.Statement(t,query),0,nil); op.Done()
		var diagnostic *sqlstate.Error
		if !errors.As(err,&diagnostic) || diagnostic.Code!=code || diagnostic.Message!=message { t.Fatal(query,err) }
	}
	// Failing a CHECK before any successful table storage access must not
	// initialize the logical simulation.
	executeError(owner,"INSERT INTO resource_boundary VALUES(-1)","23514",`new row for relation "resource_boundary" violates check constraint "resource_boundary_v_check"`)
	sessionSQL(t,owner,"SET temp_buffers=3072")
	namespace:=e.Catalog.State.Get(owner.TemporaryNamespace).Name()
	executeError(peer,"SELECT v FROM "+namespace+".resource_boundary","0A000","cannot access temporary tables of other sessions")
	sessionSQL(t,peer,"SET temp_buffers=2048")
	// Native's specified boundary is successful logical table access, not
	// PostgreSQL's physical page/cache initialization timing.
	result:=sessionSQL(t,owner,"SELECT v FROM resource_boundary")
	if rows:=enginetest.Tuples(result); len(rows)!=0 { t.Fatal(rows) }
	sessionSQL(t,owner,"BEGIN")
	store:=owner.Settings
	store.Mu.Lock(); before:=len(store.Actions); store.Mu.Unlock()
	executeError(owner,"SET temp_buffers='1024'","22023",`invalid value for parameter "temp_buffers": 1024`)
	store.Mu.Lock(); after:=len(store.Actions); store.Mu.Unlock()
	if after!=before { t.Fatal("refused assignment touched setting actions",before,after) }
	sessionSQL(t,owner,"ROLLBACK")
	sessionSQL(t,owner,"RESET temp_buffers")
	sessionSQL(t,owner,"SET temp_buffers=3072")
}

// Publication must reject initialized candidates before Touch, even when
// parsing fails earlier or the current reset value differs from the captured
// count. Inspect the real owner immediately after Execute, before Finish can
// restore a failed statement checkpoint.
func TestResourceTemporaryInitializedValidation(t *testing.T) {
	for _, prepared := range []bool{false,true} {
		for _, bound := range []bool{false,true} {
			for _, tc := range []struct{input,message,detail string}{
				{"bogus",`invalid value for parameter "temp_buffers": "bogus"`,""},
				{"99",`99 8kB is outside the valid range for parameter "temp_buffers" (100 8kB .. 1073741823 8kB)`,""},
				{"1MB",`invalid value for parameter "temp_buffers": 128`,`"temp_buffers" cannot be changed after any temporary tables have been accessed in the session.`},
				{"8MB",`invalid value for parameter "temp_buffers": 1024`,`"temp_buffers" cannot be changed after any temporary tables have been accessed in the session.`},
			} {
				t.Run(fmt.Sprintf("prepared=%v/bound=%v/%s",prepared,bound,tc.input),func(t *testing.T) {
					e:=engine.New()
					s:=sessionStart(t,e,nil)
					for _,query:=range []string{"CREATE TEMP TABLE resource_initialized(v integer)","SET temp_buffers=2048","INSERT INTO resource_initialized VALUES(11)","RESET temp_buffers","BEGIN","SET application_name='journal-sentinel'","SAVEPOINT resource_validation"} { sessionSQL(t,s,query) }
					query:="SET temp_buffers='"+tc.input+"'"
					count:=0
					var importer core.FrameImporter
					if bound {
						query="SELECT set_config('temp_buffers',$1,false)"
						count=1
						importer=func(_ context.Context,info analyze.ParameterInfo,frame []sqltype.Value,_ sqltype.InputEnvironment)error {
							frame[0]=sqltype.String(info.Types[0].Type,tc.input)
							return nil
						}
					}
					ast:=enginetest.Statement(t,query)
					var description core.StatementDescription
					if prepared { ast,description=sequencePreparedDescription(t,e,s,query) }
					store:=s.Settings
					store.Mu.Lock()
					beforeActions,beforeValues,beforeTyped:=maps.Clone(store.Actions),maps.Clone(store.Values),maps.Clone(store.Typed)
					beforeDefaults,beforeProvider:=maps.Clone(store.Defaults),maps.Clone(store.ProviderDefaults)
					store.Mu.Unlock()
					op,err:=s.Acquire(context.Background()); if err!=nil { t.Fatal(err) }
					scope,err:=e.StartStatement(op)
					if err!=nil { op.Done(); t.Fatal(err) }
					if prepared { scope.Expected=&description }
					result,executeErr:=scope.Execute(ast,count,importer)
					store.Mu.Lock()
					unchanged:=reflect.DeepEqual(store.Actions,beforeActions) && reflect.DeepEqual(store.Values,beforeValues) && reflect.DeepEqual(store.Typed,beforeTyped) && reflect.DeepEqual(store.Defaults,beforeDefaults) && reflect.DeepEqual(store.ProviderDefaults,beforeProvider)
					store.Mu.Unlock()
					if result.Rows!=nil { result.Rows.Close() }
					err=scope.Finish(executeErr)
					op.Done()
					want:=sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22023",Message:tc.message,Detail:tc.detail}
					var got *sqlstate.Error
					if !errors.As(err,&got) || *got!=want { t.Fatalf("diagnostic got %#v want %#v",got,want) }
					if !unchanged { t.Fatal("rejected candidate changed journal/value/typed/default/source provenance before Finish") }
					sessionSQL(t,s,"ROLLBACK TO resource_validation")
					rows:=enginetest.Tuples(sessionSQL(t,s,"SELECT setting,source FROM pg_settings WHERE name='temp_buffers'"))
					if !reflect.DeepEqual(rows,[][]driver.Value{{"1024","default"}}) { t.Fatal("failed assignment changed value/source",rows) }
					sessionSQL(t,s,"ROLLBACK")
				})
			}
		}
	}
}
