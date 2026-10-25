package stored

import (
 "fmt"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

func citextStringCallOf(node expr.Expr) (functions.String,bool) {
 call,ok:=node.(functions.String);return call,ok&&call.Citext!=nil
}
func validateCapturedCitextString(call functions.String,captured *catalog.Procedure) error {
 if err:=call.ValidateStoredProvider();err!=nil {return err}
 capture:=call.Citext
 if capture==nil||captured==nil||call.RoutineReference!=captured||int(capture.Member)<extension.CitextStringProcedureStart||int(capture.Member)>=extension.CitextMemberCount {return fmt.Errorf("stuntgres: invalid captured citext string declaration")}
 m:=extension.CitextStringMembers[int(capture.Member)-extension.CitextStringProcedureStart]
 if captured.Revision()==0||capture.Ref.Address!=captured.Address()||capture.Text!=capture.Text.Type.Usage()||captured.Implementation!=extension.StockCitext||captured.Builtin!=nil||captured.SQL!=nil||captured.Foreign!=nil||captured.RoutineKind!=catalog.FunctionRoutine||!captured.Strict||captured.Volatility!=expr.Immutable||captured.Installed!=m.Declaration()||captured.Language!=(ids.ObjectAddress{})||captured.Display!=(catalog.RoutineDisplay{Language:catalog.SQLRoutineLanguage})||call.Plan.Name()!=m.Name||len(call.Args)!=m.ArgCount()||captured.Signature!=m.Signature(call.Args[0].ResultUsage().Type.ID())||captured.Result!=m.Result {return fmt.Errorf("stuntgres: invalid captured citext string declaration")}
 return nil
}
func validateCitextStringDeclaration(view catalog.Read,original *catalog.Procedure,call functions.String,types func(*sqltype.Descriptor)*sqltype.Descriptor) (*catalog.Procedure,error) {
 invalid:=func()(*catalog.Procedure,error){return nil,fmt.Errorf("stuntgres: invalid stored citext string authority")}
 c:=call.Citext
 if c==nil||int(c.Member)<extension.CitextStringProcedureStart||int(c.Member)>=extension.CitextMemberCount||c.Ref.Epoch!=view.Epoch() {return invalid()}
 current,symbol,err:=currentInstalledProcedure(view,c.Ref,original);if err!=nil {return nil,err}
 m:=extension.CitextStringMembers[int(c.Member)-extension.CitextStringProcedureStart]
 if current.Implementation!=extension.StockCitext||symbol!=m.Name||len(call.Args)!=m.ArgCount()||current.Installed!=m.Declaration() {return invalid()}
 installation,ok:=view.Extension(current.Extension)
 if !ok||len(installation.Members)!=extension.CitextMemberCount||installation.Members[c.Member]!=current.Address() {return invalid()}
 base,ok:=view.Type(installation.Members[0]);if !ok||base.SQLType!=types(call.Args[0].ResultUsage().Type)||base.SQLType!=types(call.Args[1].ResultUsage().Type) {return invalid()}
 text,ok:=view.LookupType(sqltype.Text);if !ok||types(c.Text.Type)!=text {return invalid()}
 if len(call.Args)==3 {
  third:=base.SQLType
  if !m.ThirdCitext {third,ok=view.LookupType(m.Third);if !ok {return invalid()}}
  if types(call.Args[2].ResultUsage().Type)!=third {return invalid()}
 }
 result,ok:=view.LookupType(sqltype.ID(m.Result))
 if !ok||types(call.Usage.Type)!=result||current.Signature!=m.Signature(base.SQLType.ID())||current.Result!=m.Result {return invalid()}
 if err:=extension.StockCitext.ValidateProcedure(view,current);err!=nil {return nil,err}
 return current,nil
}
func remapCitextStringRoutine(view catalog.Read,reader catalog.DefinitionReader,captured,original *catalog.Procedure,call functions.String,types func(*sqltype.Descriptor)*sqltype.Descriptor) error {
 if captured==nil||original==nil||captured.Revision()!=original.Revision() {return fmt.Errorf("stuntgres: invalid remapped citext string original")}
 source:=call;capture:=*call.Citext;capture.Ref.Address=captured.Address();source.Citext=&capture
 if err:=validateCapturedCitextString(source,captured);err!=nil {return err}
 descriptor,_:=catalog.DescriptorAt(view,reader,call.Citext.Ref.Address);current,ok:=descriptor.(*catalog.Procedure)
 if !ok||!exec.SameRoutineDeclaration(current,original) {return fmt.Errorf("stuntgres: missing remapped citext string declaration")}
 _,err:=validateCitextStringDeclaration(view,original,call,types);return err
}
func currentCitextString(view catalog.Read,call functions.String) (*catalog.Procedure,error) {
 original,ok:=call.RoutineReference.(*catalog.Procedure);if !ok {return nil,fmt.Errorf("stuntgres: invalid citext string original")}
 if err:=validateCapturedCitextString(call,original);err!=nil {return nil,err}
 return validateCitextStringDeclaration(view,original,call,func(t *sqltype.Descriptor)*sqltype.Descriptor{return t})
}
