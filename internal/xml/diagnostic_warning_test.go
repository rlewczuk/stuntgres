package xml

import (
 "context"
 "errors"
 "fmt"
 "slices"
 "strings"
 "testing"
)

// Independent NoticeReceiver captures859/864/868 precede these expectations.
// PostgreSQL WARNING primary contains the rendered block, not an ErrorResponse
// Detail. Conditions, domains, severities, offsets/columns/base follow matching
// libxml2/PostgreSQL source; package publication/ownership is a provider contract.
type parserWarningWitness struct {
 name,input string
 content,document []Diagnostic
 binaryOnly bool
 declaration Declaration
 contentBase int
 output string
 contentDeclarationError bool
}

func checkParserWarningWitness(t *testing.T,tc parserWarningWitness) {
 t.Helper()
 for _,mode:=range []Mode{Content,Document} {
  want:=tc.content;if mode==Document {want=tc.document}
  kinds:=[]InputKind{Text,Binary};if tc.binaryOnly {kinds=[]InputKind{Binary}}
  for _,kind:=range kinds {
   t.Run(fmt.Sprintf("%s/%d/%d",tc.name,mode,kind),func(t *testing.T){
    ctx:=context.Background()
    result,err:=Parse(ctx,tc.input,Options{Mode:mode,InputKind:kind})
    if mode==Content&&tc.contentDeclarationError {
     var failure *ParseError
     if !errors.As(err,&failure)||result.valid||result.original!=""||result.requested!=0||result.effective!=0||result.declaration!=(Declaration{})||result.diagnostics!=nil||failure.Error()!="invalid XML content: invalid XML declaration"||failure.Category!=DeclarationError||failure.RequestedMode!=mode||failure.EffectiveMode!=mode||failure.SourceBaseOffset!=0||failure.Declaration!=(Declaration{Present:true})||len(failure.Diagnostics)!=0||failure.Detail()!="Space required." {t.Fatalf("custom declaration %+v result%+v error%v",failure,result,err)}
     if out,w,err:=OutputText(ctx,result);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("failed declaration text%q error%v",out,err)}
     if out,w,err:=OutputBinary(ctx,result,6);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("failed declaration binary%q error%v",out,err)}
     accept(t,"<healthy/>",mode);return
    }
    failed:=false;blocks:=[]string{}
    for _,d:=range want {
     if d.Severity>=Error {failed=true;blocks=append(blocks,fmt.Sprintf("line %d: %s\n%s\n%s",d.Line,d.Message,d.SourceContext,d.Caret))}
    }
    if failed {
     var failure *ParseError
     if !errors.As(err,&failure)||result.valid||result.original!=""||result.requested!=0||result.effective!=0||result.declaration!=(Declaration{})||result.diagnostics!=nil {t.Fatalf("failed publication %+v error%v",result,err)}
     primary:="invalid XML content";base:=tc.contentBase;if mode==Document {primary="invalid XML document";base=0}
     if failure.Error()!=primary||failure.Category!=GrammarError||failure.RequestedMode!=mode||failure.EffectiveMode!=mode||failure.SourceBaseOffset!=base||failure.Declaration!=tc.declaration||!slices.Equal(failure.Diagnostics,want)||failure.Detail()!=strings.Join(blocks,"\n") {t.Fatalf("error %+v diagnostics%+v want%+v detail%q want%q",failure,failure.Diagnostics,want,failure.Detail(),strings.Join(blocks,"\n"))}
     if out,w,err:=OutputText(ctx,result);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("failed text%q error%v",out,err)}
     if out,w,err:=OutputBinary(ctx,result,6);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("failed binary%q error%v",out,err)}
    }else{
     if err!=nil||!result.valid||result.Original()!=tc.input||result.RequestedMode()!=mode||result.EffectiveMode()!=mode||result.Declaration()!=tc.declaration||!slices.Equal(result.Diagnostics(),want) {t.Fatalf("accepted %+v error%v diagnostics%+v want%+v",result,err,result.Diagnostics(),want)}
     output:=tc.output;if output=="" {output=tc.input}
     if out,warning,err:=OutputText(ctx,result);err!=nil||out!=output||warning!=(OutputWarning{}) {t.Fatalf("text%q want%q warning%+v error%v",out,output,warning,err)}
     if out,warning,err:=OutputBinary(ctx,result,6);err!=nil||out!=output||warning!=(OutputWarning{}) {t.Fatalf("binary%q want%q warning%+v error%v",out,output,warning,err)}
     copied:=result.Diagnostics()
     if len(copied)>0 {copied[0].Message="mutated";copied=append(copied,Diagnostic{})}
     if !slices.Equal(result.Diagnostics(),want) {t.Fatal("borrowed warning storage")}
    }
    actual:=result.Diagnostics()
    var failure *ParseError;if errors.As(err,&failure) {actual=failure.Diagnostics}
    for i,d:=range want {
     block:=fmt.Sprintf("line %d: %s\n%s\n%s",d.Line,d.Message,d.SourceContext,d.Caret)
     if actual[i].String()!=block {t.Fatalf("diagnostic block%d %q want%q",i,actual[i].String(),block)}
    }
    healthy:=accept(t,"<healthy>é</healthy>",mode)
    if len(healthy.Diagnostics())!=0 {t.Fatal("warning state leaked into healthy call")}
    if !failed&&(!slices.Equal(result.Diagnostics(),want)||result.Original()!=tc.input) {t.Fatal("retained warning result changed after reuse")}
   })
  }
 }
}
