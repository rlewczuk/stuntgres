package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func attrsDecorations(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	call := "heap_part.tuple_data_split('attrs_schema'::regclass::oid,NULL::bytea,0,0,NULL::text)"
	for _, tc := range []struct{ query, message string }{
		{"SELECT heap_part.tuple_data_split(DISTINCT 'attrs_schema'::regclass::oid,NULL::bytea,0,0,NULL::text)", "DISTINCT specified, but heap_part.tuple_data_split is not an aggregate function"},
		{"SELECT heap_part.tuple_data_split('attrs_schema'::regclass::oid,NULL::bytea,0,0,NULL::text ORDER BY 1)", "ORDER BY specified, but heap_part.tuple_data_split is not an aggregate function"},
		{"SELECT " + call + " FILTER (WHERE true)", "FILTER specified, but heap_part.tuple_data_split is not an aggregate function"},
		{"SELECT " + call + " OVER ()", "OVER specified, but heap_part.tuple_data_split is not a window function nor an aggregate function"},
	} {
		s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42809", Message: tc.message, Position: fixture.Position(8)})
		s.SelectRows = true
		heapBoth(t, p, b, c, s)
	}
	for _, tc := range []struct{ query, message string }{
		{"SELECT heap_part.tuple_data_split(*)", "function heap_part.tuple_data_split() does not exist"},
		{"SELECT heap_part.tuple_data_split(VARIADIC ARRAY[1])", "function heap_part.tuple_data_split(integer[]) does not exist"},
	} {
		heapBoth(t, p, b, c, heapLookup(tc.query, tc.message, 8))
	}
}
