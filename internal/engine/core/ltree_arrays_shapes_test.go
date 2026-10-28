package core_test

import (
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/analyze"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/sqltype"
)

func ltreeArrayPreparedShapes(t *testing.T){
 e:=engine.New();op,done:=enginetest.RoleOperation(t,e,"postgres");defer done()
 enginetest.RoleExecute(t,e,op,"CREATE EXTENSION ltree")
 enginetest.RoleExecute(t,e,op,"CREATE DOMAIN la_shape_domain AS ltree")
 view:=e.CatalogRead()
 integerArray,ok:=view.LookupType(1007);if !ok{t.Fatal("integer array absent")}
 types:=[]*sqltype.Descriptor{domainUnitType(t,view,"_ltree").SQLType,domainUnitType(t,view,"_lquery").SQLType,domainUnitType(t,view,"_ltxtquery").SQLType,domainUnitType(t,view,"_la_shape_domain").SQLType,integerArray}
 d:=core.Description{Types:make(map[analyze.PreparedTypeKey]analyze.TypeOccurrence)}
 for i,typ:=range types{key:=analyze.PreparedTypeKey{Offset:i+1,Kind:analyze.IdentityCast};d.Types[key]=analyze.TypeOccurrence{Kind:analyze.IdentityCast,Base:typ.Usage()}}
 eligible:=d.RetireAnalysisTypeCastsForRebind(true)
 for i,typ:=range types{
  key:=analyze.PreparedTypeKey{Offset:i+1,Kind:analyze.IdentityCast};entry,found:=eligible[key]
  if found!=(i<2) || found && entry.Base.Type!=typ{t.Fatal("shape gate widened beyond exact direct companions",i,found)}
 }
}
