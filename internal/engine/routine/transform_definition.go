package routine

import (
 "errors"
 "fmt"
 "strings"

 "stuntgres/internal/engine/access"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ddl"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqlstate"
)

func transformType(h DDLHost,a ddl.Authority,raw parser.TypeName)(*catalog.Type,string,error) {
 id,err:=h.ResolveRegtype(a,raw)
 if err!=nil { return nil,"",err }
 view:=h.AccessView(a).Catalog
 typ:=view.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:id})
 path,err:=a.ReferencePath()
 if err!=nil { return nil,"",err }
 return typ,catalog.TypeDiagnosticNameAt(view,nil,typ,path),nil
}

// Ownership is checked on the original transform type. Only the diagnostic
// for a denied true array names its installed element, like aclcheck_error_type.
func checkTransformTypeOwner(h DDLHost,a ddl.Authority,typ *catalog.Type) error {
 err:=ddl.Check(h,a,access.Requirement{Object:ddl.ObjectRef(h,a,typ.Address()),OwnerOnly:true})
 diagnostic,ok:=err.(*sqlstate.Error)
 if !ok || diagnostic.Code!="42501" || !typ.SQLType.IsArray() || typ.SQLType.IsCatalogVector() || typ.SQLType.IsDomain() { return err }
 view:=h.AccessView(a).Catalog
 element,ok:=view.Type(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(typ.SQLType.Element().ID())})
 if !ok { return err }
 path,pathErr:=a.ReferencePath()
 if pathErr!=nil { return err }
 next:=*diagnostic
 next.Message="must be owner of type "+catalog.TypeDiagnosticNameAt(view,nil,element,path)
 return &next
}

// writtenTransformTypeName follows TypeNameToString, not SQL identifier
// quoting or visibility-dependent catalog formatting.
func writtenTransformTypeName(raw parser.TypeName) string {
 var name strings.Builder
 if raw.System {
  name.WriteString("pg_catalog.")
 } else if raw.Schema!=nil {
  name.WriteString(raw.Schema.Text)
  name.WriteByte('.')
 }
 name.WriteString(raw.Name)
 for range raw.ArrayBounds { name.WriteString("[]") }
 return name.String()
}
func transformPair(view catalog.Read,typ,language ids.ObjectAddress)*catalog.Transform {
 key:=ids.NameKey{ClassID:ids.TransformClassID,Name:fmt.Sprintf("%d,%d",typ.ObjectID,language.ObjectID)}
 pair,_:=view.Get(view.NameAddress(key)).(*catalog.Transform)
 return pair
}
func transformCallback(h DDLHost,a ddl.Authority,target *parser.GrantRoutineTarget,result ids.OID,from bool)(ids.ObjectAddress,error) {
 if target==nil { return ids.ObjectAddress{},nil }
 p,_,err:=ResolveUtilityRoutine(h,a,"function",*target)
 if err!=nil { return ids.ObjectAddress{},err }
 if err=checkRoutineOwner(h,a,p); err!=nil {
  if diagnostic,ok:=err.(*sqlstate.Error); ok && diagnostic.Code=="42501" {
   return ids.ObjectAddress{},sqlError("42501","must be owner of function "+catalog.ObjectNameText(target.Name))
  }
  return ids.ObjectAddress{},err
 }
 if err=ddl.Check(h,a,access.Requirement{Object:ddl.ObjectRef(h,a,p.Address()),Privileges:authorization.ExecutePrivilege}); err!=nil {
  if diagnostic,ok:=err.(*sqlstate.Error); ok && diagnostic.Code=="42501" {
   return ids.ObjectAddress{},sqlError("42501","permission denied for function "+catalog.ObjectNameText(target.Name))
  }
  return ids.ObjectAddress{},err
 }
 message:=""
 switch {
 case from && p.Result!=2281: message="return data type of FROM SQL function must be internal"
 case !from && p.Result!=result: message="return data type of TO SQL function must be the transform data type"
 case p.Volatility==expr.Volatile: message="transform function must not be volatile"
 case p.RoutineKind!=catalog.FunctionRoutine: message="transform function must be a normal function"
 case p.ReturnsSet(): message="transform function must not return a set"
 default:
  if p.Signature=="" { message="transform function must take one argument" } else {
   for _,ch:=range string(p.Signature) { if ch==',' { message="transform function must take one argument"; break } }
   if message=="" && p.Signature!="2281" { message="first argument of transform function must be type internal" }
  }
 }
 if message!="" { return ids.ObjectAddress{},sqlError("42P17",message) }
 return p.Address(),nil
}

// Keep transform diagnostic interpolation local; ordinary language DDL retains
// its own wording. Only proven name absence is conditional or reformatted.
func transformLanguageTarget(h DDLHost,a ddl.Authority,name string,missingOK bool)(*catalog.Language,error) {
 language,err:=languageTarget(h,a,name)
 if diagnostic,ok:=err.(*sqlstate.Error); ok && diagnostic.Code=="42704" &&
  h.AccessView(a).Catalog.NameAddress(ids.NameKey{ClassID:ids.LanguageClassID,Name:name})==(ids.ObjectAddress{}) {
  if missingOK { return nil,nil }
  return nil,sqlError("42704",fmt.Sprintf("language \"%s\" does not exist",name))
 }
 return language,err
}

