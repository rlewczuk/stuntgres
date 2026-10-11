package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNotNullDropSystemColumnDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) { s.Prepared = prepared; p.Step(t, s) }
			run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT a_nn NOT NULL,oid INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(7,9)", 1))
			for _, column := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
				s := fixture.ImplicitError("ALTER TABLE t ALTER "+column+" DROP NOT NULL", fixture.Diagnostic{Code: "0A000", Message: `cannot alter system column "` + column + `"`})
				s.ErrorStage = "execute"
				run(s)
			}
			for _, tc := range []struct{ column, name string }{{"missing", "missing"}, {`"CTID"`, "CTID"}} {
				s := fixture.ImplicitError("ALTER TABLE t ALTER "+tc.column+" DROP NOT NULL", fixture.Diagnostic{Code: "42703", Message: `column "` + tc.name + `" of relation "t" does not exist`})
				s.ErrorStage = "execute"
				run(s)
			}
			run(fixture.Exec("ALTER TABLE t ALTER oid DROP NOT NULL", 0))
			run(fixture.ImplicitError("UPDATE t SET a=NULL", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, 9).", Schema: "public", Table: "t", Column: "a"}))
			run(fixture.Select("SELECT * FROM t", []string{"a", "oid"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
		})
	}
}

func TestReferenceNotNullSystemColumnSetAddBoundaries(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(a INT)", 0))
			for _, backend := range p.Backends() {
				for _, tc := range []struct{ q, native, postgres string }{
					{"ALTER TABLE t ALTER ctid SET NOT NULL", "system-column nullability is unsupported", `cannot alter system column "ctid"`},
					{"ALTER TABLE t ADD NOT NULL ctid", `cannot add not-null constraint on system column "ctid"`, `cannot add not-null constraint on system column "ctid"`},
				} {
					message := tc.native
					if backend.PG {
						message = tc.postgres
					}
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: message})
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			}
		})
	}
}
