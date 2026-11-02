package xml

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Independent test reader: the vendored Unicode file, not a production decoder,
// generated table, PostgreSQL EUC_JP mapping or runtime host codec, is authority.
func rawJISSource(t *testing.T) map[uint16]rune {
	t.Helper()
	f,err:=os.Open("data/JIS0208.TXT");if err!=nil{t.Fatal(err)};defer f.Close()
	values:=map[uint16]rune{}
	scan:=bufio.NewScanner(f)
	for scan.Scan(){
		line:=scan.Text();if strings.HasPrefix(line,"#")||strings.TrimSpace(line)==""{continue}
		fields:=strings.Fields(line)
		if len(fields)<4||fields[3]!="#"{t.Fatal("malformed independent source row",line)}
		cell,e1:=strconv.ParseUint(fields[1],0,16);scalar,e2:=strconv.ParseUint(fields[2],0,16)
		if e1!=nil||e2!=nil||values[uint16(cell)]!=0{t.Fatal("corrupt independent source",line)}
		values[uint16(cell)]=rune(scalar)
	}
	if err:=scan.Err();err!=nil{t.Fatal(err)}
	if len(values)!=6879||values[0x2140]!=0x005c{t.Fatal("original source inventory/correction drift")}
	values[0x2140]=0xff3c // the sole independently observed raw-reader correction
	return values
}

func TestXMLRawISO2022JP(t *testing.T){
	label:="ISO-2022-JP"
	for _,tc:=range []struct{bytes,text string}{
		{"A","A"},{"\x1b$@$\"\x1b(B","あ"},{"\x1b$B$\"\x1b(B","あ"},
		{"\x1b$B$\"\x1b$B$$\x1b(J\\~\x1b(BZ","あい¥‾Z"},
		{"\x1b(J\\~\x1b(B\\~","¥‾\\~"},
		{"\x1b$B$\"\n$$\x1b(B","あ\nい"},
		{"\x1b$B  \t\n\x1b(B","  \t\n"},
		{"\x1b$B\r\x1b(B","\n"},{"\x1b$B\x7f\x1b(B","\x7f"},
		{strings.Repeat("a",100),strings.Repeat("a",100)},
	}{
		decoded:=tc.text
		if strings.Contains(tc.bytes,"\r"){decoded="\r"} // Original precedes XML line normalization
		rawAccept(t,rawDocument(label,tc.bytes),rawDocument(label,decoded),tc.text,RawInputInfo{Codec:label},rawDecl(label))
	}
	for _,tail:=range []struct{bytes string;buffered int}{{"\x1b(B",0},{"\x1b$B",0},{"\x1b",1},{"\x1b$",2},{"\x1b(",2},{"\x1b$B$",1}}{
		rawAccept(t,rawHeader(label)+"<r/>"+tail.bytes,rawHeader(label)+"<r/>","",RawInputInfo{Codec:label,BufferedBytes:tail.buffered},rawDecl(label))
	}
	for _,tc:=range []struct{cell uint16;r rune}{{0x2121,0x3000},{0x2140,0xff3c},{0x2141,0x301c},{0x2142,0x2016},{0x215d,0x2212},{0x2171,0xa2},{0x2172,0xa3},{0x224c,0xac},{0x7426,0x7199}}{
		body:="\x1b$B"+string([]byte{byte(tc.cell>>8),byte(tc.cell)})+"\x1b(B"
		rawAccept(t,rawDocument(label,body),rawDocument(label,string(tc.r)),string(tc.r),RawInputInfo{Codec:label},rawDecl(label))
	}
	t.Run("all8836",func(t *testing.T){
		values:=rawJISSource(t);assigned,holes:=0,0
		for lead:=0x21;lead<=0x7e;lead++{for trail:=0x21;trail<=0x7e;trail++{
			cell:=uint16(lead<<8|trail);want:=values[cell]
			if got:=jis0208Rune(byte(lead),byte(trail));got!=want{t.Fatalf("table %04X got %U want %U",cell,got,want)}
			pair:=string([]byte{byte(lead),byte(trail)})
			input:=rawDocument(label,"\x1b$B"+pair+"\x1b(B")
			if want!=0{assigned++;rawAccept(t,input,rawDocument(label,string(want)),string(want),RawInputInfo{Codec:label},rawDecl(label));continue}
			holes++
			err:=rawAtomicFailure(t,input);var re *RawInputError
			at:=len(rawHeader(label))+6
			if !errors.As(err,&re)||re.Kind!=RawInvalidSequence||re.Label!=label||re.Codec!=label||re.RawOffset!=at||re.Bytes!=pair+"\x1b("||re.Declaration!=rawDecl(label)||re.Cause==nil||re.Cause.Error()!="invalid byte sequence for ISO-2022-JP at byte 8"{t.Fatalf("hole %04X: %+v",cell,err)}
			message:=fmt.Sprintf("input conversion failed due to input error, bytes 0x%02X 0x%02X 0x1B 0x28",lead,trail)
			prefix:=rawHeader(label)+"<r>"
			wantEvents:=[]Diagnostic{
				{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:message},
				{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:message},
				{Condition:"XML_IO_ENCODER",Domain:IODomain,Severity:Error,Message:"encoder error"},
				rawExpectedDiagnosticAtBase(prefix,len(rawHeader(label))-2,"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag r line 1",len(prefix),len(prefix)+1),
			}
			assertRawDiagnostics(t,re.Diagnostics,wantEvents,re.Detail())
		}}
		if assigned!=6879||holes!=1957{t.Fatal(assigned,holes)}
	})
	for _,cell:=range []uint16{0x222f,0x7427,0x7521,0x7e7e,0x2d21}{if jis0208Rune(byte(cell>>8),byte(cell))!=0{t.Fatalf("extension/hole %04X admitted",cell)}}
	// An accepted physical EOF suffix is accounted, never arbitrary truncation:
	// appending a real invalid byte makes the same prefix fail atomically.
	for _,tail:=range []string{"\x1b","\x1b$","\x1b$B$"}{rawAtomicFailure(t,rawHeader(label)+"<r/>"+tail+"\x80")}
	testRawISOIllegalTail(t)
	if !reflect.DeepEqual((Result{}).RawInput(),RawInputInfo{}){t.Fatal("zero provenance")}
}

