package stuntgres_test

import (
	"context"
	"stuntgres"
	"testing"
)

func TestSequencePreparedInvalidation(t *testing.T) {
	t.Run("Rename", func(t *testing.T) {
		for _, query := range []string{"SELECT nextval('s')", "SELECT nextval('s'::regclass)"} {
			t.Run(query, func(t *testing.T) {
				ctx := context.Background()
				c := sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
				sequenceExec(t, c, "CREATE SEQUENCE s START 14 CACHE 1")
				held, err := c.PrepareContext(ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				sequenceExec(t, c, "ALTER SEQUENCE s RENAME TO identity")
				var value int64
				err = held.QueryRowContext(ctx).Scan(&value)
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "s" does not exist`, Position: "16"})
				missing, prepareErr := c.PrepareContext(ctx, query)
				if missing != nil {
					missing.Close()
					t.Fatal("missing name Prepare returned a statement")
				}
				typeCoreDiagnostic(t, prepareErr, stuntgres.Error{Code: "42P01", Message: `relation "s" does not exist`, Position: "16"})
				parameter, err := c.PrepareContext(ctx, "SELECT nextval($1)")
				if err != nil {
					t.Fatal(err)
				}
				defer parameter.Close()
				if err = parameter.QueryRowContext(ctx, "identity").Scan(&value); err != nil || value != 14 {
					t.Fatalf("parameter got=%d want=14 err=%v; failed held query must not allocate", value, err)
				}
				sequenceExec(t, c, "CREATE SEQUENCE s START 70")
				if err = held.QueryRowContext(ctx).Scan(&value); err != nil || value != 70 {
					t.Fatal("invalidated source did not resolve alternate same-name sequence", value, err)
				}
				sequenceValue(t, c, "SELECT nextval('identity')", 15)
			})
		}
	})
	sequencePreparedMatrix(t)
	sequencePreparedMetadata(t)
	sequencePreparedNamespace(t)
	t.Run("NativeControlsTemporary", TestSequenceTemporaryPreparedIdentitySurvivesPersistentControls)
}
