package xml

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// Read original licensed Unicode data independently of the candidate generator.
// JIS0212 has a two-column format, unlike JIS0208's SJIS/JIS/Unicode format.
func rawJIS0212Source(t *testing.T) map[uint16]rune {
	t.Helper()
	data,err:=os.ReadFile("data/JIS0212.TXT");if err!=nil { t.Fatal(err) }
	values:=map[uint16]rune{}
	previous:=uint64(0)
	for _,line:=range strings.Split(string(data),"\n") {
		if strings.HasPrefix(line,"#") || strings.TrimSpace(line)=="" { continue }
		fields:=strings.Fields(line)
		if len(fields)<3 || fields[2]!="#" { t.Fatal("malformed original JIS0212 row",line) }
		cell,e1:=strconv.ParseUint(fields[0],0,16);scalar,e2:=strconv.ParseUint(fields[1],0,16)
		if e1!=nil || e2!=nil || cell<=previous || cell>>8<0x21 || cell>>8>0x7e || cell&255<0x21 || cell&255>0x7e || scalar==0 || scalar>=0xd800 && scalar<=0xdfff || values[uint16(cell)]!=0 { t.Fatal("corrupt original JIS0212 row",line) }
		values[uint16(cell)]=rune(scalar);previous=cell
	}
	if len(values)!=6067 || previous!=0x6d63 || values[0x222f]!=0x02d8 || values[0x2237]!=0x007e || values[0x2243]!=0x00a6 || values[0x2b31]!=0x00e9 || values[0x6d63]!=0x9fa5 || values[0x2121]!=0 { t.Fatal("original JIS0212 inventory/literal drift") }
	// The pinned reader's sole JIS0212 correction is also independently
	// visible in glibc2.36 jis0212.c: first interval starts at 006C (222F),
	// index 0; its ninth entry, 2237, is FF5E. Assert the original 007E above
	// before applying this factual correction; never modify the licensed input.
	values[0x2237]=0xff5e
	return values
}

// Interpret the original PostgreSQL release radix input, never LookupUTF8 or
// generated Go tables. Only the one/two-byte source descriptors needed here are
// read; this test-only independent reader is not a second production converter.
func rawJP2ReleasedMap(t *testing.T, name string, width int) map[uint16]rune {
	t.Helper()
	path:="../sqltype/pgencoding18/data/src/backend/utils/mb/Unicode/"+name+"_to_utf8.map"
	data,err:=os.ReadFile(path);if err!=nil { t.Fatal(err) }
	clean:=regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(data),"")
	tree:=regexp.MustCompile(`(?s)static const pg_mb_radix_tree\s+\w+\s*=\s*\{(.*?)\};`).FindStringSubmatch(clean)
	array:=regexp.MustCompile(`(?s)static const uint(?:16|32)\s+\w+\[\d+\]\s*=\s*\{(.*?)\};`).FindStringSubmatch(clean)
	if len(tree)!=2 || len(array)!=2 { t.Fatal("unsupported released radix source",path) }
	numbers:=regexp.MustCompile(`0x[0-9a-fA-F]+|\b[0-9]+\b`)
	parse:=func(s string) []uint32 {
		var result []uint32
		for _,token:=range numbers.FindAllString(s,-1) { v,e:=strconv.ParseUint(token,0,32);if e!=nil { t.Fatal(e) };result=append(result,uint32(v)) }
		return result
	}
	descriptor:=parse(tree[1]);table:=parse(array[1])
	if len(descriptor)!=24 { t.Fatal("released descriptor extent",len(descriptor)) }
	start:=0
	if width==2 { start=3 }
	root:=descriptor[start];bounds:=descriptor[start+1:start+1+2*width]
	values:=map[uint16]rune{}
	for first:=int(bounds[0]);first<=int(bounds[1]);first++ {
		lastLo,lastHi:=0,0
		if width==2 { lastLo,lastHi=int(bounds[2]),int(bounds[3]) }
		for second:=lastLo;second<=lastHi;second++ {
			at:=int(root)+first-int(bounds[0])
			if at>=len(table) { t.Fatal("released radix root out of range") }
			word:=table[at];cell:=uint16(first)
			if width==2 {
				at=int(word)+second-lastLo
				if at>=len(table) { t.Fatal("released radix child out of range") }
				word=table[at];cell=uint16(first<<8|second)
			}
			if word==0 { continue }
			var spelling []byte
			for shift:=24;shift>=0;shift-=8 { value:=byte(word>>shift);if value!=0 { spelling=append(spelling,value) } }
			r,n:=utf8.DecodeRune(spelling)
			if r==utf8.RuneError || n!=len(spelling) { t.Fatalf("released non-scalar mapping %x",spelling) }
			values[cell]=r
		}
	}
	return values
}

