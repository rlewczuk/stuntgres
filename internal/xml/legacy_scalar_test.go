package xml

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
)

func TestXML2LegacyScalar(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		doc, path, want string
		number          float32
		null, boolean   bool
	}{
		{`<r><a>12</a><a>7</a></r>`, `a`, `12`, 12, false, true},
		{`<r><a>12</a></r>`, `r/a`, ``, 0, true, false},
		{`<r><a/></r>`, `a`, ``, 0, true, true},
		{`<r>A<b>B</b>C</r>`, `.`, `ABC`, 0, true, true},
		{`<!DOCTYPE r [<!ENTITY e 'A<b>B</b>'>]><r>&e;C</r>`, `b`, `B`, 0, true, true},
		{`<!DOCTYPE r [<!ATTLIST r a CDATA 'd'>]><r/>`, `@a`, ``, 0, true, false},
		{`<!DOCTYPE r [<!ENTITY e SYSTEM 'file:///no-xml2-file'>]><r>A&e;B</r>`, `.`, `AB`, 0, true, true},
		{`<r xmlns='urn:r'><a>8</a></r>`, `/r/a`, ``, 0, true, false},
		{`<r xmlns='urn:r'><a>8</a></r>`, `/*/*`, `8`, 8, false, true},
		{`<r xml:lang='en'/>`, `@xml:lang`, `en`, 0, true, true},
		{`<?xml version="1.0" encoding="ISO-8859-1"?><r>é</r>`, `.`, `Ã©`, 0, true, true},
		{`<?xml version="1.0" encoding="ISO-8859-1"?><r>&#233;</r>`, `.`, `é`, 0, true, true},
		{"\xef\xbb\xbf<r>é</r>", `string-length(.)`, `1`, 1, false, true},
	} {
		s, err := XPathScalar(ctx, tc.doc, tc.path, LegacyString)
		if err != nil || s.Null || s.Text != tc.want {
			t.Fatalf("string %q/%q: %+v %v", tc.doc, tc.path, s, err)
		}
		n, err := XPathScalar(ctx, tc.doc, tc.path, LegacyNumber)
		if err != nil || n.Null != tc.null || !n.Null && n.Number != tc.number {
			t.Fatalf("number %q/%q: %+v %v", tc.doc, tc.path, n, err)
		}
		b, err := XPathScalar(ctx, tc.doc, tc.path, LegacyBoolean)
		if err != nil || b.Null || b.Boolean != tc.boolean {
			t.Fatalf("boolean %q/%q: %+v %v", tc.doc, tc.path, b, err)
		}
	}
	for _, body := range []struct {
		raw, want     string
		valid, failed bool
	}{
		{"A", "A", true, false}, {"\x1b$B$\"\x1b(B", "あ", false, false}, {"\x1b$X", "", false, true}, {"\x1b$B$", "", false, true},
	} {
		doc := `<?xml version="1.0" encoding="ISO-2022-JP"?><r>` + body.raw + `</r>`
		valid, events, err := ValidateText(ctx, doc, Content)
		if err != nil || valid != body.valid || len(events) != 0 && body.valid {
			t.Fatal("Text/raw distinction", valid, events, err)
		}
		s, err := XPathScalar(ctx, doc, ".", LegacyString)
		if err != nil || s.Null != body.failed || s.Text != body.want {
			t.Fatal("raw string", body, s, err)
		}
		n, err := XPathScalar(ctx, doc, "string-length(.)", LegacyNumber)
		if err != nil || n.Null != body.failed || !body.failed && n.Number != 1 {
			t.Fatal("raw length", body, n, err)
		}
		b, err := XPathScalar(ctx, doc, ".", LegacyBoolean)
		if err != nil || b.Null || b.Boolean == body.failed {
			t.Fatal("raw bool", body, b, err)
		}
	}
	for _, doc := range []string{"", `<r>`, `<r>&unknown;</r>`, `<?xml version='1.0' encoding='NO-SUCH-ENCODING'?><r/>`, `<?xml version='1.0' encoding='UTF-16'?><r/>`, `<?xml version='1.0' encoding='ISO-8859-6'?><r>` + "\xa1" + `</r>`} {
		for _, kind := range []LegacyScalarKind{LegacyString, LegacyNumber, LegacyBoolean} {
			for _, path := range []string{".", "["} {
				v, err := XPathScalar(ctx, doc, path, kind)
				if err != nil || v.Null != (kind != LegacyBoolean) || v.Boolean {
					t.Fatal("document-before-compile projection", doc, path, kind, v, err)
				}
			}
		}
	}
	for _, path := range []string{"/p:r", "$v", "unregistered()"} {
		for _, kind := range []LegacyScalarKind{LegacyString, LegacyNumber, LegacyBoolean} {
			v, err := XPathScalar(ctx, `<r/>`, path, kind)
			if err != nil || v.Null != (kind != LegacyBoolean) || v.Boolean {
				t.Fatal("evaluation projection", path, kind, v, err)
			}
		}
	}
	for _, path := range []string{"", " \t\n"} {
		v, err := XPathScalar(ctx, `<r>A<b>B</b>C</r>`, path, LegacyString)
		if err != nil || v.Null || v.Text != "ABC" {
			t.Fatal("literal wrapper", v, err)
		}
		_, err = XPathScalar(ctx, `<r/>`, path, LegacyNumber)
		var legacy *LegacyXPathError
		if !errors.As(err, &legacy) || legacy.Detail != "Invalid expression" {
			t.Fatal("unwrapped compile", err)
		}
	}
	v, err := XPathScalar(ctx, `<r/>`, "1,2", LegacyString)
	if err != nil || !v.Null {
		t.Fatal("wrapper arity projects evaluation failure", v, err)
	}
	for _, tc := range []struct {
		path           string
		want           float32
		null, negative bool
	}{
		{"16777217", 16777216, false, false}, {"1000000000000000000000000000000000000000", float32(math.Inf(1)), false, false},
		{"1 div 0", float32(math.Inf(1)), false, false}, {"-1 div 0", float32(math.Inf(-1)), false, true},
		{"-0", 0, false, true}, {"-0.000000000000000000000000000000000000000000000000000001", 0, false, true}, {"0 div 0", 0, true, false},
	} {
		n, err := XPathScalar(ctx, `<r/>`, tc.path, LegacyNumber)
		if err != nil || n.Null != tc.null || !tc.null && (n.Number != tc.want || math.Signbit(float64(n.Number)) != tc.negative) {
			t.Fatal("float4", tc, n, err)
		}
	}
	t.Run("DiagnosticPolicies", func(t *testing.T) {
		for _, tc := range []struct {
			doc, message string
			domain       Domain
			severity     Severity
			strict       bool
		}{
			{`<p:r>8</p:r>`, "Namespace prefix p on r is not defined", NamespaceDomain, Error, true},
			{`<r xmlns:xml='urn:wrong' xml:lang='en'>8</r>`, "xml namespace prefix mapped to wrong URI", NamespaceDomain, Error, true},
			{`<r xmlns:p='urn:x' xmlns:q='urn:x' p:a='1' q:a='2'/>`, "Namespaced Attribute a in 'urn:x' redefined", NamespaceDomain, Error, true},
			{`<r xml:id='bad id'>8</r>`, "xml:id : attribute value bad id is not an NCName", DTDValidityDomain, Error, true},
			{`<r xml:space='invalid'>8</r>`, `Invalid value "invalid" for xml:space : "default" or "preserve" expected`, ParserDomain, Warning, false},
			{`<?xml version='1.2'?><r>8</r>`, "Unsupported version '1.2'", ParserDomain, Warning, false},
		} {
			policy := parsePolicy{collect: true, strictNamespaces: true, strictValidity: true, omitDefaultAttributes: true, documentContext: true, legacyDiagnostics: true}
			_, d, events, err := parseConfigured(ctx, tc.doc, Options{Mode: Document, InputKind: RawMemory}, policy)
			if err != nil || d == nil || len(events) != 1 || events[0].Message != tc.message || events[0].Domain != tc.domain || events[0].Severity != tc.severity {
				t.Fatal("legacy accepted events", tc, events, err)
			}
			_, err = XPathScalar(ctx, tc.doc, "[", LegacyString)
			var legacy *LegacyXPathError
			if !errors.As(err, &legacy) || legacy.Detail != events[0].String()+"\nInvalid expression" {
				t.Fatal("all-severity ordered accumulation", legacy, err)
			}
			policy.legacyDiagnostics = false
			_, strictDoc, strictEvents, strictErr := parseConfigured(ctx, tc.doc, Options{Mode: Document, InputKind: RawMemory}, policy)
			if tc.strict && (strictErr == nil || strictDoc != nil) || len(strictEvents) == 0 {
				t.Fatal("strict core contrast", tc, strictDoc, strictEvents, strictErr)
			}
		}
		doc := `<?xml version='1.2'?><r xml:space='invalid'>8</r>`
		_, err := XPathScalar(ctx, doc, "[", LegacyBoolean)
		var accumulated *LegacyXPathError
		want := "line 1: Unsupported version '1.2'\n" + doc + "\n" + strings.Repeat(" ", 19) + "^\n" +
			"line 1: Invalid value \"invalid\" for xml:space : \"default\" or \"preserve\" expected\n" + doc + "\n" + strings.Repeat(" ", 43) + "^\nInvalid expression"
		if !errors.As(err, &accumulated) || accumulated.Detail != want {
			t.Fatal("ordered multiple warnings", err)
		}
		for _, kind := range []RawInputErrorKind{RawUnknownLabel, RawUnavailableCodec, RawInvalidSequence, RawEmptyInput} {
			if !legacyDocumentFailure(&RawInputError{Kind: kind}) {
				t.Fatal(kind)
			}
		}
		ordinary := errors.New("ordinary")
		for _, err := range []error{ordinary, &SizeError{}, &RawInputError{Kind: 99}, &RawInputError{Kind: RawInvalidSequence, Cause: context.Canceled}} {
			if legacyDocumentFailure(err) {
				t.Fatal("ordinary failure projected", err)
			}
		}
		p := parser{ctx: ctx, policy: parsePolicy{legacyDiagnostics: true}, line: 1, column: 1, illFormed: true, saxDisabled: true}
		p.domainEvent("XML_NS_ERR_UNDEFINED_NAMESPACE", "namespace", Error, NamespaceDomain)
		if !p.illFormed || !p.saxDisabled {
			t.Fatal("cleared prior fatality")
		}
	})
}

