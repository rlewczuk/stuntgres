package routine

import (
 "testing"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

func TestAggregateOrderedDetachedDefinition(t *testing.T){
 state:=catalog.NewState(1)
 for _,typ:=range sqltype.Builtins(){d:=&catalog.Type{SQLType:typ,Array:typ.AssociatedArray()};state.SetDescriptor(d.Address(),d)}
 var plus *catalog.Procedure
 procedures:=BuiltinProcedures(authorization.PrincipalRef{})
 for i:=range procedures{d:=&procedures[i];state.SetDescriptor(d.Address(),d);if d.Name()=="int4pl"&&d.Signature=="23,23"{plus=d}}
 if plus==nil{t.Fatal("missing actual int4pl")}
 read:=catalog.NewRead(nil,state)
 fixed:=&catalog.Procedure{ObjectAddress:ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:94001},ObjectName:"ordered",RoutineKind:catalog.AggregateRoutine,Signature:"23,23",Result:23,Aggregate:&catalog.OwnedAggregate{Definition:catalog.AggregateDefinition{Function:94001,Kind:"o",DirectArgs:1,Functions:[8]ids.OID{plus.Address().ObjectID,plus.Address().ObjectID},TransitionType:23,HasInitial:true,Initial:"0",FinalModify:"w",MovingFinalModify:"r"}}}
 if err:=ValidateAggregateDefinition(read,fixed);err!=nil{t.Fatal("legal state/row and state/direct vectors rejected",err)}
 hypothetical:=*fixed;hypDefinition:=*fixed.Aggregate;hypDefinition.Definition.Kind="h";hypothetical.Aggregate=&hypDefinition
 if err:=ValidateAggregateDefinition(read,&hypothetical);err!=nil{t.Fatal("matching hypothetical declaration rejected",err)}
 for _,change:=range []func(*catalog.Procedure){
  func(d *catalog.Procedure){d.Aggregate.Definition.Kind="x"},
  func(d *catalog.Procedure){d.Aggregate.Definition.DirectArgs=-1},
  func(d *catalog.Procedure){d.Aggregate.Definition.DirectArgs=3},
  func(d *catalog.Procedure){d.Aggregate.Definition.FinalExtra=true},
  func(d *catalog.Procedure){d.Aggregate.Definition.Kind="h";d.Signature="25,23"},
  func(d *catalog.Procedure){d.Aggregate.Definition.FinalModify=""},
  func(d *catalog.Procedure){d.Aggregate.Variadic=true},
 }{
  bad:=*fixed;definition:=*fixed.Aggregate;bad.Aggregate=&definition;change(&bad)
  if err:=ValidateAggregateDefinition(read,&bad);err==nil{t.Fatal("malformed detached ordered declaration admitted",bad.Aggregate.Definition)}
 }
 paired:=&catalog.Procedure{ObjectAddress:ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:94002},ObjectName:"not_a_builtin_name",RoutineKind:catalog.AggregateRoutine,Signature:"2276",Result:20,Aggregate:&catalog.OwnedAggregate{Variadic:true,Definition:catalog.AggregateDefinition{Function:94002,Kind:"h",DirectArgs:1,Functions:[8]ids.OID{3971,3987},TransitionType:2281,FinalExtra:true,FinalModify:"w",MovingFinalModify:"r"}}}
 if err:=ValidateAggregateDefinition(read,paired);err!=nil{t.Fatal("real variadic support declaration rejected",err)}
	for _, extra := range []bool{false, true} {
		prefixed := *paired
		owned := *paired.Aggregate
		prefixed.Aggregate = &owned
		prefixed.Signature = "23,2276"
		owned.Definition.DirectArgs = 2
		owned.Definition.FinalExtra = extra
		if err := ValidateAggregateDefinition(read, &prefixed); err == nil {
			t.Fatal("rank_final declaration expanded to accept an unsupported fixed prefix", extra)
		}
	}
	rank, _ := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3987})
	internal, _ := read.LookupType(2281)
	anyType, _ := read.LookupType(2276)
	integer, _ := read.LookupType(sqltype.Int4)
	canonical := []sqltype.Usage{internal.Usage(), anyType.Usage()}
	if formals, _, err := AggregateSupportTypes(read, nil, rank, nil, canonical); err != nil || len(formals) != 2 {
		t.Fatal("canonical paired ANY lookup rejected", formals, err)
	}
	expanded := []sqltype.Usage{internal.Usage(), integer.Usage(), integer.Usage()}
	if _, _, err := AggregateSupportTypes(read, nil, rank, nil, expanded); err == nil {
		t.Fatal("declaration lookup expanded a variadic support")
	}
	if formals, result, err := AggregateSupportTypes(read, nil, rank, paired.Aggregate, expanded); err != nil || len(formals) != 3 || result.Type.ID() != sqltype.Int8 {
		t.Fatal("retained runtime direct/typed-NULL vector lost specialization", formals, result, err)
	}
 paired.Aggregate.Definition.Kind="o";paired.Aggregate.Definition.DirectArgs=0
 if err:=ValidateAggregateDefinition(read,paired);err!=nil{t.Fatal("legal aggregated-only ANY rejected",err)}
 paired.Aggregate.Definition.Kind="h"
 if err:=ValidateAggregateDefinition(read,paired);err==nil{t.Fatal("hypothetical aggregated-only ANY admitted")}
}
