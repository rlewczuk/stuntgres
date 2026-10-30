package xml

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func TestXMLPredicateClassification(t *testing.T) {
	for _, tc := range []struct {
		s    string
		want bool
	}{{"<a/>", true}, {"<a/><b/>", false}, {"abc", false}, {"", false}, {"<!--c-->", false}, {"<!--c--><a/><?p v?>", true}} {
		value := accept(t, tc.s, Content)
		got, err := IsDocument(context.Background(), value)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
		for _, mode := range []Mode{Content, Document} {
			want := mode == Content || tc.want
			got, err := IsWellFormed(context.Background(), tc.s, mode)
			if err != nil || got != want {
				t.Fatal(tc, mode, got, err)
			}
		}
	}
	for _, s := range []string{"<>", "<a>", "<a></b>", "<a>&missing;</a>"} {
		for _, mode := range []Mode{Content, Document} {
			got, err := IsWellFormed(context.Background(), s, mode)
			if got || err != nil {
				t.Fatal(s, mode, got, err)
			}
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	value := accept(t, "<a/>", Content)
	if got, err := IsDocument(canceled, value); got || !errors.Is(err, context.Canceled) {
		t.Fatal(got, err)
	}
	if got, err := IsWellFormed(canceled, "<a/>", Document); got || !errors.Is(err, context.Canceled) {
		t.Fatal(got, err)
	}
	if _, err := IsDocument(context.Background(), Result{}); err == nil {
		t.Fatal("invalid value classified")
	}
}
func TestXMLPredicateParsePolicies(t *testing.T) {
	bom := accept(t, "\uFEFF<r/>", Content)
	if document, err := IsDocument(context.Background(), bom); err != nil || document {
		t.Fatal("context entry changed type classification", document, err)
	}
	if exists, _, err := XMLExists(context.Background(), `/r`, bom); err != nil || !exists {
		t.Fatal("memory reader failed UTF8 BOM", exists, err)
	}
	empty := accept(t, "", Content)
	_, _, err := XMLExists(context.Background(), `false()`, empty)
	var contextError *XPathContextError
	if !errors.As(err, &contextError) || contextError.Parse.Detail() != "line 1: Document is empty\n\n^" {
		t.Fatal("memory entry diagnostic", err)
	}
	_, err = Parse(context.Background(), "", Options{Mode: Document})
	var typeError *ParseError
	if !errors.As(err, &typeError) || typeError.Detail() != "line 1: switching encoding : no input\n\n^\nline 1: Document is empty\n\n^" {
		t.Fatal("type entry diagnostic changed", err)
	}
	for _, s := range []string{`<p:r/>`, `<r xmlns='&lt;'/>`, `<r xmlns:p='u' xmlns:q='u' p:a='1' q:a='2'/>`} {
		accepted := accept(t, s, Content)
		if ok, err := IsWellFormed(context.Background(), s, Document); !ok || err != nil {
			t.Fatal("strict operation changed validation", s, err)
		}
		if _, _, err := XMLExists(context.Background(), `/r`, accepted); err == nil {
			t.Fatal("strict operation admitted namespace failure", s)
		}
		if output, _, err := Serialize(context.Background(), accepted, SerializeOptions{Mode: Content}); err != nil || output != s {
			t.Fatal("strict failure contaminated value", output, err)
		}
	}
	t.Run("xml-id", func(t *testing.T) {
		const source = `<r xml:id="a b"/>`
		value := accept(t, source, Content)
		saved, backing := value, unsafe.StringData(value.original)
		for _, mode := range []Mode{Content, Document} {
			if valid, err := IsWellFormed(context.Background(), source, mode); err != nil || !valid {
				t.Fatal("strict xml:id policy changed ordinary validation", mode, valid, err)
			}
		}
		if document, err := IsDocument(context.Background(), value); err != nil || !document {
			t.Fatal("strict xml:id policy changed document classification", document, err)
		}
		exists, notices, err := XMLExists(context.Background(), `false()`, value)
		var failure *XPathContextError
		want := Diagnostic{Condition: "XML_DTD_XMLID_VALUE", Domain: DTDValidityDomain, Severity: Error, Line: 1, Column: 16, Offset: 15, Message: "xml:id : attribute value a b is not an NCName", SourceContext: source, Caret: "               ^"}
		if exists || !errors.As(err, &failure) || !reflect.DeepEqual(failure.Parse.Diagnostics, []Diagnostic{want}) || !reflect.DeepEqual(notices, []Diagnostic{want}) || failure.Parse.Detail() != "line 1: xml:id : attribute value a b is not an NCName\n<r xml:id=\"a b\"/>\n               ^" {
			t.Fatal("xml:id context parse diagnostic", exists, notices, err)
		}
		if !reflect.DeepEqual(value, saved) || unsafe.StringData(value.original) != backing {
			t.Fatal("strict xml:id failure replaced accepted value/backing")
		}
		if output, notices, err := Serialize(context.Background(), value, SerializeOptions{Mode: Content}); err != nil || output != source || len(notices) != 0 {
			t.Fatal("strict xml:id failure contaminated ordinary serialization", output, notices, err)
		}
		// Released xmlValidateNCName(value,1) allows surrounding blanks but
		// SAX2 leaves their spelling intact in both the attribute and ID index.
		padded := accept(t, `<r xml:id=" a "/>`, Content)
		paddedBacking := unsafe.StringData(padded.original)
		for _, tc := range []struct {
			path string
			want bool
		}{{`false()`, true}, {`/r[@xml:id=' a ']`, true}, {`id('a')`, false}} {
			got, notices, err := XMLExists(context.Background(), tc.path, padded)
			if err != nil || got != tc.want || len(notices) != 0 || unsafe.StringData(padded.original) != paddedBacking {
				t.Fatal("xml:id was normalized or contaminated", tc, got, notices, err)
			}
		}
		valid := accept(t, `<r xml:id="a"/>`, Content)
		if exists, notices, err := XMLExists(context.Background(), `false()`, valid); err != nil || !exists || len(notices) != 0 {
			t.Fatal("xml:id failure contaminated a subsequent strict parse", exists, notices, err)
		}
	})
	s := `<!DOCTYPE r [<!ENTITY e '<a>entity</a>'><!ATTLIST r d CDATA 'v' id ID #IMPLIED>]><r id='a'>x&e;y</r>`
	// A top-level DTD promotes CONTENT input to effective DOCUMENT mode.
	// The general accept helper deliberately requires both modes to be equal.
	accepted, err := Parse(context.Background(), s, Options{Mode: Content})
	if err != nil || !accepted.valid || accepted.Original() != s || accepted.RequestedMode() != Content || accepted.EffectiveMode() != Document || accepted.Declaration() != (Declaration{}) || len(accepted.Diagnostics()) != 0 {
		t.Fatal("DTD input spelling/modes/validity", accepted, err)
	}
	for _, tc := range []struct {
		path string
		want bool
	}{{`/r/@d`, false}, {`/r/a`, false}, {`/r[string(.)='xentityy']`, true}, {`/r[count(text())=2]`, true}, {`id('a')`, true}} {
		ok, _, err := XMLExists(context.Background(), tc.path, accepted)
		if err != nil || ok != tc.want {
			t.Fatal(tc, ok, err)
		}
		if !accepted.valid || accepted.Original() != s || accepted.RequestedMode() != Content || accepted.EffectiveMode() != Document || accepted.Declaration() != (Declaration{}) || len(accepted.Diagnostics()) != 0 {
			t.Fatal("XPath changed accepted DTD input", tc.path, accepted)
		}
	}
	_, tree, err := parseExpanded(context.Background(), s, Options{Mode: Content})
	if err != nil {
		t.Fatal(err)
	}
	root := dtdRoot(t, tree)
	if len(root.attributes) != 2 || len(root.children) != 3 || root.children[1].kind != elementNode {
		t.Fatal("input collector cutover", root)
	}
	_, tree, _, err = parseConfigured(context.Background(), s, Options{Mode: Document}, parsePolicy{})
	if err != nil || tree != nil {
		t.Fatal("validation retained tree", tree, err)
	}
	for _, tc := range []struct {
		source, path string
		effective Mode
	}{
		{`<!DOCTYPE r [<!ATTLIST r xmlns CDATA 'urn:d'>]><r/>`, `/*[namespace-uri()='urn:d' and namespace::*[name()='' and .='urn:d']]`, Document},
		{`<!DOCTYPE p:r [<!ATTLIST p:r xmlns:p CDATA 'urn:p'>]><p:r/>`, `/*[namespace-uri()='urn:p' and namespace::*[name()='p' and .='urn:p']]`, Document},
		{`<!DOCTYPE r [<!ENTITY e ''>]><r>a&e;b&e;c</r>`, `/r[count(text())=3 and string(.)='abc']`, Document},
		{`<r><![CDATA[x]]><![CDATA[y]]></r>`, `/r[count(text())=1 and text()='xy']`, Content},
	} {
		value, err := Parse(context.Background(), tc.source, Options{Mode: Content})
		if err != nil || !value.valid || value.Original() != tc.source || value.RequestedMode() != Content || value.EffectiveMode() != tc.effective || value.Declaration() != (Declaration{}) || len(value.Diagnostics()) != 0 {
			t.Fatal("policy input spelling/modes/validity", tc, value, err)
		}
		ok, _, err := XMLExists(context.Background(), tc.path, value)
		if err != nil || !ok {
			t.Fatal(tc, ok, err)
		}
		if !value.valid || value.Original() != tc.source || value.RequestedMode() != Content || value.EffectiveMode() != tc.effective || value.Declaration() != (Declaration{}) || len(value.Diagnostics()) != 0 {
			t.Fatal("operation mutated accepted input", tc, value)
		}
	}
}

func TestXMLIDNCNameValidation(t *testing.T) {
	// These lexical boundaries follow the released tree.c/chvalid.c validator,
	// not the broader fifth-edition parser names or a host Unicode category.
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false}, {" \t\r\n", false}, {"a", true}, {"_a-1.2", true},
		{" \ta\r\n", true}, {"a b", false}, {"a b ", false},
		{":a", false}, {"a:b", false}, {"1a", false}, {"\u00a0a", false},
		{"é", true}, {"中", true}, {"〇", true}, {"a٠", true}, {"a\u0300", true},
		{"\u0300a", false}, {"a\u0346", false}, {"\u037f", false}, {"𐀀", false},
		{"々", false}, {"a々", true},
	} {
		p := parser{ctx: context.Background()}
		if got := p.validXMLID(tc.value); got != tc.want || p.err != nil {
			t.Fatal("released NCName lexical boundary", tc, got, p.err)
		}
	}
	for _, tc := range []struct {
		source string
		offset int
	}{{`<r xml:id="a b"/>`, 15}, {`<r xml:id="a b" z="v"/>`, 21}} {
		for _, collect := range []bool{false, true} {
			value, tree, events, err := parseConfigured(context.Background(), tc.source, Options{Mode: Document}, parsePolicy{collect: collect, strictValidity: true})
			var failure *ParseError
			if !errors.As(err, &failure) || value.Valid() || tree != nil || len(events) != 1 || events[0].Condition != "XML_DTD_XMLID_VALUE" || events[0].Domain != DTDValidityDomain || events[0].Severity != Error || events[0].Offset != tc.offset || events[0].Column != tc.offset+1 || events[0].SourceContext != tc.source || events[0].Caret != strings.Repeat(" ", tc.offset)+"^" {
				t.Fatal("strict validity policy depended on collection, used an early cursor or published a partial result", tc, collect, value, tree, events, err)
			}
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	p := parser{ctx: canceled}
	if p.validXMLID("a") || !errors.Is(p.err, context.Canceled) {
		t.Fatal("NCName validation lost cancellation", p.err)
	}
	prior := errors.New("prior hard parser failure")
	p = parser{ctx: context.Background(), err: prior}
	p.xmlIDValue("a")
	if p.err != prior || len(p.events) != 0 {
		t.Fatal("NCName validation replaced an existing hard failure", p.err, p.events)
	}
}
