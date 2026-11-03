package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityCheckDiagnostics(t *testing.T) {
	pvcCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS pvc_fdw CASCADE")
		for _, q := range []string{"CREATE TABLE pv_part.parent(k integer) PARTITION BY RANGE(k)", "CREATE INDEX heap_index ON pv_part.heap(k)", "CREATE SEQUENCE pv_part.seq", "CREATE VIEW pv_part.v AS SELECT k FROM pv_part.heap", "CREATE TYPE pv_part.composite AS(k integer)", "CREATE FOREIGN DATA WRAPPER pvc_fdw", "CREATE SERVER pvc_server FOREIGN DATA WRAPPER pvc_fdw", "CREATE FOREIGN TABLE pv_part.foreign_heap(k integer) SERVER pvc_server"} {
			pvRun(t, p, b, c, fixture.Exec(q, 0))
		}
		wrong := func(name, plural string) {
			for _, fn := range pvcFunctions {
				t.Run(name+"/"+fn, func(t *testing.T) {
					s := pvError("SELECT * FROM pv_part."+fn+"($1::regclass)", "42809", "relation \""+name+"\" is of wrong relation kind", "pv_part."+name)
					s.Diagnostic.Detail = "This operation is not supported for " + plural + "."
					pvBoth(t, p, b, c, s)
				})
			}
		}
		for _, tc := range []struct{ name, plural string }{{"parent", "partitioned tables"}, {"heap_index", "indexes"}, {"seq", "sequences"}, {"v", "views"}, {"foreign_heap", "foreign tables"}, {"composite", "composite types"}} {
			wrong(tc.name, tc.plural)
		}
		if b.PG {
			pvRun(t, p, b, c, fixture.Exec("CREATE INDEX parent_index ON pv_part.parent(k)", 0))
			wrong("parent_index", "partitioned indexes")
		}
		pvRun(t, p, b, c, fixture.Exec("CREATE TABLE pv_part.doomed(k integer)", 0))
		dropped := pvOID(t, p, c, "pv_part.doomed")
		pvRun(t, p, b, c, fixture.Exec("DROP TABLE pv_part.doomed", 0))
		for _, fn := range pvcFunctions {
			for _, oid := range []int64{0, 4294967295, dropped} {
				pvBoth(t, p, b, c, pvError("SELECT * FROM pv_part."+fn+"($1::regclass)", "XX000", fmt.Sprintf("could not open relation with OID %d", oid), oid))
			}
			pvBoth(t, p, b, c, pvError("SELECT * FROM pv_part."+fn+"($1::text::regclass)", "42P01", "relation \"pv_part.absent\" does not exist", "pv_part.absent"))
			q := "SELECT * FROM pv_part." + fn + "('pv_part.absent'::regclass)"
			s := pvError(q, "42P01", "relation \"pv_part.absent\" does not exist")
			s.Diagnostic.Position = fixture.Position(strings.Index(q, "'pv_part.absent'") + 1)
			pvRun(t, p, b, c, s)
			st, err := c.PrepareContext(p.Ctx, q)
			if st != nil {
				st.Close()
			}
			fixture.AssertDiagnostic(t, err, s.Diagnostic)
			s = pvError("SELECT * FROM pv_part."+fn+"($1::regclass)", "42602", "invalid name syntax", "pv_part..heap")
			s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
			pvBoth(t, p, b, c, s)
		}
		for _, phase := range []string{"empty", "inserted", "frozen", "truncated"} {
			for _, fn := range pvcFunctions {
				t.Run("foreign_temp/"+phase+"/"+fn, func(t *testing.T) {
					owner := pvConn(t, p, b)
					defer owner.Close()
					pvRun(t, p, b, owner, fixture.Exec("CREATE TEMP TABLE pvc_foreign(k integer)", 0))
					defer owner.ExecContext(p.Ctx, "DROP TABLE pvc_foreign")
					if phase != "empty" {
						pvRun(t, p, b, owner, fixture.Exec("INSERT INTO pvc_foreign VALUES(1)", 1))
					}
					if phase == "frozen" || phase == "truncated" {
						pvRun(t, p, b, owner, fixture.Exec("VACUUM (FREEZE) pvc_foreign", 0))
					}
					if phase == "truncated" {
						pvcTruncate(t, p, b, owner, "pvc_foreign")
					}
					oid := pvOID(t, p, owner, "pvc_foreign")
					q := "SELECT * FROM pv_part." + fn + "($1::regclass)"
					// Independently reasoned from actual buffer access, observed by root.
					s := pvError(q, "0A000", "cannot access temporary tables of other sessions", oid)
					if fn == pvcFunctions[2] {
						s = fixture.Select(q, []string{fn}, []string{"VOID"}, [][]any{{""}}, oid)
					}
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprint(prepared), func(t *testing.T) {
							cell := s
							if prepared {
								cell = fixture.Prepared(cell)
							}
							pvRun(t, p, b, c, cell)
						})
					}
					var data [][]any
					if phase != "empty" {
						data = [][]any{{int64(1)}}
					}
					pvBoth(t, p, b, owner, fixture.Select("SELECT k FROM pvc_foreign", []string{"k"}, []string{"INT4"}, data))
				})
			}
		}
	})
}
