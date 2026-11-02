package xml

import (
	"errors"
	"strings"
	"testing"
)

type rawCodecWitness struct { labels,codec,bytes,text string }
// Literal independently qualified raw-reader outcomes, including both combined
// Unicode words from the two JIS2004 conversion kernels.
var rawCodecWitnesses=[]rawCodecWitness{
	{"UTF8 UTF-8","UTF8","é","é"},
	{"EUC_JP EUC-JP","EUC_JP","\xa4\xa2","あ"},
	{"EUC_CN","EUC_CN","\xd6\xd0","中"},
	{"EUC_KR","EUC_KR","\xb0\xa1","가"},
	{"EUC_TW","EUC_TW","\xc4\xa1","一"},
	{"EUC-JISX0213","EUC_JIS_2004","\xa4\xf7","か\u309a"},
	{"SHIFT_JISX0213","SHIFT_JIS_2004","\x82\xf5","か\u309a"},
	{"LATIN1 ISO-8859-1","LATIN1","\xe9","é"},
	{"LATIN2 ISO-8859-2","LATIN2","\xa1","Ą"},
	{"LATIN3 ISO-8859-3","LATIN3","\xa1","Ħ"},
	{"LATIN4 ISO-8859-4","LATIN4","\xa1","Ą"},
	{"LATIN5 ISO-8859-9","LATIN5","\xa1","¡"},
	{"LATIN6 ISO-8859-10","LATIN6","\xa1","Ą"},
	{"LATIN7 ISO-8859-13","LATIN7","\xa1","\u201d"},
	{"LATIN8 ISO-8859-14","LATIN8","\xa1","\u1e02"},
	{"LATIN9 ISO-8859-15","LATIN9","\xa1","¡"},
	{"LATIN10 ISO-8859-16","LATIN10","\xa1","Ą"},
	{"WINDOWS-1250","WIN1250","\xc0","Ŕ"},
	{"WINDOWS-1251","WIN1251","\xc0","А"},
	{"WINDOWS-1252","WIN1252","\xc0","À"},
	{"WINDOWS-1253","WIN1253","\xc0","\u0390"},
	{"WINDOWS-1254","WIN1254","\xc0","À"},
	{"WINDOWS-1255","WIN1255","\xc0","\u05b0"},
	{"WINDOWS-1256","WIN1256","\xc0","\u06c1"},
	{"WINDOWS-1257","WIN1257","\xc0","Ą"},
	{"WINDOWS-1258","WIN1258","\xc0","À"},
	{"CP866","WIN866","\x80","А"},
	{"CP874","WIN874","\xa1","ก"},
	{"ISO_8859_5 ISO-8859-5","ISO_8859_5","\xa1","Ё"},
	{"ISO_8859_6 ISO-8859-6","ISO_8859_6","\xc7","ا"},
	{"ISO_8859_7 ISO-8859-7","ISO_8859_7","\xa1","\u2018"},
	{"ISO_8859_8 ISO-8859-8","ISO_8859_8","\xe0","א"},
	{"KOI8R KOI8-R","KOI8R","\xd1","я"},
	{"KOI8U","KOI8U","\xa4","є"},
	{"SJIS SHIFT_JIS","SJIS","\x82\xa0","あ"},
	{"BIG5","BIG5","\xa4\xa4","中"},
	{"GBK","GBK","\xd6\xd0","中"},
	{"UHC","UHC","\xb0\xa1","가"},
	{"GB18030","GB18030","\x94\x39\xfc\x36","😀"},
	{"JOHAB","JOHAB","\x88\x61","가"},
}
func TestXMLRawDirectCodecs(t *testing.T) {
	if len(rawCodecWitnesses)!=40{t.Fatal("missing direct identity witness")}
	seen:=map[string]bool{}
	for _,tc:=range rawCodecWitnesses{
		if seen[tc.codec]{t.Fatal("duplicate identity",tc.codec)};seen[tc.codec]=true
		label:=strings.Fields(tc.labels)[0]
		rawAccept(t,rawDocument(label,tc.bytes),rawDocument(label,tc.text),tc.text,RawInputInfo{Codec:tc.codec},rawDecl(label))
	}
	rawAccept(t,rawDocument("WINDOWS-1252","\x80"),rawDocument("WINDOWS-1252","€"),"€",RawInputInfo{Codec:"WIN1252"},rawDecl("WINDOWS-1252"))
	testRawJOHAB(t)
}
func TestXMLRawLabels(t *testing.T) {
	witnesses:=append([]rawCodecWitness(nil),rawCodecWitnesses...)
	witnesses=append(witnesses,rawCodecWitness{"ISO-2022-JP ISO2022JP CSISO2022JP ISO_2022_JP","ISO-2022-JP","\x1b$B$\"\x1b(B","あ"})
	for _,tc:=range witnesses{for _,alias:=range strings.Fields(tc.labels){for _,label:=range []string{alias,strings.ToLower(alias)}{
		rawAccept(t,rawDocument(label,tc.bytes),rawDocument(label,tc.text),tc.text,RawInputInfo{Codec:tc.codec},rawDecl(label))
	}}}
	for _,label:=range []string{"WIN1250","WIN1251","WIN1252","WIN1253","WIN1254","WIN1255","WIN1256","WIN1257","WIN1258","WIN866","WIN874","EUC_JIS_2004","SHIFT_JIS_2004","SQL_ASCII","MULE_INTERNAL","BOGUS"}{
		input:=rawDocument(label,"\xff")
		err:=rawAtomicFailure(t,input);var re *RawInputError
		if !errors.As(err,&re)||re.Kind!=RawUnknownLabel||re.Label!=label||re.Codec!=""||re.RawOffset!=len(rawHeader(label))-2||re.Bytes!=""||re.Cause!=nil{t.Fatalf("unknown %s: %+v",label,err)}
		decl:=rawDecl(label);decl.End=0
		want:=[]Diagnostic{rawExpectedDiagnostic(input,"XML_ERR_UNSUPPORTED_ENCODING","Unsupported encoding "+label,len(rawHeader(label))-2,len(rawHeader(label))-1)}
		if re.Declaration!=decl{t.Fatalf("declaration %+v want %+v",re.Declaration,decl)}
		assertRawDiagnostics(t,re.Diagnostics,want,re.Detail())
	}
	for _,label:=range []string{"ASCII","US-ASCII","ISO-2022-JP-2","UNICODE","UTF16","UTF-16"}{
		err:=rawAtomicFailure(t,rawDocument(label,"A"));var re *RawInputError
		decl:=rawDecl(label);decl.End=0
		if !errors.As(err,&re)||re.Kind!=RawUnavailableCodec||re.Label!=label||re.Codec!=""||re.RawOffset!=len(rawHeader(label))-2||re.Bytes!=""||re.Cause!=nil||re.Declaration!=decl||len(re.Diagnostics)!=0||re.Detail()!=""{t.Fatalf("capability %s: %+v",label,err)}
	}
	for _,label:=range []string{" UTF8","UTF8 ","U-T-F-8","UTF_8","windows1252","CP1252","LATIN-1","ISO2022JP//","Unicode"}{if rawEncoding(label)!=-1{t.Fatal("unqualified spelling executable",label)}}
}
