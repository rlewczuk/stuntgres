package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualSecurity(t *testing.T) {
	cases := []struct{ name, setup, columns, message, token string }{
		{"domain", `CREATE DOMAIN vd AS INT`, `a INT,b vd GENERATED ALWAYS AS(a+1) VIRTUAL`, `virtual generated column "b" cannot have a domain type`, ""},
		{"enum", `CREATE TYPE ve AS ENUM('x')`, `a INT,b ve GENERATED ALWAYS AS('x'::ve) VIRTUAL`, `virtual generated column "b" cannot have a user-defined type`, ""},
		{"composite", `CREATE TYPE vt AS(x INT)`, `a INT,b vt GENERATED ALWAYS AS(ROW(a)::vt) VIRTUAL`, `virtual generated column "b" cannot have a user-defined type`, ""},
		{"domain-array", `CREATE DOMAIN vd AS INT`, `a INT,b vd[] GENERATED ALWAYS AS(ARRAY[a::vd]) VIRTUAL`, `virtual generated column "b" cannot have a domain type`, ""},
		{"cast", `CREATE DOMAIN vd AS INT`, `a INT,b INT GENERATED ALWAYS AS((a::vd)::INT) VIRTUAL`, `generation expression uses user-defined type`, "a::"},
		{"null", `CREATE DOMAIN vd AS INT`, `a INT,b INT GENERATED ALWAYS AS((NULL::vd)::INT) VIRTUAL`, `generation expression uses user-defined type`, "NULL::"},
		{"dead", `CREATE DOMAIN vd AS INT`, `a INT,b INT GENERATED ALWAYS AS(CASE WHEN FALSE THEN (a::vd)::INT ELSE a END) VIRTUAL`, `generation expression uses user-defined type`, "a::"},
		{"base", `CREATE DOMAIN vd AS INT`, `d vd,b INT GENERATED ALWAYS AS(d+1) VIRTUAL`, `generation expression uses user-defined type`, "d+1"},
		{"unwrap", `CREATE DOMAIN vd AS INT`, `d vd,b INT GENERATED ALWAYS AS(d::INT+1) VIRTUAL`, `generation expression uses user-defined type`, "d::"},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name, func(t *testing.T) {
				generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec(tc.setup, 0))
					q := `CREATE TABLE g(` + tc.columns + `)`
					detail := "Virtual generated columns that make use of user-defined types are not yet supported."
					if tc.name == "domain" || tc.name == "domain-array" {
						detail = ""
					}
					position := ""
					if tc.token != "" {
						position = fixture.GeneratedStoredPosition(q, tc.token)
					}
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: tc.message, Detail: detail, Position: position})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
					run(fixture.ImplicitError(`SELECT * FROM g`, fixture.Diagnostic{Code: "42P01", Message: `relation "g" does not exist`, Position: "15"}))
					run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
				})
			})
		}
	}
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,b DOUBLE PRECISION GENERATED ALWAYS AS(CASE WHEN FALSE THEN random() ELSE a END) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
		run(fixture.Select(`SELECT b FROM g`, []string{"b"}, []string{"FLOAT8"}, [][]any{{float64(2)}}))
		// SQL function definitions are an external-only native boundary, not an implementation prerequisite.
		if b.PG {
			run(fixture.Exec(`CREATE FUNCTION public.virtual_udf(INT) RETURNS INT LANGUAGE SQL IMMUTABLE AS 'SELECT $1+1'`, 0))
			q := `CREATE TABLE vf(a INT,b INT GENERATED ALWAYS AS(public.virtual_udf(a)) VIRTUAL)`
			run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "generation expression uses user-defined function", Detail: "Virtual generated columns that make use of user-defined functions are not yet supported.", Position: fixture.GeneratedStoredPosition(q, "public.virtual_udf")}))
			run(fixture.Exec(`DROP FUNCTION public.virtual_udf(INT)`, 0))
		}
	})
}
