package reference_test

import "testing"

func TestReferenceXMLExistsAxes(t *testing.T) {
	const document = `<r><g id="g1"><a id="1"/><a id="2"/><a id="3"/></g><g id="g2"><b/></g></r>`
	cases := []struct {
		path string
		want bool
	}{
		{`/r/g[@id='g1']/child::a[2][@id='2']`, true}, {`/r/g[@id='g1']/child::a[2][@id='1']`, false},
		{`/r/g/a[@id='3']/preceding-sibling::*[1][@id='2']`, true}, {`/r/g/a[@id='3']/preceding-sibling::*[1][@id='1']`, false},
		{`(/r/g/a[@id='3']/preceding-sibling::*)[1][@id='1']`, true}, {`(/r/g/a[@id='3']/preceding-sibling::*)[1][@id='2']`, false},
		{`/r/g/a[@id='1']/following-sibling::*[last()][@id='3']`, true}, {`/r/g/a[@id='1']/following-sibling::*[last()][@id='2']`, false},
		{`/r/g/a[@id='1']/parent::g[@id='g1']`, true}, {`/r/g/a[@id='1']/parent::g[@id='g2']`, false},
		{`/r/g/a[@id='1']/ancestor::g[@id='g1']`, true}, {`/r/g/a[@id='1']/ancestor::g[@id='g2']`, false},
		{`/r/g/a[@id='1']/ancestor-or-self::a[@id='1']`, true}, {`/r/g/a[@id='1']/ancestor-or-self::b`, false},
		{`/r/g/a/self::a`, true}, {`/r/g/a/self::b`, false}, {`/r/descendant::b`, true}, {`/r/descendant::r`, false},
		{`/r/descendant-or-self::r`, true}, {`/r/descendant-or-self::missing`, false}, {`/r/g[@id='g1']/following::b`, true}, {`/r/g[@id='g1']/following::a`, false},
		{`/r/g[@id='g2']/preceding::a[@id='3']`, true}, {`/r/g[@id='g2']/preceding::r`, false},
		{`/r/g/attribute::id`, true}, {`/r/g/child::id`, false}, {`/r/g/@id/parent::g[@id='g1']`, true}, {`/r/g/@id/parent::a`, false},
		{`//a[2][@id='2']`, true}, {`//a[2][@id='3']`, false}, {`/r/g/a[@id='3']/preceding-sibling::*[last()][@id='1']`, true},
		{`/r/g/a[@id='1']/../a[2]/@id[.='2']`, true},
	}
	xmlExistsCases(t, document, cases)
}
func TestReferenceXMLExistsNodeModel(t *testing.T) {
	for _, tc := range []struct {
		name, document string
		cases          []struct {
			path string
			want bool
		}
	}{
		{"namespaces", `<r xmlns:p='urn:p'><a/><b xmlns:p='urn:q'/></r>`, []struct {
			path string
			want bool
		}{
			{`/r/a/namespace::*[name()='p' and .='urn:p']`, true}, {`/r/a/namespace::*[name()='p' and .='urn:q']`, false}, {`/r/a/namespace::*[name()='xml' and .='http://www.w3.org/XML/1998/namespace']`, true}, {`/r/a/namespace::*[name()='p']/parent::a`, true}, {`/r/b/namespace::*[name()='p' and .='urn:q']`, true},
		}},
		{"default-namespace", `<r xmlns='urn:d' id='x'><a xmlns=''/></r>`, []struct {
			path string
			want bool
		}{
			{`/r`, false}, {`/*[local-name()='r' and namespace-uri()='urn:d']/@id[.='x']`, true}, {`/*/a`, true}, {`/*/a/namespace::*[name()='']`, true}, {`/*/a/namespace::*[name()='' and .='']`, true}, {`/*/a/namespace::*[name()='' and .='']/parent::a`, true}, {`/*/a/namespace::*[name()='' and .='urn:d']`, false}, {`/*/namespace::*[name()='' and .='urn:d']`, true},
			{`/*[count(namespace::*[name()=''] | a/namespace::*[name()=''])=2]`, true}, {`/*[count(namespace::*[name()=''] | a/namespace::*[name()=''])=1]`, false},
		}},
		{"kinds", `<!DOCTYPE r [<!ELEMENT r ANY>]><r>x<![CDATA[y]]>z<!--c--><?p v?><a/>t</r>`, []struct {
			path string
			want bool
		}{
			{`/r[count(text())=4 and text()[1]='x' and text()[2]='y' and text()[3]='z' and string(.)='xyzt']`, true}, {`/r[count(text())=2]`, false}, {`/r/comment()[.='c']`, true}, {`/r/processing-instruction('p')[.='v']`, true}, {`/r/processing-instruction('missing')`, false}, {`/r[count(node())=7]`, true}, {`/r/processing-instruction()[name()='p']`, true}, {`(/)[count(node())=1 and string(.)='xyzt']`, true},
		}},
		{"adjacent-cdata", `<r><![CDATA[x]]><![CDATA[y]]></r>`, []struct {
			path string
			want bool
		}{{`/r[count(text())=1 and text()[1]='xy' and string(.)='xy']`, true}, {`/r[count(text())=2]`, false}}},
		{"utf8-memory-entry", "\uFEFF<r/>", []struct {
			path string
			want bool
		}{{`/r`, true}}},
		{"defaults", `<!DOCTYPE r [<!ATTLIST r d CDATA 'v'>]><r/>`, []struct {
			path string
			want bool
		}{{`/r/@d`, false}, {`/r[@d='v']`, false}}},
		{"defaulted-namespace", `<!DOCTYPE r [<!ATTLIST r xmlns CDATA 'urn:d'>]><r/>`, []struct {
			path string
			want bool
		}{{`/*[namespace-uri()='urn:d']`, true}, {`/*/namespace::*[name()='' and .='urn:d']`, true}, {`/r`, false}}},
		{"defaulted-prefixed-namespace", `<!DOCTYPE p:r [<!ATTLIST p:r xmlns:p CDATA 'urn:p'>]><p:r/>`, []struct {
			path string
			want bool
		}{{`/*[namespace-uri()='urn:p']`, true}, {`/*/namespace::*[name()='p' and .='urn:p']`, true}}},
		{"entity-text", `<!DOCTYPE r [<!ENTITY e 'abc'>]><r>&e;</r>`, []struct {
			path string
			want bool
		}{{`/r/text()`, false}, {`/r[string(.)='abc']`, true}}},
		{"entity-markup", `<!DOCTYPE r [<!ENTITY e '<a>abc</a>'>]><r>&e;</r>`, []struct {
			path string
			want bool
		}{{`/r/a`, false}, {`/r[string(.)='abc']`, true}, {`/r/node()`, false}}},
		{"entity-boundaries", `<!DOCTYPE r [<!ENTITY e 'b'>]><r>a&e;c</r>`, []struct {
			path string
			want bool
		}{{`/r[count(text())=2 and text()[1]='a' and text()[2]='c' and string(.)='abc']`, true}, {`/r[text()='b']`, false}}},
		{"xml-id", `<r xml:id='a'/>`, []struct {
			path string
			want bool
		}{{`id('a')`, true}, {`id('missing')`, false}, {`/r/@xml:id`, true}}},
		{"dtd-id", `<!DOCTYPE r [<!ATTLIST r id ID #IMPLIED>]><r id='a'/>`, []struct {
			path string
			want bool
		}{{`id('a')`, true}, {`id('missing')`, false}}},
		{"non-id", `<r id='a'/>`, []struct {
			path string
			want bool
		}{{`id('a')`, false}}},
		{"encoding-declaration", `<?xml version='1.0' encoding='ISO-8859-1'?><r/>`, []struct {
			path string
			want bool
		}{{`/r`, true}}},
		{"normalized", "<r a='x\ny'>x\r\ny\rz</r>", []struct {
			path string
			want bool
		}{{"/r[@a='x y' and string(.)='x\ny\nz']", true}}},
	} {
		t.Run(tc.name, func(t *testing.T) { xmlExistsCases(t, tc.document, tc.cases) })
	}
}
