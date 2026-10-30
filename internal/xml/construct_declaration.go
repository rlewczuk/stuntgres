package xml

import (
 "context"
 "fmt"
)

func constructorDeclaration(ctx context.Context, value Result) (Declaration,string,error) {
 if !value.Valid(){return Declaration{},"",fmt.Errorf("invalid XML constructor operand")}
 d,detail,err:=readContentDeclaration(ctx,value.Original());if err!=nil{return Declaration{},"",err}
 end:=d.End
 // Released concat/root ignore declaration syntax failure, including its body.
 if detail!="" {end=len(value.Original())}
 return d,value.Original()[end:],nil
}

func declarationParts(version string, present bool, standalone Standalone) ([]string,Declaration) {
 if !present&&standalone!=Absent {version="1.0";present=true}
 if (!present||version=="1.0")&&standalone==Absent{return nil,Declaration{}}
 parts:=[]string{"<?xml version=\"",version,"\""}
 if standalone==Yes {parts=append(parts," standalone=\"yes\"")}else if standalone==No {parts=append(parts," standalone=\"no\"")}
 parts=append(parts,"?>")
 end:=0;for _,part:=range parts{end+=len(part)}
 return parts,Declaration{Present:true,Version:version,Standalone:standalone,End:end}
}

// ConstructConcat accepts only nonNULL genuine values. SQL owns the allNULL case.
func ConstructConcat(ctx context.Context, values []Result) (Result,error) {
 if err:=ctx.Err();err!=nil{return Result{},err}
 bodies:=make([]string,len(values));version:="";common:=true;standalone:=Yes
 for i,value:=range values {
  d,body,err:=constructorDeclaration(ctx,value);if err!=nil{return Result{},err};bodies[i]=body
  if i==0 {version=d.Version}else if version!=d.Version {common=false}
  if !d.Present {common=false}
  if d.Standalone==Absent {standalone=Absent}else if d.Standalone==No&&standalone==Yes {standalone=No}
 }
 prefix,d:=declarationParts(version,common,standalone)
 if len(values)==0 {prefix=nil;d=Declaration{}}
 return constructed(ctx,append(prefix,bodies...),d)
}

// RootStandalone preserves the distinction between omitted and NO VALUE.
type RootStandalone uint8
const (
 RootPreserve RootStandalone = iota
 RootRemove
 RootYes
 RootNo
)
func ConstructRoot(ctx context.Context, value Result, version *string, standalone RootStandalone) (Result,error) {
 if err:=ctx.Err();err!=nil{return Result{},err}
 if standalone>RootNo{return Result{},fmt.Errorf("invalid XMLROOT standalone option")}
 old,body,err:=constructorDeclaration(ctx,value);if err!=nil{return Result{},err}
 next:=old.Standalone
 switch standalone {case RootRemove:next=Absent;case RootYes:next=Yes;case RootNo:next=No}
 v:="";if version!=nil {v=*version}
 prefix,d:=declarationParts(v,version!=nil,next)
 return constructed(ctx,append(prefix,body),d)
}
