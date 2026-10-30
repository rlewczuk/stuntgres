package xml

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

func xpathTestEval(t *testing.T, path, input string) (xpathValue, *xpathFrame) {
	t.Helper()
	_, d, _, err := parseConfigured(context.Background(), input, Options{Mode: Document}, parsePolicy{collect: true, strictNamespaces: true, preserveEntities: true, omitDefaultAttributes: true})
	if err != nil {
		t.Fatal(err)
	}
	e, err := compileXPath(context.Background(), path)
	if err != nil {
		t.Fatal(path, err)
	}
	f, err := newXPathTree(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	v := f.eval(e)
	if f.p.err != nil {
		t.Fatal(path, f.p.err)
	}
	return v, f
}
func TestXPathGrammar(t *testing.T) {
	for _, path := range []string{`/`, `//a`, `a/b`, `a//b`, `./a`, `../a`, `@a`, `child::a`, `namespace::*`, `a[p][2]`, `(a|b)[1]/c`, `'literal'`, `"literal"`, `.5`, `1.`, `$name`, `$p:name`, `- - 3`, `1+2*3 div 4 mod 2`, `a=1 or b!=2 and c<=3`, `processing-instruction('p')`, `a[p:*]`, `a[@xml:lang]`, `a[comment()]`, `a[text()]`, `a[node()]`, `a[position()=last()]`, `concat('a','b')`, `a/ancestor-or-self::node()`} {
		if _, err := compileXPath(context.Background(), path); err != nil {
			t.Fatal(path, err)
		}
	}
	for _, path := range []string{``, `[`, `/a/`, `a//`, `/a[]`, `a[1`, `a]`, `+1`, `'open`, `unknown::a`, `a:::b`, `a(,)`, `concat('x',)`, `a b`, `a|`, `$`, `$p:*`, `p:*()`, `a/1`, `a/(b)`, `processing-instruction(1)`, `node('x')`, `a[1]junk`, `a => b`, `a union b`} {
		if _, err := compileXPath(context.Background(), path); err == nil {
			t.Fatal("accepted invalid grammar", path)
		}
	}
	v, _ := xpathTestEval(t, `/r/and | /r/or | /r/div | /r/mod | /r/*`, `<r><and/><or/><div/><mod/></r>`)
	if len(v.nodes) != 4 {
		t.Fatal(v)
	}
}
func TestXPathAxes(t *testing.T) {
	input := `<r xmlns:p='urn:p'><a id='1'/><a id='2'/><a id='3'/></r>`
	for _, tc := range []struct{ path, want string }{
		{`/r/a[3]/preceding-sibling::*[1]/@id`, `2`}, {`(/r/a[3]/preceding-sibling::*)[1]/@id`, `1`}, {`/r/a[1]/following-sibling::*[last()]/@id`, `3`}, {`/r/a[2]/ancestor::*[1]/name`, ``},
		{`/r/a[1]/@id/parent::a/@id`, `1`}, {`/r/a[1]/following::a[1]/@id`, `2`}, {`/r/a[3]/preceding::a[1]/@id`, `2`}, {`/r/a[1]/namespace::*[name()='p']`, `urn:p`},
	} {
		v, f := xpathTestEval(t, tc.path, input)
		if got := f.string(v); got != tc.want {
			t.Fatal(tc.path, got, tc.want)
		}
	}
	v, f := xpathTestEval(t, `/r/a[1]/namespace::*[name()='p'] | /r/a[2]/namespace::*[name()='p']`, input)
	if len(v.nodes) != 2 || v.nodes[0] == v.nodes[1] || v.nodes[0].parent == v.nodes[1].parent {
		t.Fatal("namespace identity", v)
	}
	children := f.tree.root.children[0].children
	for _, n := range children {
		for _, a := range n.attributes {
			if len(f.axis(a, "child")) != 0 {
				t.Fatal("attribute child")
			}
		}
	}
	for _, tc := range []struct{ path, want string }{
		{`count(/*/a/namespace::*[name()=''])`, `1`},
		{`string(/*/a/namespace::*[name()=''])`, ``},
		{`count(/*/a/namespace::*[name()='' and .='']/parent::a)`, `1`},
		{`count(/*/a/namespace::*[name()='' and .='urn:d'])`, `0`},
		{`count(/*/a/b/namespace::*[name()='' and .='']/parent::b)`, `1`},
	} {
		v, f := xpathTestEval(t, tc.path, `<r xmlns='urn:d'><a xmlns=''><b/></a></r>`)
		if got := f.string(v); got != tc.want {
			t.Fatal("default namespace undeclaration", tc.path, got, tc.want)
		}
	}
	empty, _ := xpathTestEval(t, `/*/a/namespace::*[name()=''] | /*/a/b/namespace::*[name()='']`, `<r xmlns='urn:d'><a xmlns=''><b/></a></r>`)
	if len(empty.nodes) != 2 || empty.nodes[0] == empty.nodes[1] || empty.nodes[0].parent == empty.nodes[1].parent {
		t.Fatal("empty namespace owner identity", empty)
	}
	for _, path := range []string{`/r/a[1]/ancestor::r`, `/r/a[1]/ancestor-or-self::a`, `/r/a[1]/self::a`, `/r/child::a`, `/r/descendant::a`, `/r/descendant-or-self::r`, `/r/a/parent::r`, `/r/a/attribute::id`, `/r/a/namespace::*`, `/r/a[1]/following-sibling::a`, `/r/a[3]/preceding-sibling::a`, `/r/a[1]/following::a`, `/r/a[3]/preceding::a`} {
		v, _ := xpathTestEval(t, path, input)
		if len(v.nodes) == 0 {
			t.Fatal("axis empty", path)
		}
	}
	v, f = xpathTestEval(t, `/r`, `<!DOCTYPE r [<!ENTITY e '<a>entity</a>'>]><r>x<![CDATA[y]]>&e;z<!--c--><?p v?></r>`)
	r := v.nodes[0]
	if f.stringValue(r) != "xyentityz" || len(r.children) != 5 || r.children[0].value != "x" || r.children[1].value != "y" || r.children[2].value != "z" {
		t.Fatal("entity dual view", r, f.stringValue(r))
	}
}
func TestXPathConversions(t *testing.T) {
	for _, tc := range []struct {
		path string
		want float64
	}{
		{`1+2*3`, 7}, {`(1+2)*3`, 9}, {`-3 mod 2`, -1}, {`5 div 2`, 2.5}, {`number('-.5')`, -.5}, {`number(' +1 ')`, math.NaN()}, {`number('1e2')`, 100}, {`number('1e')`, 1}, {`number('1e+')`, 1}, {`1e2`, 100}, {`1e`, 1}, {`1e+`, 1}, {`number('-')`, 0}, {`number('0x10')`, math.NaN()}, {`number(' 1')`, math.NaN()}, {`1 div 0`, math.Inf(1)}, {`0 div 0`, math.NaN()}, {`---2`, -2},
	} {
		v, _ := xpathTestEval(t, tc.path, `<r/>`)
		if v.kind != xpathNumber || !(v.number == tc.want || math.IsNaN(v.number) && math.IsNaN(tc.want)) {
			t.Fatal(tc.path, v)
		}
	}
	for _, path := range []string{`a=b and a!=b`, `a=2`, `a<2`, `a=true()`, `not(missing=true())`, `missing=false()`, `not(missing='')`, `not(missing!='')`, `true()=2`, `not(false() and $missing)`, `true() or unknown()`} {
		v, _ := xpathTestEval(t, `/r[`+path+`]`, `<r><a>1</a><a>2</a><b>2</b></r>`)
		if len(v.nodes) != 1 {
			t.Fatal(path, v)
		}
	}
	for _, tc := range []struct{ path, want string }{{`string(0 div 0)`, `NaN`}, {`string(1 div 0)`, `Infinity`}, {`string(-1 div 0)`, `-Infinity`}, {`string(-0)`, `0`}, {`string(.0000001)`, `1e-07`}, {`string(.000001)`, `1e-06`}, {`string(.00001)`, `0.00001`}, {`string(1000000000)`, `1000000000`}, {`string(1000000001)`, `1000000001`}, {`string(2147483648)`, `2.147483648e+09`}, {`string(10000000000)`, `1e+10`}, {`string(1000000000000000000000)`, `1e+21`}, {`string(1 div 3)`, `0.333333333333333`}, {`string(1.2345678901234567)`, `1.234567890123457`}, {`string(/r/a | /r/b)`, `1`}} {
		v, _ := xpathTestEval(t, tc.path, `<r><a>1</a><b>2</b></r>`)
		if v.kind != xpathString || v.text != tc.want {
			t.Fatal(tc.path, v)
		}
	}
	v, _ := xpathTestEval(t, `round(-0.5)`, `<r/>`)
	if v.number != 0 || !math.Signbit(v.number) {
		t.Fatal("round negative zero", v)
	}
}
func TestXPathCoreFunctions(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{`string(last())`, `1`}, {`string(position())`, `1`}, {`string(count(/r/a))`, `2`}, {`name(id('a'))`, `a`}, {`local-name(/r)`, `r`}, {`namespace-uri(/r)`, ``}, {`name(/r/a)`, `a`},
		{`string(/r/a)`, `2`}, {`concat('a','b','c')`, `abc`}, {`string(starts-with('abc','a'))`, `true`}, {`string(contains('abc','bc'))`, `true`}, {`substring-before('abc','b')`, `a`}, {`substring-after('abc','b')`, `c`}, {`substring('12345',1.5,2.6)`, `234`}, {`string(string-length('é𝄞'))`, `2`}, {`normalize-space(' a  b ')`, `a b`}, {`translate('bar','abc','ABC')`, `BAr`},
		{`string(boolean(/r))`, `true`}, {`string(not(/missing))`, `true`}, {`string(true())`, `true`}, {`string(false())`, `false`}, {`string(/r[lang('en')]/@xml:lang)`, `en-US`},
		{`string(number(' 2 '))`, `2`}, {`string(sum(/r/a))`, `5`}, {`string(floor(-1.2))`, `-2`}, {`string(ceiling(-1.2))`, `-1`}, {`string(round(-1.5))`, `-1`},
		{`translate('abca','aabc','XY')`, `XX`}, {`substring('12345',-1 div 0,1 div 0)`, ``}, {`substring('12345',-1 div 0)`, `12345`}, {`normalize-space(' a ')`, ` a `},
	} {
		v, f := xpathTestEval(t, tc.path, `<r xml:lang='en-US'><a xml:id='a'>2</a><a>3</a></r>`)
		if f.string(v) != tc.want {
			t.Fatal(tc.path, v, tc.want)
		}
	}
}
func TestXPathDiagnostics(t *testing.T) {
	for _, tc := range []struct{ path, stage, detail string }{{`[`, `compile`, `Invalid expression`}, {`$missing`, `evaluate`, `Undefined variable`}, {`/p:r`, `evaluate`, `Undefined namespace prefix`}, {`/missing/p:r`, `evaluate`, `Undefined namespace prefix`}, {`unknown()`, `evaluate`, `Unregistered function`}, {`count('x')`, `evaluate`, `Invalid type`}, {`count()`, `evaluate`, `Invalid number of arguments`}, {`concat('x')`, `evaluate`, `Invalid number of arguments`}, {`(1)[1]`, `evaluate`, `Invalid type`}, {`1|/r`, `evaluate`, `Invalid type`}, {`1/a`, `evaluate`, `Invalid type`}} {
		input := accept(t, `<r/>`, Content)
		_, _, err := XMLExists(context.Background(), tc.path, input)
		var xe *XPathError
		if !errors.As(err, &xe) || xe.Stage != tc.stage || xe.Detail != tc.detail {
			t.Fatal(tc, err)
		}
	}
	for _, tc := range []struct{ path, detail string }{{`$p:missing`, ``}, {`p:missing()`, ``}, {`xml:missing()`, `Unregistered function`}, {`unknown($missing)`, `Undefined variable`}, {`count($missing,1)`, `Undefined variable`}} {
		value := accept(t, `<r/>`, Content)
		_, _, err := XMLExists(context.Background(), tc.path, value)
		var xe *XPathError
		if !errors.As(err, &xe) || xe.Stage != "evaluate" || xe.Detail != tc.detail {
			t.Fatal(tc, err)
		}
	}
}
func TestXPathCancellationAndOwnership(t *testing.T) {
	value := accept(t, `<r>`+strings.Repeat(`<a>abc</a>`, 4000)+`</r>`, Content)
	backing := unsafe.StringData(value.original)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := XMLExists(canceled, `/r`, value); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		phase, path string
		after       int
	}{
		{"next", strings.Repeat(" ", 20000) + "/r", 4}, {"project", "/r", 5}, {"axis", "//a", 5}, {"compare", "/r[a='missing']", 5}, {"sourceText", "/r[string(.)='x']", 5}, {"call", "/r[translate(string(.),'abc','ABC')='x']", 5}, {"find", "/r[contains(string(.),'missing')]", 2},
		{"find", "/r[contains(string(.),'" + strings.Repeat("a", 5000) + "')]", 8}, {"xpathParseNumber", "number('" + strings.Repeat("1", 20000) + "')", 2}, {"namespaceCopy", "/r[string(.)='missing']", 10},
	} {
		ctx := &phaseContext{Context: context.Background(), phase: tc.phase, after: tc.after, cause: context.Canceled}
		_, _, err := XMLExists(ctx, tc.path, value)
		if !ctx.admitted || !errors.Is(err, context.Canceled) {
			t.Fatal(tc, ctx.admitted, err)
		}
	}
	query := `/r/a[1]`
	compiled, err := compileXPath(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	saved := xpathSyntaxSnapshot(compiled)
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, input := range []string{`<r><a>one</a></r>`, `<r><a>two</a></r>`} {
				_, d, _, err := parseConfigured(context.Background(), input, Options{Mode: Document}, parsePolicy{collect: true, preserveEntities: true, omitDefaultAttributes: true, strictNamespaces: true})
				if err != nil {
					failures <- err
					return
				}
				f, err := newXPathTree(context.Background(), d)
				if err != nil {
					failures <- err
					return
				}
				v := f.eval(compiled)
				if f.p.err != nil || len(v.nodes) != 1 || f.string(v) != input[6:9] {
					failures <- errors.New("compiled expression retained previous document")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if !reflect.DeepEqual(saved, compiled) || unsafe.StringData(value.original) != backing {
		t.Fatal("mutable ownership")
	}
	for i := range reflect.TypeFor[Result]().NumField() {
		kind := reflect.TypeFor[Result]().Field(i).Type.Kind()
		if kind == reflect.Pointer || kind == reflect.Map || kind == reflect.Interface {
			t.Fatal("Result retained operation graph")
		}
	}
	// Exercise the actual checked diagnostic-growth branch without allocating a
	// gigabyte, then verify its hard-error identity is not a recoverable parse error.
	p := parser{ctx: context.Background(), s: "<r/>", line: 1, column: 1, policy: parsePolicy{strictNamespaces: true}, diagnosticBytes: 0x3ffffffe}
	p.namespaceError("XML_NS_ERR_UNDEFINED_NAMESPACE", "missing")
	var size *SizeError
	if !errors.As(p.err, &size) {
		t.Fatal(p.err)
	}
}

func xpathSyntaxSnapshot(e *xpathExpr) *xpathExpr {
	if e == nil {
		return nil
	}
	copy := *e
	clone := func(expressions []*xpathExpr) []*xpathExpr {
		if expressions == nil {
			return nil
		}
		out := make([]*xpathExpr, len(expressions))
		for i, child := range expressions {
			out[i] = xpathSyntaxSnapshot(child)
		}
		return out
	}
	copy.args, copy.predicates = clone(e.args), clone(e.predicates)
	copy.steps = append([]xpathStep(nil), e.steps...)
	for i := range copy.steps {
		copy.steps[i].predicates = clone(e.steps[i].predicates)
	}
	return &copy
}
