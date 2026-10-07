package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestTimeReviewAssignmentIdentity(t *testing.T) {
	e := New()
	for _, tc := range []struct {
		source                      string
		target                      sqltype.ID
		text, destination, identity string
	}{
		{"date", sqltype.Int4, "2000-01-01", "integer", "date"},
		{"date", sqltype.Time, "2000-01-01", "time without time zone", "date"},
		{"time", sqltype.Int4, "04:05", "integer", "time without time zone"},
		{"time", sqltype.Timestamp, "04:05", "timestamp without time zone", "time without time zone"},
		{"timestamp", sqltype.Time, "2003-07-07", "time without time zone", "timestamp without time zone"},
		{"timestamp", sqltype.Int4, "2003-07-07", "integer", "timestamp without time zone"},
	} {
		t.Run(tc.source+"/"+tc.destination, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			d, ok := e.CatalogRead().LookupType(tc.target)
			if !ok {
				t.Fatal("missing target")
			}
			raw := parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: tc.source}, Text: tc.text, Pos: parser.Position{Character: 30}}
			_, err := b.bind(raw, expressionPolicy{context: assignmentContext, expected: d.Usage(), mode: sqltype.Assignment})
			if err == nil {
				t.Fatal("incompatible assignment accepted")
			}
			got := insertValueError(e.CatalogRead(), err, ColumnDescriptor{Name: "v", Usage: d.Usage()}, raw, nil)
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "column \"v\" is of type " + tc.destination + " but expression is of type " + tc.identity, Hint: "You will need to rewrite or cast the expression.", Position: "30"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v want %#v", got, want)
			}
		})
	}
}
