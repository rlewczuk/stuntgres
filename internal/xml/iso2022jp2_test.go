package xml

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func TestXMLRawJP2Steps(t *testing.T) {
	for _,g0:=range []jp2Set{jp2ASCII,jp2Roman,jp2Kana,jp2JIS0208,jp2JIS0212,jp2GB2312,jp2KSC5601} {
		for _,g2:=range []jp2G2{jp2Unassigned,jp2Latin,jp2Greek} {
			state:=jp2State{g0:g0,g2:g2}
			for _,tc:=range []struct { bytes string; g0 jp2Set }{
				{"\x1b(B",jp2ASCII},{"\x1b(J",jp2Roman},{"\x1b(I",jp2Kana},{"\x1b$@",jp2JIS0208},{"\x1b$B",jp2JIS0208},{"\x1b$(D",jp2JIS0212},{"\x1b$A",jp2GB2312},{"\x1b$(C",jp2KSC5601},
			} {
				want:=jp2StepResult{n:len(tc.bytes),state:jp2State{g0:tc.g0,g2:g2}}
				if got:=iso2022JP2Step(tc.bytes,state);got!=want { t.Fatalf("G0 %x from %+v: %+v want %+v",tc.bytes,state,got,want) }
			}
			for _,tc:=range []struct { bytes string; g2 jp2G2 }{{"\x1b.A",jp2Latin},{"\x1b.F",jp2Greek}} {
				want:=jp2StepResult{n:3,state:jp2State{g0:g0,g2:tc.g2}}
				if got:=iso2022JP2Step(tc.bytes,state);got!=want { t.Fatalf("G2 from %+v: %+v want %+v",state,got,want) }
			}
			for _,value:=range []byte{0,9,10,13,0x20,0x7f} {
				want:=jp2StepResult{r:rune(value),n:1,state:state,emits:true}
				if got:=iso2022JP2Step(string([]byte{value}),state);got!=want { t.Fatalf("shared scalar %02X from %+v: %+v want %+v",value,state,got,want) }
			}
			for _,tail:=range []string{"\x1b","\x1bX","\x1b\x80","\x1b$","\x1b(","\x1b.","\x1bN","\x1b$("} {
				if got:=iso2022JP2Step(tail,state);got!=(jp2StepResult{state:state,incomplete:true}) { t.Fatalf("physical EOF %x from %+v: %+v",tail,state,got) }
			}
			if got:=iso2022JP2Step("\x1b$X",state);got!=(jp2StepResult{r:0x1b,n:1,state:state,emits:true}) { t.Fatal("unknown complete ESC was discarded",got) }
			want:=jp2StepResult{r:'é',n:3,state:state,emits:true}
			if g2==jp2Greek { want.r='ι' }
			if g2==jp2Unassigned { want=jp2StepResult{state:state,invalid:true} }
			if got:=iso2022JP2Step("\x1bNi",state);got!=want { t.Fatalf("SS2 from %+v: %+v want %+v",state,got,want) }
		}
	}
	for _,tc:=range []struct { input string; state jp2State; r rune; n int; incomplete,invalid bool }{
		{"+",jp2State{g0:jp2JIS0212},0,0,true,false},
		{"!",jp2State{g0:jp2JIS0212},0,0,false,true},
		{"n",jp2State{g0:jp2JIS0212},0,0,false,true},
		{"x",jp2State{g0:jp2GB2312},0,0,false,true},
		{"V",jp2State{g0:jp2GB2312},0,0,true,false},
		{"I",jp2State{g0:jp2KSC5601},0,0,true,false},
		{"I!",jp2State{g0:jp2KSC5601},0,0,false,true},
		{"~",jp2State{g0:jp2KSC5601},0,0,true,false},
		{"~!",jp2State{g0:jp2KSC5601},0,0,false,true},
		{"\x22\x37",jp2State{g0:jp2JIS0212},0xff5e,2,false,false},
		{"\x22\x43",jp2State{g0:jp2JIS0212},0x00a6,2,false,false},
		{"\x1bN\x00",jp2State{g0:jp2JIS0208,g2:jp2Latin},0x80,3,false,false},
		{"\x1bN\xe9",jp2State{g0:jp2JIS0212,g2:jp2Latin},0xe9,3,false,false},
		{"\x1bN.",jp2State{g0:jp2JIS0208,g2:jp2Greek},0,0,false,true},
	} {
		want:=jp2StepResult{r:tc.r,n:tc.n,state:tc.state,emits:tc.n!=0,incomplete:tc.incomplete,invalid:tc.invalid}
		if got:=iso2022JP2Step(tc.input,tc.state);got!=want { t.Fatalf("helper %x %+v: %+v want %+v",tc.input,tc.state,got,want) }
	}
	if allocations:=testing.AllocsPerRun(20,func(){
		got:=iso2022JP2Step("GQ",jp2State{g0:jp2KSC5601,g2:jp2Greek})
		if got.r!='한' || !got.emits { t.Fatal(got) }
	});allocations!=0 { t.Fatal("mapping step allocated",allocations) }
	for _,pair:=range [][2]byte{{0x20,0x21},{0x21,0x20},{0x7f,0x21},{0x21,0x7f}} { if jis0212Rune(pair[0],pair[1])!=0 { t.Fatal("out-of-range cell admitted",pair) } }
}

