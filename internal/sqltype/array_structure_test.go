package sqltype

import (
	"context"
	"testing"
)

func TestArrayStructuralTokenBoundaries(t *testing.T) {
	d := testTypes()[1007]
	for _, tc := range []struct{ input, detail string }{
		{"[1:2][1:1]={{1},{2,3}}", "Specified array dimensions do not match array contents."},
		{"[1:1]={{oops}}", "Specified array dimensions do not match array contents."},
		{"{{1},{{oops}}}", "Multidimensional arrays must have sub-arrays with matching dimensions."},
		{"[1:2][1:1]={{1,2},{oops}}", "Specified array dimensions do not match array contents."},
		{"{{NULL},{{oops}}}", "Multidimensional arrays must have sub-arrays with matching dimensions."},
		{"[1:2][1:1]={{},{oops}}", "Specified array dimensions do not match array contents."},
		{"{{1},{2,3},{oops}}", "Multidimensional arrays must have sub-arrays with matching dimensions."},
	} {
		_, err := d.Input(context.Background(), tc.input, -1)
		arrayUnitDiagnostic(t, err, "22P02", `malformed array literal: "`+tc.input+`"`, tc.detail)
	}
	for _, input := range []string{"[1:1][1:1]={oops}", "{{1},oops}", "[1:2][1:1]={{oops,2},{3}}", "{{1},{oops,2}}", "{{},{{oops}}}"} {
		_, err := d.Input(context.Background(), input, -1)
		arrayUnitDiagnostic(t, err, "22P02", `invalid input syntax for type integer: "oops"`, "")
	}
	// MAXDIM precedes frozen-rank validation at the seventh opening brace.
	_, err := d.Input(context.Background(), "[1:1][1:1][1:1][1:1][1:1][1:1]={{{{{{{oops}}}}}}}", -1)
	arrayUnitDiagnostic(t, err, "54000", "number of array dimensions exceeds the maximum allowed (6)", "")
	for _, tc := range []struct{ input, want string }{
		{"[-1:0][3:4]={{NULL,1},{2,NULL}}", "[-1:0][3:4]={{NULL,1},{2,NULL}}"},
		{"{{NULL,1},{2,NULL}}", "{{NULL,1},{2,NULL}}"},
		{"{{},{}}", "{}"},
	} {
		value, err := d.Input(context.Background(), tc.input, -1)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		got, err := d.Output(context.Background(), value)
		if err != nil || got != tc.want {
			t.Fatal(tc.input, got, err)
		}
	}
}
