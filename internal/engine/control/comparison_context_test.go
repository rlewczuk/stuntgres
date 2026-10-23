package control

import (
	"bytes"
	"context"
	"encoding/base64"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"testing"
)

func TestComparisonContextRecords(t *testing.T) {
	encode := func(s string) string {
		return "stuntgres:comparison-binding " + base64.StdEncoding.EncodeToString([]byte(s))
	}
	for _, tc := range []struct{ comment, kind, name string }{
		{ComparisonContextBegin, "begin", ""},
		{ComparisonContextEnd, "finish", ""},
		{ComparisonBindingEnd, "close", ""},
		{encode(`{"namespace":"Cube \"Namespace\""}`), "bind", `Cube "Namespace"`},
	} {
		kind, name, recognized, err := DecodeComparisonContext(tc.comment)
		if err != nil || !recognized || kind != tc.kind || name != tc.name {
			t.Fatalf("%q: %q %q %v %v", tc.comment, kind, name, recognized, err)
		}
	}
	for _, comment := range []string{
		"stuntgres:comparison-context", "stuntgres:comparison-context begin extra", "stuntgres:comparison-binding", "stuntgres:comparison-binding !!!",
		encode(`{}`), encode(`{"namespace":""}`), encode(`{"namespace":null}`), encode(`{"namespace":3}`), encode(`{"other":"public"}`),
		encode(`{"namespace":"public","namespace":"other"}`), encode(`{"namespace":"public","extra":true}`), encode(`{"namespace":"public"} {}`),
	} {
		_, _, recognized, err := DecodeComparisonContext(comment)
		if !recognized || err == nil {
			t.Fatalf("accepted malformed comparison marker %q", comment)
		}
	}
	for _, comment := range []string{"ordinary user comment", "stuntgres:routine-body-check"} {
		_, _, recognized, err := DecodeComparisonContext(comment)
		if recognized || err != nil {
			t.Fatalf("claimed unrelated comment %q", comment)
		}
	}
}

func TestComparisonContextNoWindowBytes(t *testing.T) {
	var out bytes.Buffer
	w := dumpWriter{ctx: context.Background(), dst: &out}
	const declaration = "CREATE TABLE public.plain(v integer);\n"
	const metadata = "COMMENT ON TABLE public.plain IS 'unchanged';\n"
	if err := w.emitDefinition(catalog.Read{}, dump.Statement{SQL: declaration, FollowingSQL: metadata}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != declaration+metadata || w.comparisonContext {
		t.Fatalf("plain dump changed: %q context=%v", got, w.comparisonContext)
	}
}
