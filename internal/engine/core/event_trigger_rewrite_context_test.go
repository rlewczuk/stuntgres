package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

func TestEventTriggerRewriteContext(t *testing.T) {
	e,scope:=plExceptionUnitScope(t)
	outer:=&core.EventCommand{Tag:"ALTER TABLE",Phase:"table_rewrite",RewriteOID:50001,RewriteReason:6}
	scope.EventCommand=outer
	host:=core.HostOf(scope).(functions.EventTriggerRewriteRuntime)
	oid,_:=e.CatalogRead().LookupType(sqltype.Oid)
	integer,_:=e.CatalogRead().LookupType(sqltype.Int4)
	check:=func(target uint32,reason int64) {
		t.Helper()
		v,err:=host.EventTriggerRewriteValue(true,oid); if err!=nil { t.Fatal(err) }
		got,err:=v.OID(); if err!=nil || got!=target { t.Fatal(got,err) }
		v,err=host.EventTriggerRewriteValue(false,integer); if err!=nil { t.Fatal(err) }
		r,err:=v.Integer(); if err!=nil || r!=reason { t.Fatal(r,err) }
	}
	check(50001,6)
	previous,err:=scope.BeginEventCommand("ALTER TABLE"); if err!=nil { t.Fatal(err) }
	if previous!=outer || scope.EventCommand==outer { t.Fatal("nested command pointer") }
	inner:=scope.EventCommand
	inner.Phase,inner.RewriteOID,inner.RewriteReason="table_rewrite",50002,4
	check(50002,4)
	if err:=scope.FinishEventCommand(previous,nil); err!=nil { t.Fatal(err) }
	if scope.EventCommand!=outer || outer.Phase!="table_rewrite" || outer.RewriteOID!=50001 || outer.RewriteReason!=6 { t.Fatal("normal exit lost exact outer pair") }
	check(50001,6)
	mark,err:=scope.BeginPLException(); if err!=nil { t.Fatal(err) }
	outer.RewriteOID,outer.RewriteReason=50003,15
	scope.EventCommand=&core.EventCommand{Tag:"ALTER TYPE",Phase:"table_rewrite",RewriteOID:50004,RewriteReason:4}
	if err:=scope.EndPLException(mark,true,errors.New("caught rewrite")); err!=nil { t.Fatal(err) }
	if scope.EventCommand!=outer { t.Fatal("checkpoint replaced command identity") }
	check(50001,6)
	previous,err=scope.BeginEventCommand("ALTER TYPE"); if err!=nil { t.Fatal(err) }
	failure:=errors.New("terminal rewrite failure")
	if err:=scope.FinishEventCommand(previous,failure); err!=failure || scope.EventCommand!=outer { t.Fatal("terminal failure lost outer context",err) }
	check(50001,6)
	scope.EventCommand=nil
	for _,wantOID:=range []bool{false,true} {
		result:=integer; if wantOID { result=oid }
		_,err:=host.EventTriggerRewriteValue(wantOID,result)
		var state interface{SQLState()string}
		if !errors.As(err,&state) || state.SQLState()!="39P03" { t.Fatal("detached command accepted",err) }
	}
	if outer.RewriteOID!=50001 || outer.RewriteReason!=6 { t.Fatal("detachment mutated retained command") }
}
