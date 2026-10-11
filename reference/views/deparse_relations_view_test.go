package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeparseRelationsViewOverloads(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.DeparseRelationsSeed(t, p, b, c)
		compact := " SELECT id AS k,\n    txt AS label\n   FROM t\n  WHERE visible;"
		for _, call := range []string{"pg_get_viewdef('v'::text)", "pg_get_viewdef('v'::regclass)", "pg_get_viewdef('v'::text,false)", "pg_get_viewdef('v'::regclass,false)", "pg_get_viewdef('v')", "pg_catalog.pg_get_viewdef('v')"} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%v", call, prepared), func(t *testing.T) {
					s := fixture.DeparseRelationsTextStep(t, b, "SELECT "+call+" AS definition", compact)
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				})
			}
		}
		// Actual catalog OID inputs prove each OID signature without requiring
		// an unrelated explicit REGCLASS-to-OID SQL cast.
		for _, call := range []string{"pg_get_viewdef(oid)", "pg_get_viewdef(oid,false)", "pg_get_viewdef(oid,true)", "pg_get_viewdef(oid,20)"} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("catalog/%s/prepared=%v", call, prepared), func(t *testing.T) {
					s := fixture.DeparseRelationsTextStep(t, b, "SELECT "+call+" AS definition FROM pg_class WHERE relname='v'", compact)
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				})
			}
		}
		for _, call := range []string{
			"pg_get_viewdef(0::oid)", "pg_get_viewdef(4294967295::oid)", "pg_get_viewdef('t'::regclass)", "pg_get_viewdef('ix'::regclass)", "pg_get_viewdef('t'::text)",
			"pg_get_viewdef(NULL)", "pg_get_viewdef(NULL::oid)", "pg_get_viewdef(NULL::text)", "pg_get_viewdef(NULL,true)", "pg_get_viewdef(NULL,1)",
			"pg_get_viewdef(NULL::oid,true)", "pg_get_viewdef(NULL::text,true)", "pg_get_viewdef(NULL::oid,20)", "pg_get_viewdef('v'::text,NULL)",
			"pg_get_viewdef('v'::regclass,NULL::boolean)", "pg_get_viewdef('v'::regclass,NULL::integer)",
		} {
			t.Run(call, func(t *testing.T) {
				s := fixture.DeparseRelationsTextStep(t, b, "SELECT "+call+" AS definition", nil)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE VIEW "123" AS SELECT id FROM t`, 0))
		definition := fixture.DeparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef('123'::text) AS definition")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW numeric_copy AS "+definition, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM numeric_copy ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(3)}}))
		empty := fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_viewdef('v') AS definition FROM t WHERE false", nil)
		empty.Rows = nil
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(empty))
	})
}
