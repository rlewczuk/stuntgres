package xml

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestXMLRawISO2022JP2(t *testing.T) {
	for _, label := range []string{"ISO-2022-JP-2", "ISO2022JP2", "CSISO2022JP2", "iso-2022-jp-2", "CsIsO2022jP2"} {
		t.Run(label, func(t *testing.T) {
			for _, tc := range []struct { bytes, decoded, text string }{
				{"A", "A", "A"}, {"\\~", "\\~", "\\~"},
				{"\x1b(J\\~\x1b(B\\~", "¥‾\\~", "¥‾\\~"},
				{"\x1b$@$\"\x1b(B", "あ", "あ"},
				{"\x1b$B$\"\x1b$B$$\x1b(B", "あい", "あい"},
				{"\x1b$(D+1\x1b(B", "é", "é"},
				{"\x1b$(CGQ\x1b(B", "한", "한"},
				{"\x1b$AVP\x1b(B", "中", "中"},
				{"\x1b(I!_\x1b(B", "｡ﾟ", "｡ﾟ"},
				{"\x1b.A\x1bNiA\x1bNq", "éAñ", "éAñ"},
				{"\x1b.F\x1bNa", "α", "α"},
				{"\x1b.A\x1bN \x1bN\x7f\x1bN\xe9", "\u00a0ÿé", "\u00a0ÿé"},
				{"\x1b.A\x1b$B$\"\x1b(B\x1bNi\x1b$(CGQ\x1b(B\x1bNq", "あé한ñ", "あé한ñ"},
				{"\x1b.A\x1b.F\x1bNa\x1b(J\\~\x1bNa\x1b(B", "α¥‾α", "α¥‾α"},
				{"\x1b.A\x1b$B$\"\x1bNi$$\x1b(B", "あéい", "あéい"},
				{"\x1b.F\x1bNa\x1b.A\x1bNi\x1b.F\x1bNa", "αéα", "αéα"},
				{"\x1b$B$\"\n$$\x1b(B", "あ\nい", "あ\nい"},
				{"\x1b.A\x1bNi\n\x1bNi", "é\né", "é\né"},
				{"\x1b$(D\x1b$(D+1\x1b(B", "é", "é"},
				{"\x1b$B \t\r\n\x7f\x1b(B", " \t\r\n\x7f", " \t\n\x7f"},
			} {
				rawAccept(t, rawDocument(label, tc.bytes), rawDocument(label, tc.decoded), tc.text, RawInputInfo{Codec:"ISO-2022-JP-2"}, rawDecl(label))
			}
		})
	}
	testRawJP2StateMatrix(t)
	for _, body := range []string{"\x1bNi", "\x80", "\xff", "\x1b.F\x1bN\x1f", "\x1b.F\x1bN\x80", "\x1b.F\x1bN.", "\x1b.F\x1bNR", "\x1b.F\x1bN\x7f", "\x1b(I`\x1b(B", "\x1b$(D!!\x1b(B", "\x1b$(CI!\x1b(B", "\x1b$Ax!\x1b(B"} {
		var re *RawInputError
		if err := rawAtomicFailure(t, rawDocument("ISO-2022-JP-2", body)); !errors.As(err, &re) || re.Kind != RawInvalidSequence { t.Fatalf("JP2 illegal %x: %v", body, err) }
	}
	for _, body := range []string{"\x1b$X", "\x1b(ZA", "\x1b.ZA", "\x1b$(XA", "\x00", "\x0eA\x0f", "\x1b$B$\n\x1b(B"} {
		var pe *ParseError
		err := rawAtomicFailure(t, rawDocument("ISO-2022-JP-2", body))
		if strings.Contains(body, "$\n") { var re *RawInputError; if !errors.As(err, &re) { t.Fatal(err) }; continue }
		if !errors.As(err, &pe) { t.Fatalf("JP2 grammar %x: %v", body, err) }
	}
}

