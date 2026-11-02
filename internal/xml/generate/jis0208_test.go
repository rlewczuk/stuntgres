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

func jisOriginal(t *testing.T) []byte {
	t.Helper();data,err:=os.ReadFile("../data/JIS0208.TXT");if err!=nil{t.Fatal(err)};return data
}
func TestXMLRawJISGeneratorFailures(t *testing.T){
	data:=jisOriginal(t)
	var first string
	for _,line:=range strings.Split(string(data),"\n"){if strings.HasPrefix(line,"0x"){first=line;break}}
	if first==""{t.Fatal("missing original rows")}
	for name,broken:=range map[string][]byte{
		"empty":nil,
		"missing":bytes.Replace(data,[]byte(first+"\n"),nil,1),
		"duplicate":append(append([]byte(nil),data...),[]byte(first+"\n")...),
		"order":append(bytes.Replace(data,[]byte(first+"\n"),nil,1),[]byte(first+"\n")...),
		"sjissyntax":bytes.Replace(data,[]byte("0x8140"),[]byte("0x8000"),1),
		"syntax":bytes.Replace(data,[]byte(first),[]byte("broken row"),1),
		"cellrange":bytes.Replace(data,[]byte(first),[]byte(strings.Replace(first,"0x2121","0x2021",1)),1),
		"surrogate":bytes.Replace(data,[]byte("0x3000"),[]byte("0xD800"),1),
		"outofrange":bytes.Replace(data,[]byte("0x3000"),[]byte("0x110000"),1),
		"zero":bytes.Replace(data,[]byte("0x3000"),[]byte("0x0000"),1),
		"correctiondrift":bytes.Replace(data,[]byte("0x005C"),[]byte("0x005D"),1),
	}{t.Run(name,func(t *testing.T){if _,err:=parseJIS0208(broken);err==nil{t.Fatal("corrupt source accepted")}})}
}
func TestXMLRawJISGeneration(t *testing.T){
	data:=jisOriginal(t);table,err:=parseJIS0208(data);if err!=nil{t.Fatal(err)}
	generated:=generateJIS0208(table)
	if !bytes.Equal(generated,generateJIS0208(table)){t.Fatal("nondeterministic generation")}
	checkedIn,err:=os.ReadFile("../jis0208_generated.go");if err!=nil{t.Fatal(err)}
	if !bytes.Equal(generated,checkedIn){t.Fatal("generated table differs; run go run ./internal/xml/generate from repository root")}
	file,err:=parser.ParseFile(token.NewFileSet(),"generated.go",generated,0);if err!=nil{t.Fatal(err)}
	var literal func(ast.Expr) string
	literal=func(expr ast.Expr)string{
		switch e:=expr.(type){
		case *ast.BasicLit:v,err:=strconv.Unquote(e.Value);if err!=nil{t.Fatal(err)};return v
		case *ast.BinaryExpr:if e.Op!=token.ADD{t.Fatal("unexpected expression")};return literal(e.X)+literal(e.Y)
		default:t.Fatalf("unexpected generated expression %T",expr);return ""
		}
	}
	value:=""
	for _,decl:=range file.Decls{g,ok:=decl.(*ast.GenDecl);if !ok{continue};for _,spec:=range g.Specs{v,ok:=spec.(*ast.ValueSpec);if ok&&len(v.Names)==1&&v.Names[0].Name=="jis0208Data"{value=literal(v.Values[0])}}}
	if len(value)!=8836*2{t.Fatal("wrong generated extent",len(value))}
	// Independent complete source parsing, with a literal single correction.
	want:=map[int]uint16{}
	for _,line:=range strings.Split(string(data),"\n"){
		if !strings.HasPrefix(line,"0x"){continue}
		fields:=strings.Fields(line);cell,e1:=strconv.ParseUint(fields[1],0,16);scalar,e2:=strconv.ParseUint(fields[2],0,16)
		if e1!=nil||e2!=nil{t.Fatal("independent input parse")}
		if cell==0x2140{if scalar!=0x005c{t.Fatal("correction input")};scalar=0xff3c}
		want[(int(cell>>8)-0x21)*94+int(cell&255)-0x21]=uint16(scalar)
	}
	assigned:=0
	for at:=range 8836{got:=uint16(value[at*2])<<8|uint16(value[at*2+1]);if got!=want[at]{t.Fatalf("cell %d: %04X want %04X",at,got,want[at])};if got!=0{assigned++}}
	if assigned!=6879{t.Fatal("incomplete assigned table",assigned)}
}
