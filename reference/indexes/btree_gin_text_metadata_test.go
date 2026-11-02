package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeGINTextMetadata(t *testing.T) {
	for _, tc := range textCases {
		t.Run(tc.typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(fixture.Exec("CREATE TABLE text_metadata(id integer NOT NULL,v "+textSQLType(tc)+")", 0))
				textSeed(run, "text_metadata", tc)
				q := "SELECT id,v FROM text_metadata WHERE id >= $1 ORDER BY id"
				held := classHeld(t, p, conn, q)
				check := func() {
					for _, probe := range []struct {
						arg  any
						rows [][]any
					}{{1, textRows(tc)}, {len(textTexts(tc)) + 1, textRows(tc)[len(textTexts(tc)):]}, {100, nil}, {nil, nil}} {
						s := textValueStep(b, q, tc, probe.rows, probe.arg)
						classRoutes(t, p, b, conn, s)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
				}
				check()
				run(fixture.Exec("CREATE EXTENSION btree_gin", 0))
				run(fixture.Exec("CREATE INDEX text_metadata_i ON text_metadata USING gin(v public."+tc.typ+"_ops)", 0))
				check()
			})
		})
	}
	t.Run("widths", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			compoundCleanup(t, p, b, conn)
			run(fixture.Exec("CREATE TABLE text_widths(t text,v varchar(4),b bpchar(4))", 0))
			run(fixture.Exec("INSERT INTO text_widths VALUES('ab','ab','ab'),(NULL,NULL,NULL)", 2))
			run(fixture.Exec("CREATE EXTENSION btree_gin", 0))
			for phase := range 2 {
				for _, prepared := range []bool{false, true} {
					for _, suffix := range []string{"", " WHERE false", " WHERE t IS NULL"} {
						fixture.CharacterMetadata(t, p, b.DB, "SELECT t,v,b FROM text_widths"+suffix, prepared, !b.PG, []string{"t", "v", "b"}, []string{"TEXT", "VARCHAR", "BPCHAR"}, []int64{-1, 4, 4}, []fixture.CharacterNullability{{true, true}, {true, true}, {true, true}})
					}
				}
				if phase == 0 {
					run(fixture.Exec("CREATE INDEX text_widths_i ON text_widths USING gin(t text_ops,v varchar_ops,b bpchar_ops)", 0))
				}
			}
			classRoutes(t, p, b, conn, fixture.Select("SELECT t,v,b FROM text_widths WHERE t IS NOT NULL", []string{"t", "v", "b"}, []string{"TEXT", "VARCHAR", "BPCHAR"}, [][]any{{"ab", "ab", "ab  "}}))
		})
	})
	t.Run("bytea_bound_octets", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			compoundCleanup(t, p, b, conn)
			run(fixture.Exec("CREATE EXTENSION btree_gin", 0))
			run(fixture.Exec("CREATE TABLE text_bytes(id integer NOT NULL,v bytea)", 0))
			run(fixture.Exec("CREATE INDEX text_bytes_i ON text_bytes USING gin(v bytea_ops)", 0))
			q := "INSERT INTO text_bytes VALUES($1,$2::bytea)"
			held := classHeld(t, p, conn, q)
			for i, value := range []any{[]byte{}, []byte{0, 128, 255}, nil, []byte(`\x80`)} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, i+1, value))
			}
			values := [][]any{{int64(1), []byte{}}, {int64(2), []byte{0, 128, 255}}, {int64(3), nil}, {int64(4), []byte(`\x80`)}}
			selectSQL := "SELECT id,v FROM text_bytes ORDER BY id"
			selectHeld := classHeld(t, p, conn, selectSQL)
			for _, mode := range []string{"hex", "escape"} {
				run(fixture.Exec("SET bytea_output='"+mode+"'", 0))
				s := textValueStep(b, selectSQL, textCases[5], values)
				classRoutes(t, p, b, conn, s)
				fixture.RunStep(t, p.Ctx, selectHeld, b.Recorder, s)
				classRoutes(t, p, b, conn, ginIDs("SELECT id FROM text_bytes WHERE v=$1::bytea ORDER BY id", []int64{2}, []byte{0, 128, 255}))
			}
		})
	})
}
