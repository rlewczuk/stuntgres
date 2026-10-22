package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Exercise the real importer annotation seam with a producer-owned cause,
// independently of catalog lookup and public SQL conformance definitions.
type parameterOriginFailure struct{ cause error }

func (f parameterOriginFailure) ResolveRegclass(context.Context, string) (uint32, error) {
	return 0, f.cause
}

func TestCopyFilterParameterOrigin(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	integer, _ := view.LookupType(sqltype.Int4)
	alias, _ := view.LookupType(sqltype.Regclass)
	parameters := analyze.ParameterInfo{Types: []sqltype.Usage{integer.Usage(), alias.Usage()}}
	for _, tc := range []struct {
		name string
		mark func(error) error
		soft bool
	}{
		{"ordinary", func(err error) error { return err }, false},
		{"soft", sqlstate.SoftInputError, true},
		{"hard", sqlstate.HardInputError, false},
		{"hard over soft", func(err error) error { return sqlstate.HardInputError(sqlstate.SoftInputError(err)) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, raw := range []driver.Value{"missing", []byte("missing"), ""} {
				original := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "missing" does not exist`, Detail: "detail", Hint: "hint", Position: "7", Context: "inner input context"}
				cause := tc.mark(&original)
				dst := make([]sqltype.Value, 2)
				values, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: int64(1)}, {Ordinal: 2, Value: raw}})
				if err != nil {
					t.Fatal(err)
				}
				err = frameImporter(values)(context.Background(), parameters, dst, sqltype.InputEnvironment{Regclass: parameterOriginFailure{cause}})
				want := original
				want.Context += "\nunnamed portal parameter $2 = '...'"
				if text, ok := raw.(string); ok && text == "" {
					want.Context = original.Context + "\nunnamed portal parameter $2 = ''"
				}
				var got *Error
				if !errors.As(err, &got) || *got != want || sqltype.InputSoftError(err) != tc.soft || original.Context != "inner input context" {
					t.Fatalf("raw=%#v diagnostic=%#v err=%v soft=%v", raw, got, err, sqltype.InputSoftError(err))
				}
				if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != original.Code {
					t.Fatal("lost SQLState", err)
				}
				marked, hasOrigin := err.(*sqlstate.InputError)
				if hasOrigin != (tc.name != "ordinary") || (hasOrigin && marked.Recoverable() != tc.soft) {
					t.Fatal("parameter presentation stripped or changed outer origin", err)
				}
				if value, err := dst[0].Integer(); err != nil || value != 1 {
					t.Fatal("earlier parameter import changed", dst[0], err)
				}
			}
		})
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("catalog authority unavailable")} {
		err := frameImporter([]driver.Value{int64(1), "missing"})(context.Background(), parameters, make([]sqltype.Value, 2), sqltype.InputEnvironment{Regclass: parameterOriginFailure{cause}})
		var state interface{ SQLState() string }
		if err != cause || errors.As(err, &state) {
			t.Fatal("ordinary error annotation changed identity or invented SQLState", err)
		}
	}
}
