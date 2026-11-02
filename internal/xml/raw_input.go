package xml

import (
	"fmt"
	"strings"
	"stuntgres/internal/sqltype/pgencoding18"
)

// RawInputInfo is factual immutable transport provenance. Codec is the effective
// decoder identity, not the written alias. BufferedBytes counts only unconsumed
// physical EOF bytes, not complete designations which emitted no character.
// The zero value belongs to non-RawMemory results.
type RawInputInfo struct {
	Codec string
	Default, UTF8BOM bool
	BufferedBytes int
}

type RawInputErrorKind uint8
const (
	RawUnknownLabel RawInputErrorKind = iota
	RawUnavailableCodec
	RawInvalidSequence
	RawEmptyInput
)

// RawInputError separates capability/conversion failures from XML grammar errors.
// RawOffset is a zero-based transport-byte coordinate (including initial BOM).
// Bytes owns up to four actual bytes starting at the fault, excluding EOF guards;
// parser diagnostics instead refer to the decoded, BOM-less character spelling.
// There is deliberately no SQLState method at this package boundary.
type RawInputError struct {
	Kind RawInputErrorKind
	Label, Codec string
	RawOffset int
	Bytes string
	Declaration Declaration
	Diagnostics []Diagnostic
	Cause error
}
func (e *RawInputError) Error() string {
	switch e.Kind {
	case RawUnknownLabel: return "unsupported raw XML encoding "+e.Label
	case RawUnavailableCodec: return "raw XML decoder unavailable for "+e.Label
	case RawEmptyInput: return "empty raw XML input"
	default: return "invalid raw XML input for "+e.Codec
	}
}
func (e *RawInputError) Unwrap() error { return e.Cause }
func (e *RawInputError) Detail() string { return (&ParseError{Diagnostics:e.Diagnostics}).Detail() }

func (p *parser) startRaw() error {
	p.raw=true
	p.rawInfo=RawInputInfo{Codec:"UTF8",Default:true}
	if len(p.s)==0 { return &RawInputError{Kind:RawEmptyInput} }
	if strings.HasPrefix(p.s,"\xef\xbb\xbf") {
		p.s=p.s[3:];p.rawBOMBytes=3;p.rawInfo.UTF8BOM=true
	}
	return p.ctx.Err()
}

// Selection is called by the existing DOCUMENT production, immediately after
// its valid encoding field's closing quote. It does not reparse a header or
// discard previous declaration events. Conversion affects only unread bytes.
func (p *parser) selectRawEncoding(label string) bool {
	if p.err=p.ctx.Err();p.err!=nil{return false}
	id:=rawEncoding(label)
	if id == -1 {
		kind:=RawUnknownLabel
		// PG-only spellings explicitly rejected by the pinned raw entry must
		// not become capability successes through recognition's fuzzy lookup.
		rejected:=labelRejectedRaw(label)
		if !rejected&&recognizedEncoding(label){kind=RawUnavailableCodec}
		p.rawFailure=&RawInputError{Kind:kind,Label:label,RawOffset:p.i+p.rawBOMBytes}
		if kind==RawUnknownLabel {p.declEvent("XML_ERR_UNSUPPORTED_ENCODING",p.joinMessage("Unsupported encoding ",label))}
		p.stopped=true
		return false
	}
	p.rawInfo.Codec,p.rawInfo.Default=rawCodecName(id),false
	if id==6 {return true} // immutable UTF8 identity; grammar owns invalid UTF8
	prefix:=p.i
	original:=p.s
	decoded,status,err:=rawDecode(p.ctx,original,prefix,id)
	if err!=nil{p.err=err;return false}
	// Earlier declaration callbacks observed the pre-switch buffer. Preserve
	// those exact bounded windows without retaining its transport backing.
	for i:=range p.events {
		if p.err=p.ctx.Err();p.err!=nil{return false}
		p.events[i].SourceContext=strings.Clone(p.events[i].SourceContext)
	}
	p.s=decoded
	p.base=prefix // the converter's replacement buffer starts at the completed quote
	p.nextPoll=min(p.nextPoll,p.i)
	p.rawInfo.BufferedBytes=status.incomplete
	if status.fault>=0 {
		at:=prefix+status.fault
		p.rawFailure=&RawInputError{Kind:RawInvalidSequence,Label:decoded[prefix-len(label)-1:prefix-1],Codec:p.rawInfo.Codec,RawOffset:at+p.rawBOMBytes,Bytes:strings.Clone(original[at:at+min(4,len(original)-at)]),Cause:status.cause}
		p.rawConversionEvent(original[at:])
		// xmlSwitchEncoding's initial conversion can leave a failed input
		// buffer. The following reader fill retries that same buffer with its
		// designation unchanged; each genuinely failed attempt reports once.
		if p.retryRawBuffer(original[at:],id,status.state) {
			p.rawConversionEvent(original[at:])
			p.rawEvent(Diagnostic{Condition:"XML_IO_ENCODER",Domain:IODomain,Severity:Error,Message:"encoder error"})
		}
	}
	return p.err==nil
}

func labelRejectedRaw(label string) bool {
	var key [32]byte
	if len(label)>len(key){return false}
	for i:=range len(label){c:=label[i];if c>='a'&&c<='z'{c-='a'-'A'};key[i]=c}
	switch string(key[:len(label)]) {
	case "WIN1250","WIN1251","WIN1252","WIN1253","WIN1254","WIN1255","WIN1256","WIN1257","WIN1258","WIN866","WIN874","EUC_JIS_2004","SHIFT_JIS_2004","SQL_ASCII","MULE_INTERNAL":return true
	}
	return false
}

func (p *parser) retryRawBuffer(input string,id int,state byte) bool {
	if p.err=p.ctx.Err();p.err!=nil{return false}
	if id==rawISO2022JP {_,_,_,_,invalid:=iso2022JPStep(input,state);return invalid}
	if id==40 {
		n:=rawJOHABWidth(input);if n<0{return true}
		first,_:=pgencoding18.LookupUTF8(id,pgencoding18.Pack(input,n),n)
		return first==0
	}
	if pgencoding18.Verify(id,input)<0{return true}
	_,_,n,invalid:=pgencoding18.Step(id,6,input)
	return invalid||n==0
}
func (p *parser) rawConversionEvent(input string) {
	var b strings.Builder
	b.WriteString("input conversion failed due to input error, bytes")
	for i:=range min(4,len(input)){fmt.Fprintf(&b," 0x%02X",input[i])}
	// The reader can include its terminating NUL, but bytes beyond that are
	// allocator residue rather than input provenance. Never read or invent it.
	if len(input)<4 {b.WriteString(" 0x00")}
	p.rawEvent(Diagnostic{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:b.String()})
}
func (p *parser) rawEvent(d Diagnostic) {
	if p.err!=nil{return}
	needed:=len(d.Message)+64
	if !appendSize(p.diagnosticBytes,needed){p.err=appendSizeError(p.diagnosticBytes,needed);return}
	p.diagnosticBytes+=needed
	// Conversion events do not stop grammar continuation over the successfully
	// decoded buffer; publication remains unconditionally failure-atomic.
	p.events=checkedAppend(p,p.events,d)
}
