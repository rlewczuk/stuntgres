package xml

import (
	"context"
	"errors"
	"sync"
	"testing"
)

const xsltBuiltinStyleStart = `<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:output method="text"/>`
const xsltBuiltinTextTemplate = `<xsl:template match="text()"><xsl:value-of select="concat(name(..),':',.,':',position(),'/',last(),';')"/></xsl:template>`
const xsltBuiltinStyleEnd = `</xsl:stylesheet>`

func xsltBuiltinRuntime(t *testing.T, source string, program *xsltProgram) *xsltRuntime {
	t.Helper()
	_, doc, err := parseExpanded(context.Background(), source, Options{Mode: Document})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := newXPathTree(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	r := &xsltRuntime{
		program: program, frame: frame, source: frame.tree,
		user: make(map[string]xpathValue), globals: make(map[string]xpathValue),
		globalState: make(map[string]uint8), keysBuilding: make(map[string]bool),
		registrations: []*xsltDocumentInfo{{tree: frame.tree, keys: make(map[string]map[string][]*xpathNode)}},
	}
	frame.environment = &xsltEnvironment{runtime: r, current: frame.node}
	return r
}

func xsltBuiltinRun(r *xsltRuntime) (*xsltResultTree, error) {
	tree := &xsltResultTree{}
	c := xsltContext{node: r.source.root, position: 1, size: 1, scope: xsltNewScope(nil), target: xsltTarget{tree: tree}}
	return tree, r.run([]xsltTask{{kind: xsltApplyTask, context: c}})
}

func TestXSLTBuiltinContext(t *testing.T) {
	builtin := xsltBuiltinStyleStart + xsltBuiltinTextTemplate + xsltBuiltinStyleEnd
	explicit := xsltBuiltinStyleStart + `<xsl:template match="/"><xsl:apply-templates select="r/node()"/></xsl:template>` + xsltBuiltinTextTemplate + xsltBuiltinStyleEnd
	for _, tc := range []struct{ name, source, builtin, explicit string }{
		{"text_cdata_text", `<r>A<![CDATA[B]]>C</r>`, "r:A:1/3;r:B:1/3;r:C:3/3;", "r:A:1/3;r:B:2/3;r:C:3/3;"},
		{"first_cdata", `<r><![CDATA[B]]>C</r>`, "r:B:1/1;r:C:2/2;", "r:B:1/2;r:C:2/2;"},
		{"last_cdata", `<r>A<![CDATA[B]]></r>`, "r:A:1/2;r:B:1/2;", "r:A:1/2;r:B:2/2;"},
		{"only_cdata", `<r><![CDATA[B]]></r>`, "r:B:1/1;", "r:B:1/1;"},
		{"element_before_text", `<r><n/>A<![CDATA[B]]>C</r>`, "r:A:2/4;r:B:2/4;r:C:4/4;", "r:A:2/4;r:B:3/4;r:C:4/4;"},
		{"unmatched_comment_pi", `<r><!--c--><![CDATA[B]]><?p d?>C</r>`, "r:B:1/1;r:C:4/4;", "r:B:2/4;r:C:4/4;"},
		{"nested_restore", `<r><n>X<![CDATA[Y]]>Z</n><![CDATA[B]]>C</r>`, "n:X:1/3;n:Y:1/3;n:Z:3/3;r:B:1/3;r:C:3/3;", "n:X:1/3;n:Y:1/3;n:Z:3/3;r:B:2/3;r:C:3/3;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xsltExpectText(t, tc.source, builtin, "", tc.builtin)
			xsltExpectText(t, tc.source, explicit, "", tc.explicit)
		})
	}
	t.Run("unmatched_ordinary_text", func(t *testing.T) {
		// Ordinary text updates focus only when a matching template is invoked.
		style := xsltBuiltinStyleStart + `<xsl:template match="text()[.='B']"><xsl:value-of select="concat(name(..),':',.,':',position(),'/',last(),';')"/></xsl:template>` + xsltBuiltinStyleEnd
		xsltExpectText(t, `<r>A<![CDATA[B]]>C</r>`, style, "", "Ar:B:1/1;C")
	})
	t.Run("matched_comment_pi", func(t *testing.T) {
		// Unlike unmatched leaves, matched comments/PIs establish child focus.
		style := xsltBuiltinStyleStart + xsltBuiltinTextTemplate + `<xsl:template match="comment()">comment<xsl:value-of select="concat(position(),'/',last(),';')"/></xsl:template><xsl:template match="processing-instruction()">pi<xsl:value-of select="concat(position(),'/',last(),';')"/></xsl:template>` + xsltBuiltinStyleEnd
		xsltExpectText(t, `<r><!--c--><![CDATA[B]]><?p d?><![CDATA[D]]>E</r>`, style, "", "comment1/5;r:B:1/5;pi3/5;r:D:3/5;r:E:5/5;")
	})
	t.Run("mode_scope_current_key_fragment", func(t *testing.T) {
		style := xsltBuiltinStyleStart + `<xsl:key name="k" match="n" use="@id"/><xsl:template match="/"><xsl:apply-templates select="r" mode="m"><xsl:with-param name="p" select="'X'"/></xsl:apply-templates></xsl:template><xsl:template match="text()">wrong-mode</xsl:template><xsl:template match="text()" mode="m"><xsl:param name="p" select="'D'"/><xsl:variable name="v"><v><xsl:value-of select="."/></v></xsl:variable><xsl:value-of select="concat(name(current()/..),':',$p,':',string($v),':',key('k','x'),':',position(),'/',last(),';')"/></xsl:template>` + xsltBuiltinStyleEnd
		xsltExpectText(t, `<r><n id="x">K</n>A<![CDATA[B]]>C</r>`, style, "", "n:X:K:K:1/1;r:X:A:K:2/4;r:X:B:K:2/4;r:X:C:K:4/4;")
	})
}

