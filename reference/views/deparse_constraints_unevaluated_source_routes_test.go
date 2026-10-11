package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsNeverEvaluatesStoredTreeSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) {
				s.NonContext = true
				if s.SelectRows && s.Diagnostic == nil {
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { fixture.DeparseReviewMetadata(t, b.PG, rows) }
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			run(fixture.Exec("CREATE SEQUENCE s START 1", 0))
			run(fixture.Exec("CREATE TABLE t(a INT,CONSTRAINT c CHECK(nextval('s')>0))", 0))
			// These regclass presentation goldens are source-derived until the
			// root exercises this independent PostgreSQL stream.
			for _, prepared := range []bool{false, true} {
				for _, mode := range []struct {
					pretty bool
					want   string
				}{{false, "(nextval('s'::regclass) > 0)"}, {true, "nextval('s'::regclass) > 0"}} {
					s := fixture.Select("SELECT pg_get_expr(conbin,conrelid,$1) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", []string{"d"}, []string{"TEXT"}, [][]any{{mode.want}}, mode.pretty)
					s.Prepared = prepared
					run(s)
				}
			}
			run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(fixture.Exec("ALTER SEQUENCE s RENAME TO renamed_sequence", 0))
			run(fixture.Prepared(fixture.Select("SELECT pg_get_expr(conbin,conrelid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", []string{"d"}, []string{"TEXT"}, [][]any{{"nextval('renamed_sequence'::regclass) > 0"}})))
			run(fixture.Select("SELECT nextval('renamed_sequence')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
		})
	}
}
