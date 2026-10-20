package engine

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestSettingsLocalNormalizerPublication(t *testing.T) {
	for _, mode := range []string{"cancel", "replace", "inspect"} {
		t.Run(mode, func(t *testing.T) {
			d, err := ValidateSetting(SettingDefinition{Name: "demo.normalizer", Type: SettingString, BootDefault: "b"})
			if err != nil {
				t.Fatal(err)
			}
			var e *Engine
			var s *Session
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// Attach the witness before publication; defaults cannot trigger it.
			d.Normalize = func(input string) (string, *sqlstate.Error, error) {
				if input != "transient" {
					return input, nil, nil
				}
				switch mode {
				case "cancel":
					cancel()
				case "replace":
					if err := e.Reset(context.Background()); err != nil {
						return "", nil, err
					}
				case "inspect":
					// Taking these locks proves staging did not retain publication locks.
					e.mu.Lock()
					e.mu.Unlock()
					s.settings.mu.Lock()
					s.settings.mu.Unlock()
					s.mu.Lock()
					s.mu.Unlock()
				}
				return input, nil, nil
			}
			e = NewWithSettings([]SettingDefinition{d}, "", false)
			s = sessionStart(t, e, nil)
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			result, err := scope.Execute(statement(t, "SELECT set_config('demo.normalizer','transient',true)"), 0, nil)
			finish := scope.Finish(err)
			if mode == "cancel" {
				if !errors.Is(err, context.Canceled) || !errors.Is(finish, context.Canceled) {
					t.Fatal(err, finish)
				}
			} else {
				if err != nil || finish != nil {
					t.Fatal(err, finish)
				}
				values := make([]sqltype.Value, 1)
				present, nextErr := result.Rows.Next(context.Background(), values)
				value, eerr := values[0].Text()
				if !present || nextErr != nil || eerr != nil || value != "transient" {
					t.Fatal(present, nextErr, value, eerr)
				}
				result.Rows.Close()
			}
			localOwnerValue(t, s, "demo.normalizer", "b")
		})
	}
}

func TestSettingsLocalExactTypedUndo(t *testing.T) {
	// Exact hexadecimal subnormals are accepted; rounded decimal ones are not.
	d, err := ValidateSetting(SettingDefinition{Name: "demo.exact", Type: SettingFloating, BootDefault: "0x1p-1023", FloatMax: 10})
	if err != nil {
		t.Fatal(err)
	}
	e := NewWithSettings([]SettingDefinition{d}, "", false)
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "SET demo.exact='0x1p-1074'")
	original := math.Float64bits(s.settings.typed["demo.exact"].Number)
	if original != math.Float64bits(math.SmallestNonzeroFloat64) {
		t.Fatal(original)
	}
	for _, commit := range []bool{false, true} {
		ref := transactionBegin(t, s)
		sessionSQL(t, s, "SET LOCAL demo.exact='2'")
		transactionEnd(t, s, ref, commit)
		if math.Float64bits(s.settings.typed["demo.exact"].Number) != original {
			t.Fatal("undo reparsed lossy SHOW text")
		}
	}
	ref := transactionBegin(t, s)
	sessionSQL(t, s, "SET demo.exact='0x1p-1023'")
	candidate := math.Float64bits(s.settings.typed["demo.exact"].Number)
	if candidate != math.Float64bits(0x1p-1023) {
		t.Fatal("SESSION candidate was not exact")
	}
	if got := tuples(sessionSQL(t, s, "SHOW demo.exact"))[0][0]; got != "1.11254e-308" {
		t.Fatal("expected independently known lossy display", got)
	}
	sessionSQL(t, s, "SET LOCAL demo.exact='2'")
	transactionEnd(t, s, ref, true)
	if math.Float64bits(s.settings.typed["demo.exact"].Number) != candidate {
		t.Fatal("masked candidate lost typed bits")
	}
}
