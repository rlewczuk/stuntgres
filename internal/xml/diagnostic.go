package xml

import (
 "fmt"
 "strings"
)

type Condition string
type Domain string
type Severity uint8
const ( Warning Severity = iota+1; Error; Fatal )
const ( ParserDomain Domain = "parser"; NamespaceDomain Domain = "namespace"; DTDValidityDomain Domain = "dtd" )
type DiagnosticSource uint8
const (OriginalSource DiagnosticSource=iota; EntitySource)
type Diagnostic struct {
 Condition Condition
 Domain Domain
 Severity Severity
 Line, Column, Offset, SourceBaseOffset int
 Message, SourceContext, Caret string
 Source DiagnosticSource
 EntityName string
}
func (d Diagnostic) String() string { return fmt.Sprintf("line %d: %s\n%s\n%s", d.Line,d.Message,d.SourceContext,d.Caret) }
type ErrorCategory uint8
const ( GrammarError ErrorCategory = iota; DeclarationError )
type ParseError struct {
 Category ErrorCategory
 RequestedMode, EffectiveMode Mode
 Declaration Declaration
 SourceBaseOffset int
 Diagnostics []Diagnostic
 DeclarationDetail string
}
func (e *ParseError) Error() string {
 if e.Category == DeclarationError { return "invalid XML content: invalid XML declaration" }
 if e.RequestedMode == Document { return "invalid XML document" }; return "invalid XML content"
}
func (e *ParseError) Detail() string {
 if e.Category == DeclarationError { return e.DeclarationDetail }
 var b strings.Builder
 for _,d := range e.Diagnostics { if d.Severity >= Error { if b.Len()!=0 { b.WriteByte('\n') }; b.WriteString(d.String()) } }
 return b.String()
}

// fileContext follows libxml2 error.c's byte window, including split UTF8.
// Structured character columns are deliberately independent of this caret.
func fileContext(s string, base, at int) (string,string) {
 at = min(at,len(s)); q:=at
 for q>base && q<len(s) && (s[q]=='\n'||s[q]=='\r') { q-- }
 n:=0
 for q>base && n<80 && (q==len(s) || s[q]!='\n' && s[q]!='\r') { q--; n++ }
 if q<len(s) && (s[q]=='\n'||s[q]=='\r') { q++ }
 end:=q
 for end<len(s) && end-q<80 && s[end]!=0 && s[end]!='\n' && s[end]!='\r' { end++ }
 var caret [80]byte
 count:=min(at-q,79,end-q)
 for i:=range count { caret[i]=' '; if s[q+i]=='\t' { caret[i]='\t' } }
 caret[count]='^'
 return s[q:end],string(caret[:count+1])
}
func (p *parser) event(code Condition, message string) { p.eventLevel(code,message,Fatal) }
func (p *parser) eventLevel(code Condition, message string, level Severity) {
 if p.err!=nil { return }
 if level==Fatal{p.saxDisabled=true}
 if code=="XML_WAR_UNDECLARED_ENTITY"{p.lastEntityError=code;return}
 if code=="XML_ERR_NOT_WELL_BALANCED"&&(p.illFormed||p.priorError){p.lastEntityError=code;return}
 context,caret:="","^"
 if !p.haltedInput {context,caret=fileContext(p.s,p.base,p.i)}
 // Bound the eventual rendered Detail as well as event-slice storage before
 // addition. Fixed context/line overhead cannot overflow intermediate sums.
 needed:=len(message)
 if !appendSize(needed,len(context)+len(caret)+64) {p.err=appendSizeError(needed,len(context)+len(caret)+64);return}
 needed+=len(context)+len(caret)+64
 if !appendSize(p.diagnosticBytes,needed) {p.err=appendSizeError(p.diagnosticBytes,needed);return}
 p.diagnosticBytes+=needed
 if level>=Error {p.illFormed=true}
 p.lastEntityError=code
 p.events=checkedAppend(p,p.events,Diagnostic{Condition:code, Domain:ParserDomain, Severity:level, Line:p.line, Column:p.column, Offset:p.i, SourceBaseOffset:p.base, Message:message,SourceContext:context,Caret:caret,Source:p.source,EntityName:p.entityName})
}
func (p *parser) failed() bool {return p.illFormed||p.saxDisabled}
func (p *parser) parseError() *ParseError { return &ParseError{Category:GrammarError,RequestedMode:p.requested,EffectiveMode:p.effective,Declaration:p.declaration,SourceBaseOffset:p.base,Diagnostics:p.events} }

// joinMessage bounds copies of potentially long written names/version labels.
// It leaves cancellation/size errors ordinary and publishes no partial event.
func (p *parser) joinMessage(parts ...string) string {
 if p.err!=nil{return ""}
 total:=0
 for at,s:=range parts {if at&255==0{p.err=p.ctx.Err();if p.err!=nil{return ""}};if !appendSize(total,len(s)){p.err=appendSizeError(total,len(s));return ""};total+=len(s)}
 p.err=p.ctx.Err();if p.err!=nil{return ""}
 var b strings.Builder;b.Grow(total)
 for _,s:=range parts {
  for len(s)>0 {
   if err:=p.ctx.Err();err!=nil {p.err=err;return ""}
   n:=min(4096,len(s));b.WriteString(s[:n]);s=s[n:]
  }
 }
 if err:=p.ctx.Err();err!=nil {p.err=err;return ""}
 return b.String()
}
