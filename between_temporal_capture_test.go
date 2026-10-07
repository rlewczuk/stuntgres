package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestBetweenMixedTemporalCapture(t *testing.T) {
	for _, form := range betweenForms {
		t.Run(form, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE temporal_bounds(d DATE,ts TIMESTAMP)")
			execSQL(t, db, "INSERT INTO temporal_bounds VALUES('2000-01-01','2000-01-03')")
			for _, direction := range []struct {
				bounds string
				want   bool
			}{
				{"d AND ts", form == "BETWEEN" || form == "BETWEEN SYMMETRIC"},
				{"ts AND d", form == "NOT BETWEEN" || form == "BETWEEN SYMMETRIC"},
			} {
				expression := "'2000-01-02' " + form + " " + direction.bounds
				query := "SELECT " + expression + " FROM temporal_bounds"
				want := [][]any{{direction.want}}
				rows, err := db.Query(query)
				compareOpsRows(t, rows, err, []string{"BOOL"}, want)
				held, err := db.Prepare(query)
				if err != nil {
					t.Fatal(err)
				}
				for range 3 {
					execSQL(t, db, "UPDATE temporal_bounds SET d=d")
					rows, err := held.Query()
					compareOpsRows(t, rows, err, []string{"BOOL"}, want)
				}
				held.Close()
				filterWant := [][]any{}
				if direction.want {
					filterWant = [][]any{{true}}
				}
				compareOpsDirectPrepared(t, db, "SELECT "+expression+" FROM temporal_bounds WHERE "+expression, []string{"BOOL"}, filterWant)
			}
		})
	}
}
