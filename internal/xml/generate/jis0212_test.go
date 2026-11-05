package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func jis0212Original(t *testing.T) []byte {
	t.Helper()
	data,err:=os.ReadFile("../data/JIS0212.TXT");if err!=nil { t.Fatal(err) }
	return data
}

func TestXMLRawJIS0212GeneratorFailures(t *testing.T) {
	data:=jis0212Original(t)
	var first string
	for _,line:=range strings.Split(string(data),"\n") { if strings.HasPrefix(line,"0x") { first=line;break } }
	if first=="" { t.Fatal("missing actual source rows") }
	for name,broken:=range map[string][]byte{
		"empty":nil,
		"missing":bytes.Replace(data,[]byte(first+"\n"),nil,1),
		"duplicate":append(append([]byte(nil),data...),[]byte(first+"\n")...),
		"order":append(bytes.Replace(data,[]byte(first+"\n"),nil,1),[]byte(first+"\n")...),
		"syntax":bytes.Replace(data,[]byte(first),[]byte("broken actual row"),1),
		"cellrange":bytes.Replace(data,[]byte(first),[]byte(strings.Replace(first,"0x222F","0x202F",1)),1),
		"trailrange":bytes.Replace(data,[]byte(first),[]byte(strings.Replace(first,"0x222F","0x227F",1)),1),
		"nonhex":bytes.Replace(data,[]byte(first),[]byte(strings.Replace(first,"0x222F","222F",1)),1),
		"surrogate":bytes.Replace(data,[]byte(first),[]byte(strings.Replace(first,"0x02D8","0xD800",1)),1),
		"outofrange":bytes.Replace(data,[]byte(first),[]byte(strings.Replace(first,"0x02D8","0x110000",1)),1),
		"zero":bytes.Replace(data,[]byte(first),[]byte(strings.Replace(first,"0x02D8","0x0000",1)),1),
		"comment":bytes.Replace(data,[]byte(first),[]byte(strings.Split(first,"#")[0]+"#"),1),
		"correctiondrift":bytes.Replace(data,[]byte("0x2237\t0x007E"),[]byte("0x2237\t0x007F"),1),
	} {
		t.Run(name,func(t *testing.T) {
			if bytes.Equal(data,broken) { t.Fatal("mutation did not reach an actual mapping row") }
			if _,err:=parseJIS0212(broken);err==nil { t.Fatal("corrupt original source accepted") }
		})
	}
}

func TestXMLRawJIS0212Generation(t *testing.T) {
	data:=jis0212Original(t)
	table,err:=parseJIS0212(data);if err!=nil { t.Fatal(err) }
	generated:=generateJIS0212(table)
	if !bytes.Equal(generated,generateJIS0212(table)) { t.Fatal("nondeterministic generation") }
	checkedIn,err:=os.ReadFile("../jis0212_generated.go");if err!=nil { t.Fatal(err) }
	if !bytes.Equal(generated,checkedIn) { t.Fatal("generated table differs; run go run ./internal/xml/generate from repository root") }
	file,err:=parser.ParseFile(token.NewFileSet(),"generated.go",generated,0);if err!=nil { t.Fatal(err) }
	var literal func(ast.Expr) string
	literal=func(expr ast.Expr) string {
		switch e:=expr.(type) {
		case *ast.BasicLit: value,err:=strconv.Unquote(e.Value);if err!=nil { t.Fatal(err) };return value
		case *ast.BinaryExpr: if e.Op!=token.ADD { t.Fatal("unexpected expression") };return literal(e.X)+literal(e.Y)
		default: t.Fatalf("unexpected generated expression %T",expr);return ""
		}
	}
	value:=""
	for _,decl:=range file.Decls {
		g,ok:=decl.(*ast.GenDecl);if !ok { continue }
		for _,spec:=range g.Specs { v,ok:=spec.(*ast.ValueSpec);if ok && len(v.Names)==1 && v.Names[0].Name=="jis0212Data" { value=literal(v.Values[0]) } }
	}
	if len(value)!=8836*2 { t.Fatal("generated extent",len(value)) }
	// Independently read every original cell; generated candidate output is not
	// its own expected map. Apply the sole source-and-reader-qualified delta.
	want:=map[int]uint16{}
	for _,line:=range strings.Split(string(data),"\n") {
		if !strings.HasPrefix(line,"0x") { continue }
		fields:=strings.Fields(line)
		cell,e1:=strconv.ParseUint(fields[0],0,16);scalar,e2:=strconv.ParseUint(fields[1],0,16)
		if e1!=nil || e2!=nil { t.Fatal("independent source parse") }
		if cell==0x2237 { if scalar!=0x007e { t.Fatal("original correction cell drift") };scalar=0xff5e }
		want[(int(cell>>8)-0x21)*94+int(cell&255)-0x21]=uint16(scalar)
	}
	assigned:=0
	for at:=range 8836 {
		got:=uint16(value[at*2])<<8|uint16(value[at*2+1])
		if got!=want[at] { t.Fatalf("cell %d: %04X want %04X",at,got,want[at]) }
		if got!=0 { assigned++ }
	}
	if assigned!=6067 || 8836-assigned!=2769 { t.Fatal("assigned/hole inventory",assigned) }
	for _,tc:=range []struct { cell,scalar uint16 }{{0x222f,0x02d8},{0x2237,0xff5e},{0x2243,0x00a6},{0x2b31,0x00e9},{0x6d63,0x9fa5},{0x2121,0}} {
		at:=(int(tc.cell>>8)-0x21)*94+int(tc.cell&255)-0x21
		if got:=uint16(value[at*2])<<8|uint16(value[at*2+1]);got!=tc.scalar { t.Fatalf("independent literal %04X: %04X want %04X",tc.cell,got,tc.scalar) }
	}
}