func TestXSLTBuiltinCancellation(t *testing.T) {
	style := xsltBuiltinStyleStart + xsltBuiltinTextTemplate + xsltBuiltinStyleEnd
	for _, phase := range []string{"builtinChild", "selectTemplate"} {
		t.Run(phase, func(t *testing.T) {
			ctx := &phaseContext{Context: context.Background(), phase: phase, after: 4, cause: context.Canceled}
			out, err := ProcessXSLT(ctx, `<r><n>X<![CDATA[Y]]>Z</n><![CDATA[B]]>C</r>`, style, "")
			if !ctx.admitted || !errors.Is(err, context.Canceled) || out != (XSLTResult{}) {
				t.Fatalf("admitted=%v result=%+v error=%v", ctx.admitted, out, err)
			}
			if _, ok := err.(interface{ SQLState() string }); ok {
				t.Fatal("ordinary cancellation acquired SQLSTATE")
			}
			xsltExpectText(t, `<r><![CDATA[B]]>C</r>`, style, "", "r:B:1/1;r:C:2/2;")
		})
	}
	// Even an interrupted continuation releases invocation focus and run depth;
	// no result is published by the public boundary after this private failure.
	r := xsltBuiltinRuntime(t, `<r>A<![CDATA[B]]>C</r>`, xsltTestProgram(t, style))
	r.focus = xsltFocus{position: 7, size: 11}
	ctx := &phaseContext{Context: context.Background(), phase: "builtinChild", after: 3, cause: context.DeadlineExceeded}
	r.frame.p.ctx = ctx
	_, err := xsltBuiltinRun(r)
	if !ctx.admitted || !errors.Is(err, context.DeadlineExceeded) || r.runDepth != 0 || r.focus != (xsltFocus{position: 7, size: 11}) {
		t.Fatalf("canceled focus=%+v depth=%d error=%v", r.focus, r.runDepth, err)
	}
}

func TestXSLTBuiltinOwnership(t *testing.T) {
	style := xsltBuiltinStyleStart + xsltBuiltinTextTemplate + xsltBuiltinStyleEnd
	program := xsltTestProgram(t, style)
	left := xsltBuiltinRuntime(t, `<r><n>X<![CDATA[Y]]>Z</n><![CDATA[B]]>C</r>`, program)
	right := xsltBuiltinRuntime(t, `<r><![CDATA[Q]]>R</r>`, program)
	left.focus, right.focus = xsltFocus{position: 7, size: 11}, xsltFocus{position: 13, size: 17}
	leftRoot, rightRoot := left.source.root.children[0], right.source.root.children[0]
	cdata := leftRoot.children[1]
	if cdata.kind != xpathText || cdata.source == nil || cdata.source.kind != cdataNode || cdata.owner != left.source || cdata.parent != leftRoot {
		t.Fatal("projection lost real CDATA provenance or document ownership")
	}
	if left.frame == right.frame || left.source == right.source || leftRoot == rightRoot || left.registrations[0] == right.registrations[0] {
		t.Fatal("independent invocations share mutable execution state")
	}
	leftEnvironment, rightEnvironment := left.frame.environment, right.frame.environment
	var trees [2]*xsltResultTree
	var failures [2]error
	var group sync.WaitGroup
	for at, r := range []*xsltRuntime{left, right} {
		group.Add(1)
		go func() {
			defer group.Done()
			trees[at], failures[at] = xsltBuiltinRun(r)
		}()
	}
	group.Wait()
	for at, r := range []*xsltRuntime{left, right} {
		if failures[at] != nil || r.failed || r.runDepth != 0 || r.frame.node != r.source.root || r.frame.position != 1 || r.frame.size != 1 {
			t.Fatalf("invocation %d failed/restored incorrectly: %v", at, failures[at])
		}
	}
	if left.focus != (xsltFocus{position: 7, size: 11}) || right.focus != (xsltFocus{position: 13, size: 17}) || left.frame.environment != leftEnvironment || right.frame.environment != rightEnvironment {
		t.Fatal("continuations did not restore each invocation's actual caller context")
	}
	if trees[0] == trees[1] || trees[0].Children[0] == trees[1].Children[0] || cdata != leftRoot.children[1] || cdata.source.kind != cdataNode || len(leftRoot.children) != 3 || len(leftRoot.children[0].children) != 3 {
		t.Fatal("result ownership or original text/CDATA boundaries changed")
	}
	for at, want := range []string{"n:X:1/3;n:Y:1/3;n:Z:3/3;r:B:1/3;r:C:3/3;", "r:Q:1/1;r:R:2/2;"} {
		r := []*xsltRuntime{left, right}[at]
		if got := xsltTreeString(&r.frame.p, trees[at]); got != want || r.frame.p.err != nil {
			t.Fatalf("invocation %d text=%q want=%q error=%v", at, got, want, r.frame.p.err)
		}
	}
}
