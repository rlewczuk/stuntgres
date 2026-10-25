package enginetest

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

// IndependentFinalRows returns a new cursor over the rows of original, so a
// test can read them without moving the original result's public position.
func IndependentFinalRows(original exec.RowResult) *exec.FinalRows {
	old := original.(*exec.FinalRows)
	r := exec.NewFinalRows(old.Columns, old.Count)
	r.Values, r.Selection, r.Slots = old.Values, old.Selection, old.Slots
	r.Anonymous = old.Anonymous
	r.RegclassOutput = old.RegclassOutput
	if old.Source != nil {
		source := *old.Source
		if source.View != nil {
			source.View = source.View.Retain()
		}
		r.Source = &source
	}
	return r
}

// Tuples reads every row of r through an independent cursor and exports each
// value with Export.
func Tuples(r engine.Result) [][]driver.Value {
	cursor := IndependentFinalRows(r.Rows)
	defer cursor.Close()
	rows := make([][]driver.Value, 0, cursor.Count)
	values := make([]sqltype.Value, len(cursor.Schema()))
	for {
		present, err := cursor.Next(context.Background(), values)
		if err != nil {
			panic(err)
		}
		if !present {
			break
		}
		row := make([]driver.Value, len(values))
		for j, v := range values {
			row[j] = Export(v)
		}
		rows = append(rows, row)
	}
	return rows
}

// Export converts v to a driver value: nil for NULL, the output text for
// arrays and enums, int64 for integers, string for text types and bool for
// booleans. It panics on any other type.
func Export(v sqltype.Value) driver.Value {
	if v.IsNull() {
		return nil
	}
	if v.Type().BaseType().IsArray() || v.Type().BaseType().IsEnum() {
		s, err := v.Type().Output(context.Background(), v)
		if err != nil {
			panic(err)
		}
		return s
	}
	switch v.Type().ID() {
	case sqltype.Int2, sqltype.Int4, sqltype.Int8:
		n, err := v.Integer()
		if err != nil {
			panic(err)
		}
		return n
	case sqltype.Text, sqltype.BPChar, sqltype.VarChar, sqltype.Name:
		s, err := v.Text()
		if err != nil {
			panic(err)
		}
		return s
	case sqltype.Bool:
		b, err := v.Boolean()
		if err != nil {
			panic(err)
		}
		return b
	}
	panic("invalid test value")
}

// Usage returns the usage of the published type id.
func Usage(e *engine.Engine, id sqltype.ID) sqltype.Usage {
	d, ok := e.CatalogRead().LookupType(id)
	if !ok {
		panic("missing type")
	}
	return d.Usage()
}

// State fails the test unless err carries SQLSTATE want.
func State(t testing.TB, err error, want string) {
	t.Helper()
	var s interface{ SQLState() string }
	if !errors.As(err, &s) || s.SQLState() != want {
		t.Fatalf("error %v, want SQLSTATE %s", err, want)
	}
}
