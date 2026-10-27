// Package xml validates XML character values without retaining a document tree.
package xml

import (
 "context"
 "fmt"
)

type Mode uint8
const ( Content Mode = iota; Document )
type InputKind uint8
const ( Text InputKind = iota; Binary )
type Options struct { Mode Mode; InputKind InputKind }
type Standalone uint8
const ( Absent Standalone = iota; No; Yes )
type Declaration struct {
 Present bool
 Version string
 Encoding string
 EncodingPresent bool
 Standalone Standalone
 End int
}

// Result contains only immutable character spelling and small parsing metadata.
// A zero Result is not an accepted empty CONTENT value.
type Result struct {
 original string
 requested, effective Mode
 declaration Declaration
 diagnostics []Diagnostic
 valid bool
}
func (r Result) Original() string { return r.original }
func (r Result) RequestedMode() Mode { return r.requested }
func (r Result) EffectiveMode() Mode { return r.effective }
func (r Result) Declaration() Declaration { return r.declaration }
func (r Result) Diagnostics() []Diagnostic { return append([]Diagnostic(nil), r.diagnostics...) }

func Parse(ctx context.Context, input string, opts Options) (Result, error) {
 if err := ctx.Err(); err != nil { return Result{}, err }
 if opts.Mode > Document || opts.InputKind > Binary { return Result{}, fmt.Errorf("invalid XML options") }
 if opts.InputKind == Binary {
  d, _, err := readContentDeclaration(ctx, input)
  if err != nil { return Result{}, err }
  id := 6
  if d.EncodingPresent {
   id = encodingID(d.Encoding)
   if id < 0 {
    messageParser:=parser{ctx:ctx}
    message:=messageParser.joinMessage("invalid encoding name \"",d.Encoding,"\"")
    if messageParser.err!=nil{return Result{},messageParser.err}
    return Result{}, &EncodingError{Kind:InvalidEncodingName,Label:d.Encoding,SourceID:-1,DestinationID:6,DestinationName:"UTF8",Message:message}
   }
  }
  var errConvert error
  input, errConvert = convert(ctx, input, id, 6)
  if errConvert != nil { return Result{}, errConvert }
 }
 p := parser{ctx:ctx, s:input, requested:opts.Mode, effective:opts.Mode, line:1, column:1}
 if opts.Mode == Content {
  d, detail, err := readContentDeclaration(ctx,input)
  if err != nil { return Result{}, err }
  p.declaration = d
  if detail != "" { return Result{}, &ParseError{Category:DeclarationError, RequestedMode:opts.Mode, EffectiveMode:opts.Mode, Declaration:d, DeclarationDetail:detail} }
  p.base, p.i = d.End, d.End
  p.fragmentInput=true;p.windowBase=d.End
 } else { p.documentDeclaration() }
 if p.err == nil { p.top() }
 if p.err != nil { return Result{}, p.err }
 if err := ctx.Err(); err != nil { return Result{}, err }
 if p.failed() { return Result{}, p.parseError() }
 return Result{original:input, requested:opts.Mode, effective:p.effective, declaration:p.declaration, diagnostics:p.events, valid:true}, nil
}

type Capability string
const DTD Capability = "DTD"
type UnsupportedCapabilityError struct {
 Capability Capability
 Offset int
 RequestedMode, EffectiveMode Mode
}
func (e *UnsupportedCapabilityError) Error() string { return "XML capability not implemented: " + string(e.Capability) }
