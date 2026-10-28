package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestSessionActivityLeafOwnership(t *testing.T) {
	e,op,scope:=strictNullUnitFixture(t)
	for i,tc:=range []struct {name string; oid uint32; id sqltype.ID; strict bool; volatility expr.Volatility}{{"current_query",817,sqltype.Text,false,expr.Volatile},{"pg_backend_pid",2026,sqltype.Int4,true,expr.Stable},{"pg_postmaster_start_time",2560,sqltype.Timestamptz,true,expr.Stable},{"pg_conf_load_time",2034,sqltype.Timestamptz,true,expr.Stable}} {
		node,_,evaluation:=strictNullUnitBind(t,e,op,scope,tc.name+"()")
		n:=node.(functions.SessionActivity)
		declaration,err:=exec.ExpressionRoutineDescriptor(n)
		if err!=nil || declaration==nil || uint32(declaration.Address().ObjectID)!=tc.oid || declaration.Strict!=tc.strict || declaration.Volatility!=tc.volatility || declaration.ReturnsSet() || n.ResultUsage().Type.ID()!=tc.id { t.Fatal("routine declaration",declaration,err) }
		if n.Kind!=functions.SessionActivityKind(i) || n.Validate()!=nil || n.IntrinsicVolatility()!=tc.volatility || n.PlanningFold()!=expr.FoldNever || n.PreflightFold()!=expr.PreflightKeep || n.ClosedDefault(func(expr.Expr)bool{return true}) || len(n.Children())!=0 { t.Fatal("leaf protocol",n) }
		before:=n; info:=n.Info; info.Occurrence++
		next,err:=n.Rebuild(info,nil); if err!=nil || next.(functions.SessionActivity).Info!=info || next.(functions.SessionActivity).RoutineReference!=n.RoutineReference || n!=before { t.Fatal("reconstruction provenance",next,err) }
		if _,err:=n.Rebuild(info,[]expr.Expr{n});err==nil { t.Fatal("leaf accepted children") }
		retagged,changed,err:=stored.RetagTypes(e.CatalogRead(),n,func(d *sqltype.Descriptor)*sqltype.Descriptor{return d})
		if err!=nil || changed || !reflect.DeepEqual(retagged,n) { t.Fatal("immutable recipe retag",retagged,err) }
		rendered,err:=n.Render(); if err!=nil || rendered.Name!=tc.name { t.Fatal(rendered,err) }
		capture,ok,err:=n.Capture(expr.CaptureFunction); if err!=nil || !ok || capture.Count!=0 { t.Fatal(capture,ok,err) }
		if !n.SameGroup(n,nil,false) { t.Fatal("same kind grouping") }; other:=n; other.Kind=functions.SessionActivityKind((i+1)%4); if n.SameGroup(other,nil,false) { t.Fatal("distinct kinds grouped") }
		bad:=n; bad.Kind=255; if bad.Validate()==nil { t.Fatal("invalid kind accepted") }; bad=n;bad.Usage.Type=nil;if bad.Validate()==nil { t.Fatal("nil type accepted") }
		if i==0 {
			op.SQLAvailable=false
			value,err:=n.Eval(&evaluation); if err!=nil || !value.IsNull() || value.Type()!=n.Usage.Type { t.Fatal("absent source",value,err) }
			op.SQLAvailable=true; op.OriginalSQL=""
			value,err=n.Eval(&evaluation); if err!=nil || value.IsNull() { t.Fatal("available empty source",value,err) }; text,err:=value.Text();if err!=nil || text!="" { t.Fatal(text,err) }
			op.OriginalSQL="first"; first,err:=n.Eval(&evaluation); if err!=nil { t.Fatal(err) }
			op.OriginalSQL="second"; second,err:=n.Eval(&evaluation); if err!=nil { t.Fatal(err) }; a,_:=first.Text();b,_:=second.Text(); if a!="first" || b!="second" || n!=before { t.Fatal("runtime captured on recipe",a,b) }
		}
		cursor,err:=n.OpenSource(&evaluation);if err!=nil { t.Fatal(err) }; _,present,err:=cursor.Next(context.Background());if err!=nil || !present { t.Fatal("scalar source",present,err) };_,present,err=cursor.Next(context.Background());if err!=nil || present { t.Fatal("scalar EOF",present,err) };cursor.Close()
	}
}
