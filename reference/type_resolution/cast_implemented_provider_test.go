package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func castProviderMatrixValue(t *testing.T, p *fixture.Pair, b fixture.Backend, cell castMatrixCell, value castMatrixValue) castMatrixValue {
	t.Helper()
	// aclparse's omitted grantor is bootstrap principal10. The fixture's pinned
	// PG bootstrap is stuntgres; native predefined principal10 is postgres.
	if b.PG {
		value.output = strings.ReplaceAll(value.output, "=/postgres", "=/stuntgres")
	}
	if cell.source.name != "pg_node_tree" || !cell.contexts[2].admitted || (castPhysicalFact(cell.target).category != "S" && cell.target.name != "pg_node_tree" && cell.target.name != `"char"` && cell.target.name != "regclass") {
		return value
	}
	// This independently observed original is not a cast-derived golden. The
	// physical tree serializations intentionally differ between native and PG.
	var original string
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT "+cell.source.source).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if original == "" {
		t.Fatal("missing original node output")
	}
	value.output = original
	if cell.target.name == `"char"` {
		value.output = original[:1]
	}
	if cell.target.name == "regclass" {
		// The fixed CHECK(n>0) tree output is not a qualified relation name.
		value.diagnostic = &fixture.Diagnostic{Code: "42602", Message: "invalid name syntax"}
	}
	if castPhysicalFact(cell.target).name == "name" {
		n := min(63, len(original))
		for n > 0 && !utf8.ValidString(original[:n]) {
			n--
		}
		value.output = original[:n]
	}
	if cell.target.name == "cast_short" {
		runes := []rune(original)
		value.output = string(runes[:min(3, len(runes))])
	}
	return value
}