func TestXMLRawJP2ReplayPass(t *testing.T) {
	for _,tc:=range []struct { input,want string; consumed,fault,incomplete int; state jp2State }{
		{"A\x1b\x80","A",1,-1,2,jp2State{}},
		{"A\x1bX\x80","A\x1bX",3,3,0,jp2State{}},
		{"A\x1b$X\x80","A\x1b$X",4,4,0,jp2State{}},
		{"A\x1bNi","A",1,1,0,jp2State{}},
		{"\x1b.A\x1bNi\x80","é",6,6,0,jp2State{g2:jp2Latin}},
		{"A\x1b.F\x1bNa\x1b","Aα",7,-1,1,jp2State{g2:jp2Greek}},
		{"A\x1b.A\x1b$B$\"\x1bNi$$\x1b(B","Aあéい",17,-1,0,jp2State{g2:jp2Latin}},
		{"\x1b$(D!","",4,4,0,jp2State{g0:jp2JIS0212}},
		{"\x1b$(D+","",4,-1,1,jp2State{g0:jp2JIS0212}},
		{"\x1b$(CI","",4,-1,1,jp2State{g0:jp2KSC5601}},
		{"\x1b$(CI!","",4,4,0,jp2State{g0:jp2KSC5601}},
		{"\x1b$Ax","",3,3,0,jp2State{g0:jp2GB2312}},
		{"\x1b$B~","",3,-1,1,jp2State{g0:jp2JIS0208}},
		{"\x1b.A\x1b$B$\"\x1bNi\x80","あé",11,11,0,jp2State{g0:jp2JIS0208,g2:jp2Latin}},
		{"\x1b.F\x1b$(D+1\x1bN.","é",9,9,0,jp2State{g0:jp2JIS0212,g2:jp2Greek}},
	} {
		for _,prefix:=range []string{"","prefix"} {
			decoded,status,err:=rawDecode(context.Background(),prefix+tc.input,len(prefix),rawISO2022JP2)
			if err!=nil || decoded!=prefix+tc.want || status.size!=len(tc.want) || status.consumed!=tc.consumed || status.fault!=tc.fault || status.incomplete!=tc.incomplete || status.jp2!=tc.state { t.Fatalf("decode %x: %q %+v %v",tc.input,decoded,status,err) }
			if tc.fault>=0 {
				if status.cause==nil || status.cause.Error()!=fmt.Sprintf("invalid byte sequence for ISO-2022-JP-2 at byte %d",tc.fault) { t.Fatal("fault cause",status.cause) }
				p:=parser{ctx:context.Background()}
				if !p.retryRawBuffer(tc.input[tc.fault:],rawISO2022JP2,status) { t.Fatalf("retry lost G0/G2 %+v",status.jp2) }
			} else if status.cause!=nil { t.Fatal("successful/buffered input has a cause",status.cause) }
			var out strings.Builder
			replayed,e:=iso2022JP2Pass(context.Background(),tc.input,tc.consumed,&out)
			if e!=nil || out.String()!=tc.want || replayed.size!=len(tc.want) || replayed.consumed!=tc.consumed || replayed.fault!=-1 || replayed.incomplete!=0 || replayed.cause!=nil || replayed.jp2!=tc.state { t.Fatalf("full-physical bounded replay %x: %q %+v %v",tc.input,out.String(),replayed,e) }
		}
	}
	// Every complete boundary replays a prefix with full lookahead, including
	// a genuine three-byte SS2 scalar and a literal ESC before a later fault.
	input:="A\x1b.A\x1bNi\x1b$X\x80"
	for _,tc:=range []struct { limit int; want string }{{0,""},{1,"A"},{4,"A"},{7,"Aé"},{8,"Aé\x1b"},{9,"Aé\x1b$"},{10,"Aé\x1b$X"}} {
		var out strings.Builder
		status,err:=iso2022JP2Pass(context.Background(),input,tc.limit,&out)
		if err!=nil || out.String()!=tc.want || status.consumed!=tc.limit || status.fault!=-1 || status.incomplete!=0 { t.Fatal(tc,out.String(),status,err) }
	}
}

func TestXMLRawASCIIIdentity(t *testing.T) {
	input:=rawDocument("ASCII","A&#233;\x7f")
	decoded,status,err:=rawDecode(context.Background(),input,len(rawHeader("ASCII"))-2,rawASCII)
	if err!=nil || decoded!=input || unsafe.StringData(decoded)!=unsafe.StringData(input) || status.fault!=-1 || status.size!=len(input)-(len(rawHeader("ASCII"))-2) { t.Fatal("validated ASCII identity copied or changed",decoded,status,err) }
	for _,input:=range []string{"A\x80tail","A\x00\xfftail"} {
		decoded,status,err:=rawDecode(context.Background(),input,0,rawASCII)
		at:=strings.IndexFunc(input,func(r rune) bool { return r>=0x80 })
		if err!=nil || status.fault!=at || status.consumed!=at || status.size!=at || decoded!=input[:at] || status.incomplete!=0 || status.cause==nil || !reflect.DeepEqual(status.jp2,jp2State{}) { t.Fatal("ASCII physical validation",decoded,status,err) }
	}
}
