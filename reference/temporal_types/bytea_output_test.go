package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceByteaOutputCasts(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("SET standard_conforming_strings=on", 0))
			run(fixture.Exec("CREATE TABLE bytea_output_values(id INTEGER,v BYTEA)", 0))
			for _, mode := range []struct {
				setting, canonical string
				values             []any
			}{
				{"HeX", "hex", []any{nil, `\x`, `\x0020275c7e7f80ff`, `\x0020275c7e7f80ff`, `\x1f207e7f`, `\xdeadbeef`}},
				{"ESCAPE", "escape", []any{nil, "", `\000 '\\~\177\200\377`, `\000 '\\~\177\200\377`, `\037 ~\177`, `\336\255\276\357`}},
			} {
				run(fixture.Exec("SET bytea_output="+mode.setting, 0))
				run(fixture.Select("SHOW bytea_output", []string{"bytea_output"}, []string{"TEXT"}, [][]any{{mode.canonical}}))
				run(fixture.Exec("DELETE FROM bytea_output_values", 0))
				run(fixture.Exec(`INSERT INTO bytea_output_values VALUES(0,NULL),(1,''),(2,'\x0020275c7e7f80ff'),(3,'\000 ''\\~\177\200\377'),(4,'\x1f207e7f'),(5,'\xDEADBEEF')`, 6))
				for _, expression := range []string{"v::text", "CAST(v AS text)", "text(v)"} {
					want := make([][]any, len(mode.values))
					for i, value := range mode.values {
						want[i] = []any{value}
					}
					s := fixture.Select("SELECT "+expression+" AS rendered FROM bytea_output_values ORDER BY id", []string{"rendered"}, []string{"TEXT"}, want)
					run(s)
					run(fixture.Prepared(s))
				}
				empty := []byte{}
				if b.PG && mode.canonical == "escape" {
					empty = nil // lib/pq's empty escape BYTEA is typed nil, not SQL NULL.
				}
				referenceByteaRaw(t, p.Ctx, c, b, "SELECT v FROM bytea_output_values ORDER BY id", false, []string{"v"}, []int{1}, [][]any{{nil}, {empty}, {[]byte{0, 32, 39, 92, 126, 127, 128, 255}}, {[]byte{0, 32, 39, 92, 126, 127, 128, 255}}, {[]byte{31, 32, 126, 127}}, {[]byte{222, 173, 190, 239}}}, false)
				run(fixture.Exec("DELETE FROM bytea_output_values", 6))
			}
		})
	}
}

func TestReferenceByteaOutputHeldStatements(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE bytea_output_values(id INTEGER,v BYTEA)", 0))
			owned := []byte{0, 32, 39, 92, 126, 127, 128, 255}
			run(fixture.Exec("INSERT INTO bytea_output_values VALUES(1,$1)", 1, owned))
			owned[0] = 255
			columnQuery := "SELECT v::text AS rendered FROM bytea_output_values WHERE id=$1"
			column, err := c.PrepareContext(p.Ctx, columnQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer column.Close()
			boundQuery := "SELECT $1::bytea::text AS rendered"
			bound, err := c.PrepareContext(p.Ctx, boundQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer bound.Close()
			for _, mode := range []struct{ name, vector, empty, slash, binary string }{
				{"hex", `\x0020275c7e7f80ff`, `\x`, `\x5c783431`, `\x0080ff`},
				{"escape", `\000 '\\~\177\200\377`, "", `\\x41`, `\000\200\377`},
				{"hex", `\x0020275c7e7f80ff`, `\x`, `\x5c783431`, `\x0080ff`},
			} {
				run(fixture.Exec("SET bytea_output="+mode.name, 0))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, column, columnQuery}, b.Recorder, fixture.Select(columnQuery, []string{"rendered"}, []string{"TEXT"}, [][]any{{mode.vector}}, 1))
				run(fixture.Select("SELECT v::text AS rendered FROM bytea_output_values WHERE id=1", []string{"rendered"}, []string{"TEXT"}, [][]any{{mode.vector}}))
				for _, frame := range []struct{ arg, want any }{
					{[]byte{0, 32, 39, 92, 126, 127, 128, 255}, mode.vector},
					{[]byte{}, mode.empty}, {"", mode.empty}, {nil, nil}, {[]byte(nil), nil},
					{`\x41`, mode.slash}, {[]byte{0, 128, 255}, mode.binary},
				} {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, bound, boundQuery}, b.Recorder, fixture.Select(boundQuery, []string{"rendered"}, []string{"TEXT"}, [][]any{{frame.want}}, frame.arg))
				}
				referenceByteaRaw(t, p.Ctx, c, b, "SELECT v FROM bytea_output_values", false, []string{"v"}, []int{1}, [][]any{{[]byte{0, 32, 39, 92, 126, 127, 128, 255}}}, true)
			}
		})
	}
}
