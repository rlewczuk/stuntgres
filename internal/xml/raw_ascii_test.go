package xml

import (
	"errors"
	"strings"
	"testing"
)

func TestXMLRawASCII(t *testing.T) {
	for _, label := range []string{"ASCII", "US-ASCII", "ascii", "Us-AsCiI"} {
		t.Run(label, func(t *testing.T) {
			for value := range 128 {
				body := string([]byte{byte(value)})
				input := rawDocument(label, body)
				if (value >= 0x20 || value == 9 || value == 10 || value == 13) && value != '<' && value != '&' {
					text := body
					if value == 13 { text = "\n" }
					rawAccept(t, input, input, text, RawInputInfo{Codec:"ASCII"}, rawDecl(label))
				} else {
					var pe *ParseError
					if err := rawAtomicFailure(t, input); !errors.As(err, &pe) { t.Fatalf("7-bit grammar %02X: %v", value, err) }
				}
			}
			rawAccept(t, rawDocument(label, "&amp;&lt;&gt;&#233;"), rawDocument(label, "&amp;&lt;&gt;&#233;"), "&<>é", RawInputInfo{Codec:"ASCII"}, rawDecl(label))
			// NUL belongs to XML grammar, but must not stop genuine7bit
			// validation of later physical bytes.
			afterNUL:=rawDocument(label,"\x00\x80")
			var nulFailure *RawInputError
			if err:=rawAtomicFailure(t,afterNUL);!errors.As(err,&nulFailure) || nulFailure.Kind!=RawInvalidSequence || nulFailure.RawOffset!=len(rawHeader(label))+4 || nulFailure.Bytes!="\x80</r" { t.Fatalf("NUL hid a later ASCII fault: %+v",err) }
			for value := 0x80; value <= 0xff; value++ {
				bad := string([]byte{byte(value)})
				for _, parts := range [][2]string{
					{rawHeader(label)+"<r>", "</r>"},
					{rawHeader(label)+`<r a="`, `"/>`},
					{rawHeader(label)[:len(rawHeader(label))-2], "?><r/>"},
					{rawHeader(label)+"<r/>", "<later/>"},
				} {
					for _, bom := range []string{"", "\xef\xbb\xbf"} {
						input := bom+parts[0]+bad+parts[1]
						err := rawAtomicFailure(t, input)
						var re *RawInputError
						at := len(bom)+len(parts[0])
						if !errors.As(err, &re) || re.Kind != RawInvalidSequence || re.Label != label || re.Codec != "ASCII" || re.RawOffset != at || re.Bytes != input[at:at+min(4,len(input)-at)] || re.Cause == nil { t.Fatalf("ASCII %02X: %+v", value, err) }
						for _, d := range re.Diagnostics {
							if d.Domain == EncodingDomain || d.Domain == IODomain { t.Fatalf("builtin ASCII invented converter event %+v", d) }
						}
						// The strict selected contract rejects the entire physical
						// input even though pinned builtin ASCII drops this tail.
						if strings.HasSuffix(parts[0], "<r/>") && len(re.Diagnostics) != 0 { t.Fatalf("closed root invented grammar failure %+v", re.Diagnostics) }
					}
				}
			}
		})
	}
}
