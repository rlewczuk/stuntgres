package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTimestamptzRangeAggregateAdmission(t *testing.T) {
	e := New()
	d, ok := e.CatalogRead().LookupType(sqltype.TimestamptzRange)
	if !ok {
		t.Fatal("missing range descriptor")
	}
	for _, distinct := range []bool{false, true} {
		_, err := functions.AggregateOrdering(d.Usage(), parser.Position{Character: 27}, distinct)
		diagnostic, ok := err.(*sqlstate.Error)
		kind := "ordering"
		if distinct {
			kind = "DISTINCT"
		}
		if !ok || diagnostic.Code != "0A000" || diagnostic.Message != "aggregate "+kind+" is unsupported for type tstzrange" || diagnostic.Position != "27" {
			t.Fatal("storage-only aggregate admission", err)
		}
	}
}
