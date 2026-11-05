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

// Interpret original PostgreSQL release radix inputs, never LookupUTF8 or
// generated Go tables. Validate all four descriptor depths, including the
// four-byte EUC_TW plane keys; callers still own raw-handler framing.
func rawJP2ReleasedMap(t *testing.T, name string, width int) map[uint32]rune {
	t.Helper()
	if width < 1 || width > 4 { t.Fatal("released radix width", width) }
	path := "../sqltype/pgencoding18/data/src/backend/utils/mb/Unicode/" + name + "_to_utf8.map"
	data, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	clean := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(data), "")
	trees := regexp.MustCompile(`(?s)static const pg_mb_radix_tree\s+\w+\s*=\s*\{(.*?)\};`).FindAllStringSubmatch(clean, -1)
	arrays := regexp.MustCompile(`(?s)static const uint(16|32)\s+(\w+)\[(\d+)\]\s*=\s*\{(.*?)\};`).FindAllStringSubmatch(clean, -1)
	if len(trees) != 1 || len(arrays) != 1 { t.Fatal("unsupported released radix source", path) }
	parse := func(s string, bits int) []uint32 {
		var result []uint32
		fields := strings.Split(s, ",")
		for i, field := range fields {
			token := strings.TrimSpace(field)
			if token == "" && i == len(fields)-1 { continue }
			v, e := strconv.ParseUint(token, 0, bits)
			if e != nil { t.Fatal("released radix integer", path, token, e) }
			result = append(result, uint32(v))
		}
		return result
	}
	bits, e1 := strconv.Atoi(arrays[0][1])
	extent, e2 := strconv.Atoi(arrays[0][3])
	fields := strings.Split(strings.TrimSpace(trees[0][1]), ",")
	if len(fields) != 26 { t.Fatal("released radix descriptor fields", path) }
	pointer16, pointer32 := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
	if bits == 16 && (pointer16 != arrays[0][2] || pointer32 != "NULL") || bits == 32 && (pointer16 != "NULL" || pointer32 != arrays[0][2]) {
		t.Fatal("released radix array pointer", path)
	}
	descriptor := parse(strings.Join(fields[2:], ","), 32)
	table := parse(arrays[0][4], bits)
	if e1 != nil || e2 != nil || len(descriptor) != 24 || len(table) != extent || extent == 0 {
		t.Fatal("released radix descriptor/array extent", path)
	}
	values := map[uint32]rune{}
	for depth, start := range []int{0, 3, 8, 15} {
		n := depth + 1
		root := descriptor[start]
		bounds := descriptor[start+1:start+1+2*n]
		for i := range n {
			if bounds[2*i] > bounds[2*i+1] || bounds[2*i+1] > 255 { t.Fatal("released radix byte bounds", path, n) }
		}
		var walk func(uint32, int, uint32)
		walk = func(base uint32, level int, key uint32) {
			lo, hi := bounds[2*level], bounds[2*level+1]
			if uint64(base)+uint64(hi-lo) >= uint64(len(table)) { t.Fatal("released radix reachable index", path, n, base, level) }
			for b := lo; b <= hi; b++ {
				word := table[base+b-lo]
				cell := key<<8 | b
				if word == 0 { continue }
				if level+1 < n { walk(word, level+1, cell); continue }
				var spelling []byte
				started := false
				for shift := 24; shift >= 0; shift -= 8 {
					value := byte(word >> shift)
					if value != 0 || started { spelling = append(spelling, value); started = true }
				}
				r, size := utf8.DecodeRune(spelling)
				if !utf8.Valid(spelling) || size != len(spelling) || r == 0 { t.Fatalf("released non-scalar mapping %x in %s", spelling, path) }
				if n == width {
					if _, exists := values[cell]; exists { t.Fatalf("duplicate released key %08X in %s", cell, path) }
					values[cell] = r
				}
			}
		}
		walk(root, 0, 0)
	}
	return values
}

func TestXMLRawJP2Mappings(t *testing.T) {
	label:="ISO-2022-JP-2"
	jis0208,jis0212:=rawJISSource(t),rawJIS0212Source(t)
	gb,ksc,greek:=rawJP2ReleasedMap(t,"euc_cn",2),rawJP2ReleasedMap(t,"euc_kr",2),rawJP2ReleasedMap(t,"iso8859_7",1)
	for _,tc:=range []struct { name,designation string; lookup func(uint32) rune; assigned int }{
		{"jis0208-at","\x1b$@",func(cell uint32) rune { return jis0208[uint16(cell)] },6879}, {"jis0208-b","\x1b$B",func(cell uint32) rune { return jis0208[uint16(cell)] },6879},
		{"jis0212","\x1b$(D",func(cell uint32) rune { return jis0212[uint16(cell)] },6067}, {"gb2312","\x1b$A",func(cell uint32) rune { return gb[cell] },7445}, {"ksc5601","\x1b$(C",func(cell uint32) rune { return ksc[cell] },8227},
	} {
		t.Run(tc.name,func(t *testing.T) {
			assigned,holes:=0,0
			for lead:=0x21;lead<=0x7e;lead++ { for trail:=0x21;trail<=0x7e;trail++ {
				cell:=uint16(lead<<8|trail);sourceCell:=cell
				if tc.name=="gb2312" || tc.name=="ksc5601" { sourceCell|=0x8080 }
				want:=tc.lookup(uint32(sourceCell))
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
				if tc.name=="greek" { r=greek[uint32(value|0x80)];mapped=value>=0x20 && value<0x80 && r!=0 }
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
