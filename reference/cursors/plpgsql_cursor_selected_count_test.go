package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Source-grounded direction/count conflict witnesses with independently expected
// outcomes observed on the pinned oracle.
func TestReferencePLpgSQLCursorSelectedCount(t *testing.T) {
	for _, kind := range []string{"counted_fetch", "counted_move", "omitted_fetch", "omitted_move"} {
		separators := []string{"FROM"}
		if strings.HasPrefix(kind, "omitted") {
			separators = append(separators, "IN")
		}
		for _, direction := range []string{"FORWARD", "BACKWARD"} {
			for _, separator := range separators {
				t.Run(kind+"_"+direction+"_"+separator, func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, err := b.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer c.Close()
							plCursorRows(t, p, b, c)
							cursor := "c"
							count := "reverse('1')::integer "
							if strings.HasPrefix(kind, "omitted") {
								cursor = `"reverse"`
								count = ""
							}
							declarations := "DECLARE " + cursor + " SCROLL CURSOR FOR SELECT i FROM pc_rows ORDER BY i; x integer; "
							motion := direction + " " + count + separator + " " + cursor
							if kind == "counted_fetch" {
								// PL FETCH rejects every expression-count form, even1.
								// Parsing it as a bare cursor name would select a different
								// diagnostic and cannot satisfy this complete record.
								body := "\n-- é selected count\n" + declarations + "BEGIN OPEN " + cursor + "; FETCH " + motion + " INTO x; CLOSE " + cursor + "; END"
								for _, prefix := range []string{"CREATE FUNCTION pc_selected_bad() RETURNS void LANGUAGE plpgsql AS $$", "DO $$"} {
									q := prefix + body + "$$"
									want := fixture.ControlsReferenceDiagnostic("0A000", "FETCH statement cannot return multiple rows")
									at := strings.Index(q, "FETCH")
									want.Position = fixture.Position(utf8.RuneCountInString(q[:at]) + 1)
									plCursorError(t, p, b, c, q, want, false, false)
								}
								plCursorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='pc_selected_bad'", "INT8", int64(0))
								plCursorFunction(t, p, b, c, "pc_selected_bad", "", "integer", "BEGIN RETURN 7; END")
								plCursorValue(t, p, b, c, "SELECT pc_selected_bad() AS v", "INT4", int64(7))
								return
							}
							statements := "OPEN " + cursor + "; "
							if direction == "BACKWARD" {
								statements += "FETCH LAST FROM " + cursor + " INTO x; "
							}
							want := int64(10)
							if strings.HasSuffix(kind, "move") {
								statements += "MOVE " + motion + "; FETCH NEXT FROM " + cursor + " INTO x; "
								want = 20
								if direction == "BACKWARD" {
									want = 30
								}
							} else {
								statements += "FETCH " + motion + " INTO x; "
								if direction == "BACKWARD" {
									want = 20
								}
							}
							statements += "CLOSE " + cursor + "; RETURN CASE WHEN FOUND THEN x ELSE -1 END;"
							plCursorFunction(t, p, b, c, "pc_selected", "", "integer", declarations+"BEGIN "+statements+" END")
							plCursorValue(t, p, b, c, "SELECT pc_selected() AS v", "INT4", want)
						})
					}
				})
			}
		}
	}
}