func TestXMLRawJP2Mappings(t *testing.T) {
	label:="ISO-2022-JP-2"
	jis0208,jis0212:=rawJISSource(t),rawJIS0212Source(t)
	gb,ksc,greek:=rawJP2ReleasedMap(t,"euc_cn",2),rawJP2ReleasedMap(t,"euc_kr",2),rawJP2ReleasedMap(t,"iso8859_7",1)
	for _,tc:=range []struct { name,designation string; values map[uint16]rune; assigned int }{
		{"jis0208-at","\x1b$@",jis0208,6879}, {"jis0208-b","\x1b$B",jis0208,6879},
		{"jis0212","\x1b$(D",jis0212,6067}, {"gb2312","\x1b$A",gb,7445}, {"ksc5601","\x1b$(C",ksc,8227},
	} {
		t.Run(tc.name,func(t *testing.T) {
			assigned,holes:=0,0
			for lead:=0x21;lead<=0x7e;lead++ { for trail:=0x21;trail<=0x7e;trail++ {
				cell:=uint16(lead<<8|trail);sourceCell:=cell
				if tc.name=="gb2312" || tc.name=="ksc5601" { sourceCell|=0x8080 }
				want:=tc.values[sourceCell]
				if tc.name=="ksc5601" && (lead==0x49 || lead==0x7e) { want=0 }
				if tc.name=="jis0212" && jis0212Rune(byte(lead),byte(trail))!=want { t.Fatalf("JIS0212 immutable cell %04X differs from original qualified source",cell) }
				input:=rawDocument(label,tc.designation+string([]byte{byte(lead),byte(trail)})+"\x1b(B")
				if want==0 { holes++;testRawJP2MappingFailure(t,input,len(tc.designation));continue }
				assigned++
				rawAccept(t,input,rawDocument(label,string(want)),string(want),RawInputInfo{Codec:label},rawDecl(label))
			} }
			if assigned!=tc.assigned || assigned+holes!=8836 { t.Fatalf("independent %s inventory assigned %d holes %d",tc.name,assigned,holes) }
		})
	}
	for _,tc:=range []struct { designation,bytes,text string }{
		{"\x1b$(D","\x22\x37","\uff5e"}, {"\x1b$(D","\x22\x43","¦"},
		{"\x1b$A","\x21\x24","・"}, {"\x1b$A","\x21\x2a","―"},
		{"\x1b$(C","\x22\x66","€"}, {"\x1b$(C","\x22\x67","®"}, {"\x1b$(C","\x22\x68","㉾"},
	} { rawAccept(t,rawDocument(label,tc.designation+tc.bytes+"\x1b(B"),rawDocument(label,tc.text),tc.text,RawInputInfo{Codec:label},rawDecl(label)) }
	for _,tc:=range []struct { name,designation string }{{"ascii","\x1b(B"},{"roman","\x1b(J"},{"kana","\x1b(I"}} {
		t.Run(tc.name,func(t *testing.T) {
			for value:=range 128 {
				r:=rune(value);mapped:=true
				if tc.name=="roman" { if value==0x5c { r='¥' };if value==0x7e { r='‾' } }
				if tc.name=="kana" && value>=0x21 && value!=0x7f { if value<=0x5f { r=rune(0xff61+value-0x21) } else { mapped=false } }
				body:=tc.designation+string([]byte{byte(value)})+"\x1b(B"
				testRawJP2Scalar(t,body,r,mapped)
			}
		})
	}
	for _,tc:=range []struct { name,designation string }{{"latin","\x1b.A"},{"greek","\x1b.F"}} {
		t.Run(tc.name,func(t *testing.T) {
			for value:=range 256 {
				r:=rune(value|0x80);mapped:=true
				if tc.name=="greek" { r=greek[uint16(value|0x80)];mapped=value>=0x20 && value<0x80 && r!=0 }
				testRawJP2Scalar(t,tc.designation+"\x1bN"+string([]byte{byte(value)}),r,mapped)
			}
		})
	}
}

func testRawJP2Scalar(t *testing.T,body string,r rune,mapped bool) {
	t.Helper()
	label:="ISO-2022-JP-2"
	// CDATA makes raw '<' and '&' real scalar witnesses, not entity spelling.
	input:=rawHeader(label)+"<r><![CDATA["+body+"]]></r>"
	if !mapped { testRawJP2MappingFailure(t,rawDocument(label,body),3);return }
	if r==0 || r<0x20 && r!=9 && r!=10 && r!=13 {
		var pe *ParseError
		if err:=rawAtomicFailure(t,input);!errors.As(err,&pe) { t.Fatalf("mapped XML-invalid scalar %U: %v",r,err) }
		return
	}
	text:=string(r);if r==13 { text="\n" }
	decoded:=rawHeader(label)+"<r><![CDATA["+string(r)+"]]></r>"
	for _,collect:=range []bool{false,true} {
		result,tree,err:=parse(context.Background(),input,rawOptions,collect)
		if err!=nil || !result.Valid() || result.Original()!=decoded || result.RawInput()!=(RawInputInfo{Codec:label}) || result.Declaration()!=rawDecl(label) || collect && rawText(rawRoot(tree))!=text || !collect && tree!=nil { t.Fatal(fmt.Sprintf("single scalar %x want %U collect %t",body,r,collect),result,tree,err) }
	}
}

func testRawJP2MappingFailure(t *testing.T,input string,designationBytes int) {
	t.Helper()
	label:="ISO-2022-JP-2"
	prefix:=rawHeader(label)+"<r>"
	at:=len(prefix)+designationBytes
	var re *RawInputError
	err:=rawAtomicFailure(t,input)
	bytes:=input[at:at+min(4,len(input)-at)]
	if !errors.As(err,&re) || re.Kind!=RawInvalidSequence || re.Label!=label || re.Codec!=label || re.RawOffset!=at || re.Bytes!=bytes || re.Declaration!=rawDecl(label) || re.Cause==nil { t.Fatalf("source-backed hole %x: %+v",input,err) }
	preview:="input conversion failed due to input error, bytes"
	for _,b:=range []byte(bytes) { preview+=fmt.Sprintf(" 0x%02X",b) }
	if len(bytes)<4 { preview+=" 0x00" }
	want:=[]Diagnostic{
		{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:preview},
		{Condition:"XML_I18N_CONV_FAILED",Domain:EncodingDomain,Severity:Fatal,Message:preview},
		{Condition:"XML_IO_ENCODER",Domain:IODomain,Severity:Error,Message:"encoder error"},
		rawExpectedDiagnosticAtBase(prefix,len(rawHeader(label))-2,"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag r line 1",len(prefix),len(prefix)+1),
	}
	assertRawDiagnostics(t,re.Diagnostics,want,re.Detail())
}
