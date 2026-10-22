package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEarthAssignment(t *testing.T) {
	earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE earth_assign(id integer NOT NULL,v earth)", 0))
		q := "INSERT INTO earth_assign VALUES ($1,$2)"
		held := earthHeld(t, p, c, q)
		defer held.Statement.Close()
		run(fixture.Exec("INSERT INTO earth_assign VALUES (1,'(6378168)')", 1))
		run(fixture.Exec(q, 1, 2, "(6378168,0)"))
		run(fixture.Prepared(fixture.Exec(q, 1, 3, "(6378168,0,0)")))
		buffer := []byte("(-6378168,-0)")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 4, buffer))
		for i := range buffer {
			buffer[i] = '9'
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 5, nil))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 6, []byte(nil)))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 7, "(6378168,NaN)"))
		want := [][]any{{int64(1), "(6378168)"}, {int64(2), "(6378168, 0)"}, {int64(3), "(6378168, 0, 0)"}, {int64(4), "(-6378168, -0)"}, {int64(5), nil}, {int64(6), nil}, {int64(7), "(6378168, NaN)"}}
		run(fixture.Exec("CREATE TABLE earth_copy(id integer NOT NULL,v earth)", 0))
		run(fixture.Exec("INSERT INTO earth_copy SELECT id,v FROM earth_assign", 7))
		run(fixture.Exec("UPDATE earth_copy SET v=v WHERE id=1", 1))
		run(fixture.Prepared(fixture.Exec("UPDATE earth_copy SET v=$1::cube WHERE id=3", 1, "6378174")))
		for _, prepared := range []bool{false, true} {
			s := earthSelect(b, "SELECT id,v FROM earth_assign ORDER BY id", []string{"id", "v"}, want)
			s.Prepared = prepared
			run(s)
			copyWant := append([][]any(nil), want...)
			copyWant[2] = []any{int64(3), "(6378174)"}
			s = earthSelect(b, "SELECT id,v FROM earth_copy ORDER BY id", []string{"id", "v"}, copyWant)
			s.Prepared = prepared
			run(s)
			for _, arg := range []any{nil, []byte(nil), "6378168", []byte("6378168")} {
				value := any("(6378168)")
				if arg == nil {
					value = nil
				} else if bytes, ok := arg.([]byte); ok && bytes == nil {
					value = nil
				}
				s = earthSelect(b, "SELECT $1::earth AS v", []string{"v"}, [][]any{{value}}, arg)
				s.Prepared = prepared
				run(s)
			}
			for _, arg := range []any{"", []byte{}} {
				cubeError(t, p, b, c, "SELECT $1::earth AS v", prepared, true, false, cubeParameterDiagnostic(cubeSyntaxDiagnostic("syntax error at end of input"), "", 1), arg)
			}
			for _, typ := range []string{"text", "varchar"} {
				name := "TEXT"
				if typ == "varchar" {
					name = "VARCHAR"
				}
				s = fixture.Select("SELECT '(6378168)'::earth::"+typ+" AS v", []string{"v"}, []string{name}, [][]any{{"(6378168)"}})
				s.Prepared = prepared
				run(s)
				s = earthSelect(b, "SELECT '(6378168)'::"+typ+"::earth AS v", []string{"v"}, [][]any{{"(6378168)"}})
				s.Prepared = prepared
				run(s)
				prefix := "INSERT INTO earth_assign VALUES (8,"
				displayType := "text"
				if typ == "varchar" {
					displayType = "character varying"
				}
				d := fixture.ControlsReferenceDiagnostic("42804", `column "v" is of type earth but expression is of type `+displayType)
				d.Position, d.Hint = strconv.Itoa(len(prefix)+1), "You will need to rewrite or cast the expression."
				cubeError(t, p, b, c, prefix+"'6378168'::"+typ+")", prepared, false, true, d)
			}
			s = earthSelect(b, "SELECT '(6378168,0)'::cube::earth::cube AS v", []string{"v"}, [][]any{{"(6378168, 0)"}})
			s.Prepared = prepared
			run(s)
		}
		selectQ := "SELECT $1::earth AS v"
		selectHeld := earthHeld(t, p, c, selectQ)
		defer selectHeld.Statement.Close()
		for _, arg := range []any{"6378168", nil, []byte("(6378168,0)")} {
			value := any("(6378168)")
			if arg == nil {
				value = nil
			} else if _, ok := arg.([]byte); ok {
				value = "(6378168, 0)"
			}
			fixture.RunStep(t, p.Ctx, selectHeld, b.Recorder, earthSelect(b, selectQ, []string{"v"}, [][]any{{value}}, arg))
		}
		run(fixture.Exec("CREATE TABLE earth_character(txt text,vc varchar)", 0))
		run(fixture.Exec("INSERT INTO earth_character SELECT v,v FROM earth_assign WHERE id=1", 1))
		run(fixture.Select("SELECT txt,vc FROM earth_character", []string{"txt", "vc"}, []string{"TEXT", "VARCHAR"}, [][]any{{"(6378168)", "(6378168)"}}))
	})
}
