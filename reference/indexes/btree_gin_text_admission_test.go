package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeGINTextAdmission(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		compoundCleanup(t, p, b, conn)
		run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
		for _, actual := range []string{"text", "varchar"} {
			for _, selected := range []string{"text", "varchar", "bpchar"} {
				table := "text_admit_" + actual + "_" + selected
				run(fixture.Exec("CREATE TABLE "+table+"(id integer NOT NULL,v "+actual+")", 0))
				run(fixture.Exec("INSERT INTO "+table+" VALUES(1,'ab'),(2,'ab '),(3,NULL)", 3))
				run(fixture.Exec("CREATE INDEX "+table+"_i ON "+table+" USING gin(v public."+selected+"_ops)", 0))
				classRoutes(t, p, b, conn, ginIDs("SELECT id FROM "+table+" WHERE v='ab'::"+actual+" ORDER BY id", []int64{1}))
				classRoutes(t, p, b, conn, ginIDs("SELECT id FROM "+table+" WHERE v::bpchar='ab'::bpchar ORDER BY id", []int64{1, 2}))
				classRoutes(t, p, b, conn, fixture.Select("SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", map[string]string{"text": "TEXT", "varchar": "VARCHAR"}[actual]}, [][]any{{int64(1), "ab"}, {int64(2), "ab "}, {int64(3), nil}}))
			}
		}
		for i, tc := range textCases {
			domain := fmt.Sprintf("text_domain_%d", i)
			table := domain + "_rows"
			run(fixture.Exec("CREATE DOMAIN "+domain+" AS "+textSQLType(tc), 0))
			run(fixture.Exec("CREATE TABLE "+table+"(id integer,v "+domain+")", 0))
			run(fixture.Exec("INSERT INTO "+table+" VALUES(1,"+networkLiteral(tc.boundary, textSQLType(tc))+"),(2,NULL)", 2))
			run(fixture.Exec("CREATE INDEX "+table+"_i ON "+table+" USING gin(v public."+tc.typ+"_ops)", 0))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM "+table+" WHERE v=$1::"+textSQLType(tc)+" ORDER BY id", []int64{1}, textArgument(tc, tc.boundary)))
		}
		// Diagnostics owns every negative, late-key failure and valid-name retry;
		// the positive matrix above never treats functional casts as admission.
	})
}
