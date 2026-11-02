package regobj_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRegLookupRelTypeInputUsage(t *testing.T) {
	b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
	if err := b.CaptureTypeInput(b.Type(sqltype.Regtype)); err != nil {
		t.Fatal(err)
	}
	input, ok := b.Input.Regtype.(sqltype.RegtypeUsageResolver)
	if !ok {
		t.Fatal("captured type input lacks declaration capability")
	}
	for _, tc := range []struct {
		input string
		typ   sqltype.ID
		mod   int32
	}{
		{"varchar(12)", sqltype.VarChar, 16}, {"numeric(5,2)", sqltype.Numeric, 327686},
		{"numeric(2,-3)", sqltype.Numeric, 133121}, {"char", sqltype.BPChar, 5},
		{"character", sqltype.BPChar, 5}, {"bit", sqltype.Bit, 1},
		{"pg_catalog.bit", sqltype.Bit, -1}, {"bpchar", sqltype.BPChar, -1},
		{"varbit", sqltype.VarBit, -1}, {"integer", sqltype.Int4, -1},
	} {
		t.Run(tc.input, func(t *testing.T) {
			usage, err := input.ResolveInputUsage(context.Background(), tc.input)
			if err != nil || usage.Type.ID() != tc.typ || usage.Typmod != tc.mod {
				t.Fatal("independent declaration usage", usage, err)
			}
		})
	}
	usage, err := input.ResolveInputUsage(context.Background(), "char[]")
	if err != nil || !usage.Type.IsArray() || usage.Type.Element().ID() != sqltype.BPChar || usage.Typmod != 5 {
		t.Fatal("array element default modifier", usage, err)
	}
	for _, missing := range []string{"", "SETOF integer", "rl_unit_missing(1+2)", "unknown[]"} {
		_, err := input.ResolveInputUsage(context.Background(), missing)
		if err == nil || !sqltype.InputSoftError(err) {
			t.Fatal("destination soft failure lost producer origin", missing, err)
		}
	}
	for _, text := range []string{"23", "-", "integer(4)", "numeric(1+2)", "varchar(0)"} {
		_, err := input.ResolveInputUsage(context.Background(), text)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || sqltype.InputSoftError(err) {
			t.Fatal("declaration grammar/provider failure became soft", text, err)
		}
	}
	// These are OID-input shortcuts only, never declaration modifiers.
	for _, text := range []string{"23", "-"} {
		id, err := b.Input.Regtype.ResolveRegtype(context.Background(), text)
		want := uint32(23)
		if text == "-" {
			want = 0
		}
		if err != nil || id != want {
			t.Fatal("OID-facing input changed", text, id, err)
		}
	}
}
