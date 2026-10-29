package core

import (
 "fmt"
 "strings"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/engine/stored"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

// invokeRefint receives only an admitted native invocation. Its borrowed rows
// and literal arguments never escape this call or enter a retained SQL plan.
func (s *StatementScope) invokeRefint(d *catalog.Procedure, relation *catalog.Relation, p stored.TriggerParts, event stored.TriggerEvent, next, old []sqltype.Value) error {
 previousAtomic := s.plAtomic
 s.plAtomic = true
 defer func(){ s.plAtomic = previousAtomic }()
 read:=s.refintCatalog()
 if extension.RefintProviderOf(d)!=extension.StockRefint { return fmt.Errorf("stuntgres: invalid native trigger provider") }
 symbol,err:=extension.ProcedureSymbol(read,d); if err!=nil { return err }
 fail:=func(message string) error { return SQLError("XX000",symbol+": "+message) }
 if p.Level!=stored.TriggerRow { return fail("must be fired for row") }
 if symbol=="check_foreign_key" && event==stored.TriggerInsert { return fail("cannot process INSERT events") }
 if p.Timing!=stored.TriggerAfter { return fail("must be fired by AFTER trigger") }
 if symbol=="check_primary_key" {
  if event==stored.TriggerDelete { return fail("cannot process DELETE events") }
  return s.refintPrimary(relation,p,next)
 }
 return s.refintForeign(relation,p,event,next,old)
}

func refintLocal(relation *catalog.Relation, row []sqltype.Value, name string) (sqltype.Value, sqltype.Usage, error) {
 slot,ok:=relation.Index[name]
 if !ok || slot<0 || slot>=len(row) { return sqltype.Value{},sqltype.Usage{},SQLError("42703",fmt.Sprintf("there is no attribute \"%s\" in relation \"%s\"",name,relation.Name())) }
 return row[slot],relation.Columns[slot].Usage,nil
}
func refintPredicate(b *strings.Builder, keys []string) {
 for i,key:=range keys { fmt.Fprintf(b,"%s = $%d ",key,i+1); if i+1<len(keys) { b.WriteString("and ") } }
}
func (s *StatementScope) refintPrimary(relation *catalog.Relation,p stored.TriggerParts,next []sqltype.Value) error {
 args:=p.Arguments
 if len(args)%2!=1 { return SQLError("XX000","check_primary_key: odd number of arguments should be specified") }
 n:=len(args)/2
 values:=make([]sqltype.Value,n); inputs:=make([]sqltype.Usage,n)
 for i:=range n {
  value,usage,err:=refintLocal(relation,next,args[i]); if err!=nil { return err }; if value.IsNull() { return nil }; values[i],inputs[i]=value,usage.Type.Usage()
 }
 remote:=args[n]
 var b strings.Builder; fmt.Fprintf(&b,"select 1 from %s where ",remote); refintPredicate(&b,args[n+1:])
 q,err:=s.prepareRefint(b.String(),inputs); if err!=nil { return err }
 count,err:=s.executeRefint(q,inputs,values); if err!=nil { return err }
 if count==0 { return &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"09000",Message:"tuple references non-existent key",Detail:fmt.Sprintf("Trigger \"%s\" found tuple referencing non-existent key in \"%s\".",p.Name,remote)} }
 return nil
}