// StageCreateTransform publishes no callback execution. All pair/consumer
// revisions and converter plans belong to this one private schema candidate.
func StageCreateTransform(h DDLHost,a ddl.Authority,s *parser.CreateTransform) error {
 typ,name,err:=transformType(h,a,s.Type)
 if err!=nil { return err }
 facts,_:=typ.SQLType.PGTypeMetadata()
 if facts.Kind=="p" { return sqlError("42809","data type "+writtenTransformTypeName(s.Type)+" is a pseudo-type") }
 if typ.SQLType.IsDomain() { return sqlError("42809","data type "+writtenTransformTypeName(s.Type)+" is a domain") }
 if err=checkTransformTypeOwner(h,a,typ); err!=nil { return err }
 if err=ddl.Check(h,a,access.Requirement{Object:ddl.ObjectRef(h,a,typ.Address()),Privileges:authorization.UsagePrivilege}); err!=nil { return err }
 language,err:=transformLanguageTarget(h,a,s.Language.Text,false)
 if err!=nil { return err }
 // Transform declaration checks language USAGE, not routine-body admission's
 // separate untrusted-language execution restriction or language ownership.
 if err=ddl.Check(h,a,access.Requirement{Object:ddl.ObjectRef(h,a,language.Address()),Privileges:authorization.UsagePrivilege}); err!=nil { return err }
 from,err:=transformCallback(h,a,s.From,typ.Address().ObjectID,true)
 if err!=nil { return err }
 to,err:=transformCallback(h,a,s.To,typ.Address().ObjectID,false)
 if err!=nil { return err }
 if from.ObjectID==0 && to.ObjectID==0 { return sqlError("42601","transform requires at least one direction") }
 previous:=transformPair(h.StageRead(),typ.Address(),language.Address())
 source:=h.StageRead()
 if previous!=nil && !s.Replace { return sqlError("42710",fmt.Sprintf("transform for type %s language \"%s\" already exists",name,language.Name())) }
 pair:=&catalog.Transform{Type:typ.Address(),SQLType:typ.SQLType,Language:language.Address(),From:from,To:to,Runtime:language.Host,SQLDefined:true}
 if previous!=nil { pair.ObjectAddress=previous.Address() } else {
  oid,err:=h.AllocateOID(); if err!=nil { return err }
  pair.ObjectAddress=ids.ObjectAddress{ClassID:ids.TransformClassID,ObjectID:oid}
 }
 pair.ObjectRevision,err=h.Revision()
 if err!=nil { return err }
 if previous==nil { err=ddl.CreateCommand(h.Stage(),"CREATE TRANSFORM",pair) } else { err=ddl.ReplaceCommand(h.Stage(),"CREATE TRANSFORM",pair) }
 if err!=nil { return err }
 if previous!=nil {
  if err=refreshTransformConsumers(h,source,pair); err!=nil { return err }
 }
 return h.RecordDependencyPath(a)
}

func refreshTransformConsumers(h DDLHost,source catalog.Read,pair *catalog.Transform)error {
 descriptors,err:=h.RefreshTransformConsumers(source,pair)
 if err!=nil { return err }
 for _,descriptor:=range descriptors { if err=h.Stage().Replace(descriptor);err!=nil { return err } }
 return nil
}

func StageDropTransform(h DDLHost,a ddl.Authority,s *parser.DropTransform) error {
 typ,name,err:=transformType(h,a,s.Type)
 if err!=nil {
  var diagnostic *sqlstate.Error
  if s.IfExists && errors.As(err,&diagnostic) && (diagnostic.Code=="42704" || diagnostic.Code=="3F000") { ddl.Notice(h,"00000",diagnostic.Message+", skipping"); return nil }
  return err
 }
 language,err:=transformLanguageTarget(h,a,s.Language.Text,s.IfExists)
 if err!=nil { return err }
 var pair *catalog.Transform
 if language!=nil { pair=transformPair(h.StageRead(),typ.Address(),language.Address()) }
 if pair==nil {
  if s.IfExists {
   ddl.Notice(h,"00000",fmt.Sprintf("transform for type %s language \"%s\" does not exist, skipping",writtenTransformTypeName(s.Type),s.Language.Text))
   return nil
  }
  return sqlError("42704",fmt.Sprintf("transform for type %s language \"%s\" does not exist",name,language.Name()))
 }
 // Pinned18.6 tests10 resolves the manual/source discrepancy: type owner is
 // the actual DROP authority; transform has no independent owner or ACL.
 if err=checkTransformTypeOwner(h,a,typ); err!=nil { return err }
 h.DropTarget(pair.Address())
 return h.RecordDependencyPath(a)
}