func testRawISOIllegalTail(t *testing.T) {
	t.Helper()
	// ESC+illegal80 is not a genuine partial designation. ESC falls through
	// literally and the following byte is a conversion fault, never buffering.
	r,n,state,incomplete,invalid:=iso2022JPStep("\x1b\x80",jisASCII)
	if r!=0x1b||n!=1||state!=jisASCII||incomplete||invalid{t.Fatal("illegal byte hidden in incomplete escape",r,n,state,incomplete,invalid)}
	label:="ISO-2022-JP"
	prefix:=rawHeader(label)+"<r/>\x1b"
	err:=rawAtomicFailure(t,prefix+"\x80");var re *RawInputError
	if !errors.As(err,&re)||re.Kind!=RawInvalidSequence||re.Label!=label||re.Codec!=label||re.RawOffset!=49||re.Bytes!="\x80"||re.Declaration!=rawDecl(label)||re.Cause==nil||re.Cause.Error()!="invalid byte sequence for ISO-2022-JP at byte 7"{t.Fatalf("illegal EOF escape: %+v",err)}
	message:="input conversion failed due to input error, bytes 0x80 0x00"
	want:=[]Diagnostic{
		{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:message},
		{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:message},
		{Condition:"XML_IO_ENCODER",Domain:IODomain,Severity:Error,Message:"encoder error"},
		rawExpectedDiagnosticAtBase(prefix,42,"XML_ERR_DOCUMENT_END","Extra content at the end of the document",48,49),
	}
	assertRawDiagnostics(t,re.Diagnostics,want,re.Detail())
}

func TestXMLRawISOReplay(t *testing.T) {
	for _,tc:=range []struct{input,want string;consumed,fault,incomplete int;state byte}{
		{"\x1b\x80","\x1b",1,1,0,jisASCII},
		{"A\x1b\x80","A\x1b",2,2,0,jisASCII},
		{"A\x1b$\x80","A\x1b$",3,3,0,jisASCII},
		{"A\x1bX\x80","A\x1bX",3,3,0,jisASCII},
		{"A\x1b$B$\"\x80","Aあ",6,6,0,jisDouble},
		{"A\x1b","A",1,-1,1,jisASCII},
		{"A\x1b$","A",1,-1,2,jisASCII},
		{"A\x1b(","A",1,-1,2,jisASCII},
		{"A\x1b$B$","A",4,-1,1,jisASCII},
		{"A\x1b$B$\"","Aあ",6,-1,0,jisASCII},
		{"A\x1b$B$\"\x1b","Aあ",6,-1,1,jisASCII},
	}{
		decoded,status,err:=rawDecode(context.Background(),tc.input,0,rawISO2022JP)
		if err!=nil||decoded!=tc.want||status.size!=len(tc.want)||status.consumed!=tc.consumed||status.fault!=tc.fault||status.incomplete!=tc.incomplete||status.state!=tc.state{t.Fatalf("replay %q: decoded %q status %+v error %v",tc.input,decoded,status,err)}
		if (status.cause!=nil)!=(tc.fault>=0){t.Fatalf("replay cause %q: %v",tc.input,status.cause)}
		// Replaying only the consumption limit must still inspect the full
		// physical input. It neither retries the fault nor buffers a scalar
		// already emitted by the sizing pass, and starts in ASCII each time.
		var out strings.Builder
		emitted,e:=iso2022JPPass(context.Background(),tc.input,tc.consumed,&out)
		if e!=nil||out.String()!=tc.want||emitted.size!=len(tc.want)||emitted.consumed!=tc.consumed||emitted.fault!=-1||emitted.incomplete!=0||emitted.cause!=nil{t.Fatalf("bounded replay %q: %q %+v %v",tc.input,out.String(),emitted,e)}
	}
	// Real entrypoints preserve that literal ESC for the parser's epilog
	// diagnostic while returning the original conversion fault atomically.
	testRawISOIllegalTail(t)
}