func refintLiteral(value string) string {
 // quote_literal_cstr always escapes apostrophes and uses E when a backslash
 // occurs, independently of standard_conforming_strings.
 prefix:=""; if strings.Contains(value,"\\") { prefix="E"; value=strings.ReplaceAll(value,"\\","\\\\") }
 return prefix+"'"+strings.ReplaceAll(value,"'","''")+"'"
}
func (s *StatementScope) refintForeign(relation *catalog.Relation,p stored.TriggerParts,event stored.TriggerEvent,next,old []sqltype.Value) error {
 args:=p.Arguments; total:=len(args)
 if total<5 { return SQLError("XX000",fmt.Sprintf("check_foreign_key: too short %d (< 5) list of arguments",total)) }
 typ,ok:=s.refintCatalog().LookupType(sqltype.Int4); if !ok { return fmt.Errorf("stuntgres: missing integer type") }
 parsed,err:=typ.Input(s.executionContext(),args[0],-1); if err!=nil { return err }
 references,err:=parsed.Integer(); if err!=nil { return err }
 if references<1 { return SQLError("XX000",fmt.Sprintf("check_foreign_key: %d (< 1) number of references specified",references)) }
 action:=byte(0); if len(args[1])!=0 { action=args[1][0]; if action>='A' && action<='Z' { action+='a'-'A' } }
 if action!='r' && action!='c' && action!='s' { return SQLError("XX000","check_foreign_key: invalid action "+args[1]) }
 keys:=(int64(total-2)-references)/(references+1)
 if keys<=0 || int64(total-2)!=references+keys*(references+1) { return SQLError("XX000",fmt.Sprintf("check_foreign_key: invalid number of arguments %d for %d references",total,references)) }
 n,r:=int(keys),int(references)
 values:=make([]sqltype.Value,n); inputs:=make([]sqltype.Usage,n)
 var literals []string
 if action=='c' && event==stored.TriggerUpdate { literals=make([]string,n) }
 unchanged:=event==stored.TriggerUpdate
 for i:=range n {
  value,usage,err:=refintLocal(relation,old,args[2+i]); if err!=nil { return err }; if value.IsNull() { return nil }; values[i],inputs[i]=value,usage.Type.Usage()
  if event==stored.TriggerUpdate {
   fresh,_,err:=refintLocal(relation,next,args[2+i]); if err!=nil { return err }
   if literals!=nil { literals[i]="NULL" }
   if fresh.IsNull() { unchanged=false; continue }
   previous,err:=value.Type().OutputWithEnvironment(s.executionContext(),value,s.Output); if err!=nil { return err }
   current,err:=fresh.Type().OutputWithEnvironment(s.executionContext(),fresh,s.Output); if err!=nil { return err }
   if previous!=current { unchanged=false }
   if literals!=nil { literals[i]=refintLiteral(current) }
  }
 }
 queries:=make([]refintQuery,r); remotes:=make([]string,r)
 for ref:=range r {
  offset:=2+n+ref*(n+1); remote:=args[offset]; columns:=args[offset+1:offset+n+1]; remotes[ref]=remote
  var b strings.Builder
  switch {
  case action=='r': fmt.Fprintf(&b,"select 1 from %s where ",remote)
  case action=='c' && event!=stored.TriggerUpdate: fmt.Fprintf(&b,"delete from %s where ",remote)
  default:
   fmt.Fprintf(&b,"update %s set ",remote)
   for i,column:=range columns {
    if action=='s' { fmt.Fprintf(&b,"%s = null",column) } else { fmt.Fprintf(&b," %s = %s ",column,literals[i]) }
    if i+1<n { b.WriteString(", ") }
   }
   b.WriteString(" where ")
  }
  refintPredicate(&b,columns)
  queries[ref],err=s.prepareRefint(b.String(),inputs); if err!=nil { return err }
 }
 if unchanged { return nil }
 for ref,q:=range queries {
  count,err:=s.executeRefint(q,inputs,values); if err!=nil { return err }
  if action=='r' {
   if count!=0 { return SQLError("09000",fmt.Sprintf("\"%s\": tuple is referenced in \"%s\"",p.Name,remotes[ref])) }; continue
  }
  verb:="are deleted"; if action=='s' { verb="are set to null" } else if event==stored.TriggerUpdate { verb="are updated" }
  s.Op.EmitNotice(session.LevelNotice,sqlstate.Error{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:fmt.Sprintf("%s: %d tuple(s) of %s %s",p.Name,count,remotes[ref],verb)})
 }
 return nil
}
