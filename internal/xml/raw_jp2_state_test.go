package xml

import "testing"

// All G0-to-G0 transitions cross both assigned G2 sets. Each SS2 is followed
// by another G0 payload, so losing G0 during a single shift is observable.
func testRawJP2StateMatrix(t *testing.T) {
	t.Helper()
	g0:=[]struct { name,designation,payload,text string }{
		{"ascii","\x1b(B","A","A"}, {"roman","\x1b(J","\\","¥"},
		{"kana","\x1b(I","!","｡"}, {"jis-at","\x1b$@","$\"","あ"},
		{"jis-b","\x1b$B","$\"","あ"}, {"jis0212","\x1b$(D","+1","é"},
		{"gb","\x1b$A","VP","中"}, {"ksc","\x1b$(C","GQ","한"},
	}
	for _,first:=range g0 { for _,second:=range g0 { for _,g2:=range []struct { name,designation,payload,text string }{
		{"latin","\x1b.A","i","é"}, {"greek","\x1b.F","a","α"},
	} {
		t.Run(first.name+"-"+second.name+"-"+g2.name,func(t *testing.T) {
			body:=g2.designation+first.designation+first.payload+"\x1bN"+g2.payload+first.payload+second.designation+second.payload+"\x1bN"+g2.payload+second.payload+"\x1b(B"
			text:=first.text+g2.text+first.text+second.text+g2.text+second.text
			rawAccept(t,rawDocument("ISO-2022-JP-2",body),rawDocument("ISO-2022-JP-2",text),text,RawInputInfo{Codec:"ISO-2022-JP-2"},rawDecl("ISO-2022-JP-2"))
		})
	} } }
}