func TestXML2LegacyCancellation(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, tc := range []struct {
			phase, doc, path string
			kind             LegacyScalarKind
		}{
			{"startRaw", `<r/>`, ".", LegacyString},
			{"iso2022JPPass", `<?xml version='1.0' encoding='ISO-2022-JP'?><r>` + strings.Repeat("\x1b$B$\"\x1b(B", 1000) + `</r>`, ".", LegacyString},
			{"text", `<r>` + strings.Repeat("x", 20000) + `</r>`, ".", LegacyString},
			{"project", `<r>` + strings.Repeat("<a>12</a>", 1000) + `</r>`, ".", LegacyString},
			{"next", `<r/>`, strings.Repeat(" ", 20000) + ".", LegacyNumber},
			{"axis", `<r>` + strings.Repeat("<a>12</a>", 1000) + `</r>`, "//a", LegacyBoolean},
			{"sourceText", `<r>A<b>` + strings.Repeat("x", 20000) + `</b>C</r>`, ".", LegacyString},
			{"xpathParseNumber", `<r>` + strings.Repeat("1", 20000) + `</r>`, ".", LegacyNumber},
		} {
			c := &phaseContext{Context: context.Background(), phase: tc.phase, after: 1, cause: cause}
			got, err := XPathScalar(c, tc.doc, tc.path, tc.kind)
			if !c.admitted || !errors.Is(err, cause) || got != (LegacyScalar{}) {
				t.Fatal("failure atomicity", tc.phase, c.admitted, got, err)
			}
			if _, ok := err.(interface{ SQLState() string }); ok {
				t.Fatal("ordinary error acquired SQLSTATE")
			}
			healthy, err := XPathScalar(context.Background(), `<r>8</r>`, ".", LegacyString)
			if err != nil || healthy.Text != "8" || healthy.Null {
				t.Fatal("next invocation", healthy, err)
			}
		}
		c := &phaseContext{Context: context.Background(), phase: "eval", final: true, cause: cause}
		got, err := XPathScalar(c, `<r/>`, "unregistered()", LegacyBoolean)
		if !c.admitted || !errors.Is(err, cause) || got != (LegacyScalar{}) {
			t.Fatal("evaluation failure hid publication cancellation", got, err)
		}
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 8 {
				v, err := XPathScalar(context.Background(), `<p:r>8</p:r>`, ".", LegacyString)
				if err != nil || v.Text != "8" {
					t.Error(v, err)
				}
				_, err = XPathScalar(context.Background(), `<r/>`, "[", LegacyString)
				var xp *LegacyXPathError
				if !errors.As(err, &xp) || xp.Detail != "Invalid expression" {
					t.Error("leaked invocation events", err)
				}
			}
		}()
	}
	wg.Wait()
}
