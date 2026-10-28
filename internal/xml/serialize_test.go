package xml

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"unsafe"
)

func serializeValue(t *testing.T, input string, mode Mode) Result {
	t.Helper()
	v, e := Parse(context.Background(), input, Options{Mode: mode})
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func requireSerialized(t *testing.T, v Result, o SerializeOptions, want string) {
	t.Helper()
	got, _, e := Serialize(context.Background(), v, o)
	if e != nil || got != want {
		t.Fatalf("got %q error %v; want %q", got, e, want)
	}
}
func TestXMLSerializeContract(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		v Result
		o SerializeOptions
	}{{Result{}, SerializeOptions{}}, {serializeValue(t, "", Content), SerializeOptions{Mode: 9}}} {
		s, _, e := Serialize(ctx, tc.v, tc.o)
		if e == nil || s != "" {
			t.Fatal(s, e)
		}
		if _, ok := e.(interface{ SQLState() string }); ok {
			t.Fatal("ordinary error acquired SQLSTATE")
		}
	}
	v := serializeValue(t, "<a> <b></b> </a>", Content)
	before := v
	for _, mode := range []Mode{Content, Document} {
		got, _, e := Serialize(ctx, v, SerializeOptions{Mode: mode})
		if e != nil || got != v.Original() || unsafe.StringData(got) != unsafe.StringData(v.original) {
			t.Fatal("no-indent failed backing identity", e)
		}
	}
	if !reflect.DeepEqual(v, before) {
		t.Fatal("retained value changed")
	}
	for _, input := range []string{"", "  ", "text", "<a/><b/>"} {
		value := serializeValue(t, input, Content)
		for _, indent := range []bool{false, true} {
			got, _, e := Serialize(ctx, value, SerializeOptions{Mode: Document, Indent: indent})
			var nd *NotDocumentError
			var pe *ParseError
			if got != "" || !errors.As(e, &nd) || errors.As(e, &pe) || e.Error() != "not an XML document" {
				t.Fatal(input, got, e)
			}
			if _, ok := e.(interface{ SQLState() string }); ok {
				t.Fatal("provider invented SQLSTATE")
			}
		}
		requireSerialized(t, value, SerializeOptions{}, input)
	}
	// Adjacent fields are accepted by the genuine DOCUMENT entry, not CONTENT.
	const adjacent = `<?xml version="1.0" encoding="UTF8"standalone="yes"?><a/>`
	value := serializeValue(t, adjacent, Document)
	requireSerialized(t, value, SerializeOptions{}, adjacent)
	requireSerialized(t, value, SerializeOptions{Mode: Document}, adjacent)
	requireSerialized(t, value, SerializeOptions{Mode: Document, Indent: true}, "<a/>")
	got, _, e := Serialize(ctx, value, SerializeOptions{Indent: true})
	var nd *NotDocumentError
	if got != "" || !errors.As(e, &nd) {
		t.Fatal(got, e)
	}
	text, warning, e := OutputText(ctx, value)
	if e != nil || text != adjacent || warning.Message != "could not parse XML declaration in stored value" || warning.Detail != "Space required." {
		t.Fatal(text, warning, e)
	}
}
func TestXMLSerializeFeatureSmoke(t *testing.T) {
	nested := serializeValue(t, "<a> <b><c/></b> </a>", Content)
	requireSerialized(t, nested, SerializeOptions{Indent: true}, "<a>\n  <b>\n    <c/>\n  </b>\n</a>")
	raw, _, e := Serialize(context.Background(), nested, SerializeOptions{})
	if e != nil || raw != nested.original || unsafe.StringData(raw) != unsafe.StringData(nested.original) {
		t.Fatal(raw, e)
	}
	fragment := serializeValue(t, "<a/><b/>", Content)
	s, _, e := Serialize(context.Background(), fragment, SerializeOptions{Mode: Document, Indent: true})
	var nd *NotDocumentError
	if s != "" || !errors.As(e, &nd) {
		t.Fatal(s, e)
	}
	declared := serializeValue(t, `<?xml version="1.0"?><a><b>é</b></a>`, Content)
	requireSerialized(t, declared, SerializeOptions{Indent: true}, "<a>\n  <b>&#xE9;</b>\n</a>")
	requireSerialized(t, declared, SerializeOptions{Mode: Document, Indent: true}, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<a>\n  <b>é</b>\n</a>")
	dtd := serializeValue(t, "<!DOCTYPE a><a/>", Content)
	requireSerialized(t, dtd, SerializeOptions{Indent: true}, "<!DOCTYPE a>\n<a/>\n")
	requireSerialized(t, dtd, SerializeOptions{Mode: Document, Indent: true}, "<!DOCTYPE a>\n<a/>")
	composed := serializeValue(t, `<!DOCTYPE a [<!ENTITY % p '<!ENTITY e "<b>é</b>"><!ATTLIST a x CDATA "v">'>%p;]><a>&e;</a>`, Content)
	requireSerialized(t, composed, SerializeOptions{Indent: true}, "<!DOCTYPE a [\n<!ENTITY % p '<!ENTITY e \"<b>é</b>\"><!ATTLIST a x CDATA \"v\">'>\n<!ENTITY e \"<b>é</b>\">\n<!ATTLIST a x CDATA \"v\">\n]>\n<a x=\"v\">\n  <b>&#xE9;</b>\n</a>\n")
	c := &phaseContext{Context: context.Background(), phase: "element", after: 2, cause: context.Canceled}
	s, _, e = Serialize(c, nested, SerializeOptions{Indent: true})
	if s != "" || !c.admitted || !errors.Is(e, context.Canceled) {
		t.Fatal(s, e)
	}
	requireSerialized(t, nested, SerializeOptions{Indent: true}, "<a>\n  <b>\n    <c/>\n  </b>\n</a>")
}