func TestXMLRawJP2Replay(t *testing.T) {
	label := "ISO-2022-JP-2"
	for _, tc := range []struct { tail string; buffered int }{
		{"\x1b",1}, {"\x1bX",2}, {"\x1b$",2}, {"\x1b(",2}, {"\x1b.",2}, {"\x1bN",2}, {"\x1b\x80",2}, {"\x1b$(",3},
		{"\x1b(B",0}, {"\x1b(J",0}, {"\x1b(I",0}, {"\x1b$@",0}, {"\x1b$B",0}, {"\x1b$A",0}, {"\x1b$(C",0}, {"\x1b$(D",0}, {"\x1b.A",0}, {"\x1b.F",0},
		{"\x1b$B$",1}, {"\x1b$(D+",1}, {"\x1b$AV",1}, {"\x1b$(CG",1},
		// JIS0212 and GB validate the lead before checking available bytes;
		// KSC checks availability first, including an invalid row at EOF.
		{"\x1b$(CI",1}, {"\x1b$(C~",1},
	} {
		rawAccept(t, rawHeader(label)+"<r/>"+tc.tail, rawHeader(label)+"<r/>", "", RawInputInfo{Codec:label,BufferedBytes:tc.buffered}, rawDecl(label))
		rawAtomicFailure(t, rawHeader(label)+"<r>"+tc.tail)
		// Extending the physical input beyond a short ESC must change its
		// classification; it cannot hide illegal bytes as replay/EOF buffering.
		rawAtomicFailure(t, rawHeader(label)+"<r/>"+tc.tail+"\x80\x80")
	}
	for _, tail := range []string{"\x1b$(D!", "\x1b$(Dn", "\x1b$Ax"} {
		var re *RawInputError
		if err := rawAtomicFailure(t, rawHeader(label)+"<r/>"+tail); !errors.As(err, &re) || re.Kind != RawInvalidSequence { t.Fatalf("invalid first lead buffered %x: %v", tail, err) }
	}
	for _, tc := range []struct { tail, decoded string; buffered int }{
		{"\x1b.A\x1bNi\x1bN", "é", 2},
		{"\x1b.A\x1bNi\x1b(B\x1bNi\x1bX", "éé", 2},
		{"\x1b$(D+1\x1b(B\x1b$(", "é", 3},
	} {
		input := rawHeader(label)+"<r>"+tc.tail[:len(tc.tail)-tc.buffered]+"</r>"+tc.tail[len(tc.tail)-tc.buffered:]
		rawAccept(t, input, rawDocument(label,tc.decoded), tc.decoded, RawInputInfo{Codec:label,BufferedBytes:tc.buffered}, rawDecl(label))
	}
	// Unknown complete ESC is a scalar, not a zero-output designation. When
	// a later conversion fault limits emission it must remain in the prefix.
	for _, prefix := range []string{"<r>", "<r/>"} {
		for _, body := range []string{"\x1b$X", "\x1b$X\x80", "\x1b.A\x1bNi\x80", "\x1b.A\x1b$B$\"\x1bNi\x80"} {
			err := rawAtomicFailure(t, rawHeader(label)+prefix+body)
			if strings.HasSuffix(body,"\x80") {
				var re *RawInputError
				if !errors.As(err,&re) || re.RawOffset != len(rawHeader(label)+prefix+body)-1 || re.Bytes != "\x80" { t.Fatalf("replay %x: %+v",body,err) }
				if strings.HasPrefix(body,"\x1b$X") {
					want := Condition("XML_ERR_INVALID_CHAR")
					if prefix == "<r/>" { want = "XML_ERR_DOCUMENT_END" }
					found := false
					for _, d := range re.Diagnostics { if d.Condition == want { found=true } }
					if !found { t.Fatalf("literal ESC disappeared in replay %+v",re.Diagnostics) }
				}
			} else { var pe *ParseError; if !errors.As(err,&pe) { t.Fatal(err) } }
		}
	}
	// Real parser entrypoints supplement exact private pass/state assertions.
	if _, err := Parse(context.Background(), rawDocument(label,"\x1b$(D+1\x1b(B"), rawOptions); err != nil { t.Fatal(err) }
}
