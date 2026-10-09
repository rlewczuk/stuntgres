package reference_test

import (
	"reflect"
	"testing"
)

func TestReferenceBetweenMixedTemporalCapture(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE temporal_bounds(d DATE,ts TIMESTAMP)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO temporal_bounds VALUES('2000-01-01','2000-01-03')", 1))
	for _, form := range betweenReferenceForms {
		for _, direction := range []struct {
			bounds string
			want   bool
		}{
			{"d AND ts", form == "BETWEEN" || form == "BETWEEN SYMMETRIC"},
			{"ts AND d", form == "NOT BETWEEN" || form == "BETWEEN SYMMETRIC"},
		} {
			expression := "'2000-01-02' " + form + " " + direction.bounds
			query := "SELECT " + expression + " FROM temporal_bounds"
			betweenReferenceBoth(t, p, referenceSelect(query, []string{"?column?"}, []string{"BOOL"}, [][]any{{direction.want}}))
			for _, backend := range p.backends() {
				db := backend.db
				held, err := db.PrepareContext(p.ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := held.Close(); err != nil {
						t.Error(err)
					}
				}()
				for range 3 {
					result, err := db.ExecContext(p.ctx, "UPDATE temporal_bounds SET d=d")
					if err != nil {
						t.Fatal(err)
					}
					if count, err := result.RowsAffected(); err != nil || count != 1 {
						t.Fatal(count, err)
					}
					rows, err := held.QueryContext(p.ctx)
					if err != nil {
						held.Close()
						t.Fatal(err)
					}
					defer rows.Close()
					meta, err := rows.ColumnTypes()
					if err != nil || len(meta) != 1 || meta[0].Name() != "?column?" || meta[0].DatabaseTypeName() != "BOOL" {
						t.Fatal(meta, err)
					}
					scalarAssertMetadata(t, backend.pg, meta)
					if !rows.Next() {
						t.Fatal("missing temporal row", rows.Err())
					}
					var got any
					if err := rows.Scan(&got); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, direction.want) {
						t.Fatal(form, got, direction.want)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("extra temporal row/error", rows.Err())
					}
					rows.Close()
				}
				held.Close()
			}
			want := [][]any{}
			if direction.want {
				want = [][]any{{true}}
			}
			betweenReferenceBoth(t, p, referenceSelect("SELECT "+expression+" FROM temporal_bounds WHERE "+expression, []string{"?column?"}, []string{"BOOL"}, want))
		}
	}
}

func TestReferenceBetweenGroupedDefaults(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE grouped_defaults(b BOOL DEFAULT (TRUE BETWEEN (1<2) AND (2<3)),a BOOL DEFAULT ((TRUE OR FALSE) BETWEEN FALSE AND TRUE),s BOOL DEFAULT (TRUE BETWEEN SYMMETRIC TRUE AND (2 BETWEEN 1 AND 3)),n BOOL DEFAULT (TRUE NOT BETWEEN SYMMETRIC (FALSE OR TRUE) AND (1 BETWEEN 0 AND 2)),o BOOL DEFAULT (TRUE NOT BETWEEN (1 BETWEEN 0 AND 2) AND TRUE))", 0))
	scalarStep(t, p, referenceExec("INSERT INTO grouped_defaults DEFAULT VALUES", 1))
	scalarStep(t, p, referencePrepared(referenceExec("INSERT INTO grouped_defaults DEFAULT VALUES", 1)))
	step := referenceSelect("SELECT * FROM grouped_defaults", []string{"b", "a", "s", "n", "o"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, false, false}, {true, true, true, false, false}})
	step.bag = true
	betweenReferenceBoth(t, p, step)
}

func TestReferenceBetweenPredicateDefaults(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE predicates(n BOOL DEFAULT (TRUE BETWEEN (NULL=TRUE) AND TRUE),i BOOL DEFAULT ((NULL IS NULL) BETWEEN FALSE AND TRUE),t BOOL DEFAULT ((TRUE IS TRUE) BETWEEN FALSE AND TRUE),d BOOL DEFAULT ((1 IS DISTINCT FROM 2) BETWEEN FALSE AND TRUE))", 0))
	scalarStep(t, p, referenceExec("INSERT INTO predicates DEFAULT VALUES", 1))
	scalarStep(t, p, referencePrepared(referenceExec("INSERT INTO predicates DEFAULT VALUES", 1)))
	step := referenceSelect("SELECT * FROM predicates", []string{"n", "i", "t", "d"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{nil, true, true, true}, {nil, true, true, true}})
	step.bag = true
	betweenReferenceBoth(t, p, step)
	scalarStep(t, p, referenceExec("CREATE TABLE deferred(b BOOL DEFAULT (TRUE BETWEEN (1/0=0) AND TRUE))", 0))
	failure := referenceImplicitError("INSERT INTO deferred DEFAULT VALUES", referenceDiagnostic{Code: "22012", Message: "division by zero"})
	failure.errorStage = "execute"
	betweenReferenceBoth(t, p, failure)
	scalarStep(t, p, referenceSelect("SELECT * FROM deferred", []string{"b"}, []string{"BOOL"}, nil))
}
