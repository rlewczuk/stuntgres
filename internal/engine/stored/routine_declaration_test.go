package stored

import (
 "context"
 "errors"
 "strings"
 "testing"

 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/proc"
 "stuntgres/internal/sqltype"
)

func TestRoutineDeclarationRecipePresentation(t *testing.T) {
 read,_,_,_:=routineCaptureFixture(t)
 namespace:=&catalog.Namespace{ObjectAddress:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:11},ObjectName:"pg_catalog",ObjectRevision:1}
 read.State().SetDescriptor(namespace.Address(),namespace)
 callee:=&catalog.Procedure{ObjectAddress:ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:1707},ObjectNamespace:namespace.Address(),ObjectName:"round",Signature:"1700,23",ObjectRevision:1}
 read.State().SetDescriptor(callee.Address(),callee)
 read.State().BindName(catalog.DescriptorNameKey(callee),callee.Address())
 body,err:=proc.NewRoutineDeclarationBody(proc.DeclarationRecipe{Return:true,Parts:[]proc.DeclarationPart{proc.DeclarationFunction("round","1700,23"),proc.DeclarationText("("),proc.DeclarationArgument(0),proc.DeclarationText(", 0)")}})
 if err!=nil {t.Fatal(err)}
 target:=&catalog.Procedure{ObjectAddress:ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:1708},ObjectNamespace:namespace.Address(),ObjectName:"round",Signature:"1700",DeclarationBody:body,ObjectRevision:1}
 read.State().SetDescriptor(target.Address(),target)
 read.State().SetProcedureNames(catalog.RebuildProcedureNames(read.Descriptors()))
 policy:=Presentation{Deparse:true,Path:[]ids.ObjectAddress{namespace.Address()}}
 assert:=func(want string) {t.Helper();got,err:=RoutineDeclarationSQL(t.Context(),read,target,policy);if err!=nil || got!=want {t.Fatalf("body=%q, %v; want %q",got,err,want)}}
 // Independently transcribed from pinned PG18.6 round(numeric), not generated
 // by a native renderer or obtained by round-tripping its output.
 assert("RETURN round($1, 0)\n")
 policy.Output.QuoteAllIdentifiers=true
 assert("RETURN \"round\"($1, 0)\n")
 renamed:=*callee;renamed.ObjectName="New Round";renamed.ObjectRevision++
 read.State().SetDescriptor(renamed.Address(),&renamed)
 read.State().UnbindName(catalog.DescriptorNameKey(callee))
 read.State().BindName(catalog.DescriptorNameKey(&renamed),renamed.Address())
 read.State().SetProcedureNames(catalog.RebuildProcedureNames(read.Descriptors()))
 assert("RETURN \"New Round\"($1, 0)\n")
 target.InputNames=catalog.NewRoutineInputNames("select")
 assert("RETURN \"New Round\"(\"select\", 0)\n")
 policy.Path=nil
 assert("RETURN \"pg_catalog\".\"New Round\"(\"select\", 0)\n")
 // Publication identity, not an equal descriptor or executable provider, owns
 // the disclosure. No provider or authorization has been installed here.
 copied:=*target
 if _,err:=RoutineDeclarationSQL(t.Context(),read,&copied,policy);!errors.Is(err,ids.ErrCatalogReplaced) {t.Fatalf("copied descriptor: %v",err)}
 canceled,cancel:=context.WithCancel(t.Context());cancel()
 if _,err:=RoutineDeclarationSQL(canceled,read,target,policy);!errors.Is(err,context.Canceled) {t.Fatalf("cancellation: %v",err)}
}

func TestRoutineDeclarationAtomicAndLiterals(t *testing.T) {
 read,_,_,_:=routineCaptureFixture(t)
 namespace:=&catalog.Namespace{ObjectAddress:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:11},ObjectName:"pg_catalog",ObjectRevision:1}
 read.State().SetDescriptor(namespace.Address(),namespace)
 text:=read.Get(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:25})
 read.State().BindName(catalog.DescriptorNameKey(text),text.Address())
 body,err:=proc.NewRoutineDeclarationBody(proc.DeclarationRecipe{Parts:[]proc.DeclarationPart{proc.DeclarationText("SELECT "),proc.DeclarationString("a'b\\c"),proc.DeclarationText("::"),proc.DeclarationTypeName(25,"text")}})
 if err!=nil {t.Fatal(err)}
 target:=&catalog.Procedure{ObjectAddress:ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:950},DeclarationBody:body}
 read.State().SetDescriptor(target.Address(),target)
 for _,tc:=range []struct {nonstandard,quoted bool;want string}{
  {false,false,"BEGIN ATOMIC\n SELECT 'a''b\\c'::text;\nEND\n"},
  {true,false,"BEGIN ATOMIC\n SELECT 'a''b\\\\c'::text;\nEND\n"},
  {false,true,"BEGIN ATOMIC\n SELECT 'a''b\\c'::\"text\";\nEND\n"},
 } {
  got,err:=RoutineDeclarationSQL(t.Context(),read,target,Presentation{Deparse:true,Path:[]ids.ObjectAddress{namespace.Address()},NonstandardStrings:tc.nonstandard,Output:sqltype.OutputEnvironment{QuoteAllIdentifiers:tc.quoted}})
  if err!=nil || got!=tc.want {t.Fatalf("atomic=%q, %v; want %q",got,err,tc.want)}
 }
 structure,err:=body.StructuralOutput(t.Context())
 if err!=nil || !strings.HasPrefix(structure,"{RoutineDeclarationBody {parser.Select ") {t.Fatalf("structure=%q, %v",structure,err)}
}
