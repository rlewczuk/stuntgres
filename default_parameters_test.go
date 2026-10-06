package stuntgres_test

import (
	"errors"
	"fmt"
	"stuntgres"
	"testing"
)

func TestDefaultParameterUtilityArgumentBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			q := `CREATE TABLE bad (i integer DEFAULT $1)`
			var supplied, semantic error
			if prepared {
				stmt, err := db.Prepare(q)
				if err != nil {
					t.Fatal("utility Prepare", err)
				}
				defer stmt.Close()
				_, supplied = stmt.Exec(7)
				_, semantic = stmt.Exec()
			} else {
				_, supplied = db.Exec(q, 7)
				_, semantic = db.Exec(q)
			}
			var state interface{ SQLState() string }
			if supplied == nil || errors.As(supplied, &state) {
				t.Fatal("ordinary adapter count boundary changed", supplied)
			}
			typeCoreDiagnostic(t, semantic, stuntgres.Error{Code: "42P02", Message: "there is no parameter $1", Position: "37"})
		})
	}
}
