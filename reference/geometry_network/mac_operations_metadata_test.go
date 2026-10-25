package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMACOperationsMetadataAndBinds(t *testing.T) {
	macOperationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mac_ops_metadata(id integer,m6 macaddr,m8 macaddr8)", 0))
		run(fixture.Exec("INSERT INTO mac_ops_metadata VALUES(1,NULL,NULL),(2,'12:34:56:78:90:ab','12:34:56:78:90:ab:cd:ef')", 2))
		for _, tc := range []struct{ expression, typ string }{
			{"~m6", "MACADDR"}, {"m6 & m6", "MACADDR"}, {"m6 | m6", "MACADDR"}, {"trunc(m6)", "MACADDR"},
			{"~m8", "MACADDR8"}, {"m8 & m8", "MACADDR8"}, {"m8 | m8", "MACADDR8"}, {"trunc(m8)", "MACADDR8"},
			{"macaddr8_set7bit(m6)", "MACADDR8"}, {"macaddr8_set7bit(m8)", "MACADDR8"},
		} {
			t.Run(tc.expression, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				for _, id := range []string{"0", "1"} {
					var want [][]any
					if id == "1" {
						want = [][]any{{nil}}
					}
					s := macaddr8Select(b.PG, "SELECT "+tc.expression+" AS v FROM mac_ops_metadata WHERE id="+id, []string{"v"}, []string{tc.typ}, want)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						if tc.typ == "MACADDR" {
							macaddrMetadata(t, rows, b.PG, nil)
						} else {
							macaddr8Metadata(t, rows, b.PG, nil)
						}
					}
					run(s)
					run(fixture.Prepared(s))
				}
			})
		}
		for _, tc := range []struct{ expression, input, typ, first, second string }{
			{"$1::macaddr & macaddr 'ff:ff:ff:ff:ff:ff'", "macaddr", "MACADDR", "12:34:56:78:90:ab", "00:34:56:ab:cd:ef"},
			{"$1::macaddr8 | macaddr8 '00:00:00:00:00:00:00:00'", "macaddr8", "MACADDR8", "12:34:56:78:90:ab:cd:ef", "00:34:56:ff:fe:ab:cd:ef"},
			{"trunc($1::macaddr)", "macaddr", "MACADDR", "12:34:56:00:00:00", "00:34:56:00:00:00"},
			{"trunc($1::macaddr8)", "macaddr8", "MACADDR8", "12:34:56:00:00:00:00:00", "00:34:56:00:00:00:00:00"},
			{"macaddr8_set7bit($1::macaddr)", "macaddr", "MACADDR8", "12:34:56:ff:fe:78:90:ab", "02:34:56:ff:fe:ab:cd:ef"},
			{"macaddr8_set7bit($1::macaddr8)", "macaddr8", "MACADDR8", "12:34:56:78:90:ab:cd:ef", "02:34:56:ff:fe:ab:cd:ef"},
		} {
			t.Run(tc.expression, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				q := "SELECT " + tc.expression + " AS v"
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
				input := "12:34:56:78:90:ab"
				if tc.input == "macaddr8" {
					input += ":cd:ef"
				}
				for _, frame := range []struct{ arg, want any }{{input, tc.first}, {[]byte("00:34:56:ab:cd:ef"), tc.second}, {nil, nil}, {input, tc.first}} {
					s := macaddr8Select(b.PG, q, []string{"v"}, []string{tc.typ}, [][]any{{frame.want}}, frame.arg)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						if tc.typ == "MACADDR" {
							macaddrMetadata(t, rows, b.PG, nil)
						} else {
							macaddr8Metadata(t, rows, b.PG, nil)
						}
					}
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				d := macaddr8ParameterDiagnostic("bad", "1")
				if tc.input == "macaddr" {
					d = macaddrDiagnostic("bad", "22P02")
					d.Context = "unnamed portal parameter $1 = '...'"
				}
				s := fixture.ErrorDiagnostic(q, d, "bad")
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, macaddr8Select(b.PG, q, []string{"v"}, []string{tc.typ}, [][]any{{nil}}, nil))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, macaddr8Select(b.PG, q, []string{"v"}, []string{tc.typ}, [][]any{{tc.first}}, input))
			})
		}
	})
}
