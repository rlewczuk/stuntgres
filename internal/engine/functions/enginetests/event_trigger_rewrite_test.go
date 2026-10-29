package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestEventTriggerRewriteHelperRuntime(t *testing.T) {
	for _, tc:=range []struct{name string; oid bool; proc ids.OID; typ sqltype.ID; out string}{
		{"pg_event_trigger_table_rewrite_oid",true,4566,sqltype.Oid,"oid"},
		{"pg_event_trigger_table_rewrite_reason",false,4567,sqltype.Int4,""},
	} {
		t.Run(tc.name,func(t *testing.T) {
			e,op,scope:=strictNullUnitFixture(t)
			node,_,evaluation:=strictNullUnitBind(t,e,op,scope,tc.name+"()")
			n:=node.(functions.EventTriggerRewrite)
			d,err:=exec.ExpressionRoutineDescriptor(n)
			if err!=nil || d==nil || d.Address().ObjectID!=tc.proc || d.Result!=ids.OID(tc.typ) || !d.Strict || d.Volatility!=expr.Stable || d.ReturnsSet() || d.Builtin==nil || d.Builtin.Parallel!='r' || d.Signature!="" { t.Fatal("released declaration",d,err) }
			if tc.oid {
				o:=d.OutputDeclaration(); if o==nil || o.Len()!=1 || o.Inputs()!=0 || o.Name(0)!="oid" || o.Type(0)!=26 { t.Fatal("single OUT",o) }
			} else if d.OutputDeclaration()!=nil { t.Fatal("unexpected OUT") }
			if expr.SourceOutName(n)!=tc.out || n.ResultUsage().Type.ID()!=tc.typ || n.Validate()!=nil || n.IntrinsicVolatility()!=expr.Stable || n.PlanningFold()!=expr.FoldNever || n.PreflightFold()!=expr.PreflightKeep || len(n.Children())!=0 || n.ClosedDefault(func(expr.Expr)bool{return true}) { t.Fatal("live protocol",n) }
			before:=n
			info:=n.Info; info.Occurrence++
			next,err:=n.Rebuild(info,nil)
			if err!=nil || next.(functions.EventTriggerRewrite).Info!=info || next.(functions.EventTriggerRewrite).RoutineReference!=n.RoutineReference || n!=before { t.Fatal("provenance",next,err) }
			if _,err:=n.Substitute(info,[]expr.Expr{n}); err==nil { t.Fatal("children accepted") }
			bad:=info; bad.Usage.Type=nil
			if _,err:=n.Rebuild(bad,nil); err==nil { t.Fatal("nil type accepted") }
			bad.Usage.Type,_=e.CatalogRead().LookupType(sqltype.Text)
			if _,err:=n.Rebuild(bad,nil); err==nil { t.Fatal("wrong type accepted") }
			render,err:=n.Render(); if err!=nil || render.Name!=tc.name { t.Fatal(render,err) }
			capture,ok,err:=n.Capture(expr.CaptureFunction); if err!=nil || !ok || capture.Count!=0 { t.Fatal(capture,ok,err) }
			if !n.SameGroup(n,nil,false) || n.ValidateStoredProvider()!=nil { t.Fatal("stored/group protocol") }
			retagged,changed,err:=stored.RetagTypes(e.CatalogRead(),n,func(d *sqltype.Descriptor)*sqltype.Descriptor{return d})
			if err!=nil || changed || !reflect.DeepEqual(retagged,n) { t.Fatal("retag",retagged,err) }
			for _,pair:=range []struct{oid ids.OID; reason int32}{{50001,4},{50002,15},{50001,2}} {
				scope.EventCommand=&core.EventCommand{Tag:"ALTER TABLE",Phase:"table_rewrite",RewriteOID:pair.oid,RewriteReason:pair.reason}
				value,err:=n.Eval(&evaluation); if err!=nil || value.Type()!=n.Usage.Type { t.Fatal(value,err) }
				if tc.oid { got,err:=value.OID(); if err!=nil || got!=uint32(pair.oid) { t.Fatal(got,err) } } else { got,err:=value.Integer(); if err!=nil || got!=int64(pair.reason) { t.Fatal(got,err) } }
				cursor,err:=n.OpenSource(&evaluation); if err!=nil { t.Fatal(err) }
				_,present,err:=cursor.Next(context.Background()); if err!=nil || !present { t.Fatal(present,err) }
				_,present,err=cursor.Next(context.Background()); cursor.Close(); if err!=nil || present { t.Fatal("EOF",present,err) }
				if n!=before { t.Fatal("retained lexical pair") }
			}
			for _,command:=range []*core.EventCommand{nil,{Phase:"ddl_command_start"},{Phase:"ddl_command_end",RewriteOID:50001,RewriteReason:4},{Phase:"sql_drop",RewriteOID:50001,RewriteReason:4},{Phase:"table_rewrite",RewriteReason:4},{Phase:"table_rewrite",RewriteOID:50001}} {
				scope.EventCommand=command
				_,err:=n.Eval(&evaluation); var state interface{SQLState()string}; if !errors.As(err,&state) || state.SQLState()!="39P03" { t.Fatal("protocol",err) }
			}
		})
	}
}
