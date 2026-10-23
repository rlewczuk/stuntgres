package regobj

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// The collation path is read only for a result that returns a non-NULL
// regcollation value, so a failing path fails no other result. A selected
// source reads only its selected rows.
func TestCaptureRowsOutputCollationPathOnDemand(t *testing.T) {
	builtin := func(id sqltype.ID) *sqltype.Descriptor {
		for _, d := range sqltype.Builtins() {
			if d.ID() == id {
				return d
			}
		}
		t.Fatalf("missing builtin type %d", id)
		return nil
	}
	text, regcollation := builtin(sqltype.Text), builtin(sqltype.Regcollation)
	collation, err := sqltype.RegObjectValue(regcollation, 950)
	if err != nil {
		t.Fatal(err)
	}
	unavailable := errors.New("collation path unavailable")
	failingPath := func() ([]ids.ObjectAddress, error) { return nil, unavailable }
	shapes := []struct {
		name string
		fill func(*exec.FinalRows, []sqltype.Value)
	}{
		{"flat", func(rows *exec.FinalRows, values []sqltype.Value) { rows.Values = values }},
		{"anonymous", func(rows *exec.FinalRows, values []sqltype.Value) { rows.Anonymous = [][]sqltype.Value{values} }},
		{"selected source", func(rows *exec.FinalRows, values []sqltype.Value) {
			unselected := slices.Repeat([]sqltype.Value{collation}, len(values))
			rows.Source = &exec.Source{Committed: []storage.RowVersion{{Values: unselected}, {Values: values}}}
			rows.Count, rows.Selection = 1, []int{1}
			for slot := range values {
				rows.Slots = append(rows.Slots, slot)
			}
		}},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			rows := exec.NewFinalRows(nil, 0)
			shape.fill(rows, []sqltype.Value{sqltype.String(text, "x"), sqltype.Null(regcollation)})
			if err := CaptureRowsOutput(rows, catalog.Read{}, nil, authorization.Read{}, nil, failingPath); err != nil {
				t.Fatal("a result without a regcollation value read the collation path", err)
			}
			rows = exec.NewFinalRows(nil, 0)
			shape.fill(rows, []sqltype.Value{sqltype.String(text, "x"), collation})
			if err := CaptureRowsOutput(rows, catalog.Read{}, nil, authorization.Read{}, nil, failingPath); !errors.Is(err, unavailable) {
				t.Fatal("a regcollation result did not report its collation path failure", err)
			}
		})
	}
}
