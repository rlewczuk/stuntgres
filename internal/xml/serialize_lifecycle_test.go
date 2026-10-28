package xml

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

func TestXMLSerializeCancellation(t *testing.T) {
	ordinary := serializeValue(t, "<a><b/></a>", Content)
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, indent := range []bool{false, true} {
			c := &phaseContext{Context: context.Background(), phase: "Serialize", after: 1, cause: cause}
			out, _, e := Serialize(c, ordinary, SerializeOptions{Indent: indent})
			if out != "" || !errors.Is(e, cause) {
				t.Fatal(out, e)
			}
		}
		for _, tc := range []struct{ phase, input string }{
			{"text", "<a>" + strings.Repeat("x", 20000) + "</a>"},
			{"ignorableBlank", "<a>" + strings.Repeat(" ", 20000) + "<b/></a>"},
			{"collectCharacters", "<a>\r" + strings.Repeat("\t", 2400) + "<b/> </a>"},
			{"collectEnumerationMember", "<!DOCTYPE a [<!ATTLIST a x (v|w|v|w|z) #IMPLIED>]><a/>"},
			{"collectEnumerationMember", "<!DOCTYPE a [<!ATTLIST a x NOTATION (n|m|n|m|o) #IMPLIED>]><a/>"},
			{"element", "<a>" + strings.Repeat("<b/>", 100) + "</a>"},
			{"node", "<a>" + strings.Repeat("<b/>", 100) + "</a>"},
			{"escape", "<a>" + strings.Repeat("é&amp;", 5000) + "</a>"},
			{"escape", "<a x='" + strings.Repeat("é&amp;&#13;", 5000) + "'/>"},
			{"namespaceCopy", "<a><!--" + strings.Repeat("x", 20000) + "--></a>"},
			{"saveDTD", "<!DOCTYPE a [<!NOTATION n SYSTEM 'u'><!ENTITY e 'x'>]><a>&e;</a>"},
			{"model", "<!DOCTYPE a [<!ELEMENT a ((b,c)|(d,e))>]><a/>"},
			{"quoted", "<!DOCTYPE a [<!ENTITY e '" + strings.Repeat("x", 20000) + "'>]><a/>"},
			{"hasContentType", "<!DOCTYPE html SYSTEM 'http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd'><html><head>" + strings.Repeat("<meta/>", 20) + "</head></html>"},
			{"xhtmlAttributes", "<!DOCTYPE html SYSTEM 'http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd'><html lang='en' name='x' id='a'><body/></html>"},
		} {
			value := serializeValue(t, tc.input, Content)
			before := value
			backing := unsafe.StringData(value.original)
			for _, after := range []int{1, 2} {
				for _, final := range []bool{false, true} {
					c := &phaseContext{Context: context.Background(), phase: tc.phase, after: after, cause: cause, final: final}
					out, _, e := Serialize(c, value, SerializeOptions{Indent: true})
					if !c.admitted || out != "" || !errors.Is(e, cause) {
						t.Fatalf("%s/%d/final%t admitted%t out %q error %v", tc.phase, after, final, c.admitted, out, e)
					}
					if _, ok := e.(interface{ SQLState() string }); ok {
						t.Fatal("context acquired SQLSTATE")
					}
					if !reflect.DeepEqual(value, before) || unsafe.StringData(value.original) != backing {
						t.Fatal("failed operation mutated value")
					}
					requireSerialized(t, ordinary, SerializeOptions{Indent: true}, "<a>\n  <b/>\n</a>")
				}
			}
		}
	}
}
func TestXMLSerializeOwnershipConcurrency(t *testing.T) {
	const input = `<?xml version="1.0"?><!DOCTYPE a [<!ENTITY e "<b>é</b>"><!ATTLIST a x CDATA "v">]><a>&e;</a>`
	value := serializeValue(t, input, Content)
	before := value
	backing := unsafe.StringData(value.original)
	const formatted = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE a [\n<!ENTITY e \"<b>é</b>\">\n<!ATTLIST a x CDATA \"v\">\n]>\n<a x=\"v\">\n  <b>é</b>\n</a>"
	var wg sync.WaitGroup
	for worker := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := range 12 {
				mode := Mode((worker + iteration) % 2)
				indent := iteration%3 != 0
				want := input
				if indent {
					want = formatted
					if mode == Content {
						want += "\n"
					}
				}
				out, _, e := Serialize(context.Background(), value, SerializeOptions{Mode: mode, Indent: indent})
				if e != nil || out != want {
					t.Errorf("mode %d indent %t out %q error %v", mode, indent, out, e)
					return
				}
				failed, e := Parse(context.Background(), `<!DOCTYPE z [<!ENTITY e "&e;">]><z>&e;</z>`, Options{})
				if e == nil || failed.valid {
					t.Error("recursive entity accepted")
					return
				}
				healthy, e := Parse(context.Background(), `<!DOCTYPE z [<!ENTITY e "q"><!ATTLIST z x CDATA "w">]><z>&e;</z>`, Options{})
				if e != nil {
					t.Error(e)
					return
				}
				out, _, e = Serialize(context.Background(), healthy, SerializeOptions{Mode: Document, Indent: true})
				if e != nil || out != "<!DOCTYPE z [\n<!ENTITY e \"q\">\n<!ATTLIST z x CDATA \"w\">\n]>\n<z x=\"w\">q</z>" {
					t.Error(out, e)
					return
				}
			}
		}()
	}
	wg.Wait()
	if !reflect.DeepEqual(value, before) || unsafe.StringData(value.original) != backing {
		t.Fatal("concurrent operation mutated retained value")
	}
	warning := serializeValue(t, "<a><?XMLfoo?></a>", Content)
	events := warning.Diagnostics()
	if len(events) != 1 {
		t.Fatal(events)
	}
	events[0].Message = "changed"
	if warning.Diagnostics()[0].Message == "changed" {
		t.Fatal("diagnostics borrowed")
	}
	// Result's lifetime remains character-only, with no tree/context fields.
	typ := reflect.TypeOf(value)
	for i := range typ.NumField() {
		field := typ.Field(i)
		if field.Type == reflect.TypeOf((*document)(nil)) || field.Type.Implements(reflect.TypeOf((*context.Context)(nil)).Elem()) {
			t.Fatal("operation retained in Result", field.Name)
		}
	}
}
