package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceUUIDOsspNamespaceValues(t *testing.T) {
	uuidOsspNamespaceCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		uuidOsspNamespaceInstall(t, p, c, r)
		for _, tc := range uuidOsspNamespaces {
			t.Run(tc.name, func(t *testing.T) {
				q := "SELECT uuid_ossp_part." + tc.name + "() AS u"
				s := uuidOsspNamespaceSelect(q, tc.value)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { uuidOsspColumns(t, rows, pg, 1, false) }
				s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
					var raw any
					if err := rows.Scan(&raw); err != nil {
						t.Fatal(err)
					}
					if pg {
						b, ok := raw.([]byte)
						if !ok || string(b) != tc.value {
							t.Fatalf("raw UUID=%T %v", raw, raw)
						}
					} else if v, ok := raw.(string); !ok || v != tc.value {
						t.Fatalf("raw UUID=%T %v", raw, raw)
					}
				}
				fixture.RunStep(t, p.Ctx, c, r, s)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
				held, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				for range 2 {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, r, s)
				}
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT uuid_ossp_part."+tc.name+"()", []string{tc.name}, []string{"UUID"}, [][]any{{tc.value}}))
				empty := fixture.Select(q+" WHERE FALSE", []string{"u"}, []string{"UUID"}, nil)
				empty.CheckMetadata = s.CheckMetadata
				fixture.RunStep(t, p.Ctx, c, r, empty)
			})
		}
	})
}

func TestReferenceUUIDOsspNamespaceBindings(t *testing.T) {
	uuidOsspNamespaceCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		uuidOsspNamespaceInstall(t, p, c, r)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE TABLE namespace_values(id integer,u uuid)", 0))
		for i, tc := range uuidOsspNamespaces {
			call := "uuid_ossp_part." + tc.name + "()"
			run(fixture.Exec("INSERT INTO namespace_values VALUES($1,"+call+")", 1, i))
			q := "SELECT $1::uuid=" + call + " AS equal"
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, arg := range []any{tc.value, strings.ToUpper(tc.value), "{" + tc.value + "}", strings.ReplaceAll(tc.value, "-", ""), []byte(tc.value), nil, tc.value} {
				var want any = true
				if arg == nil {
					want = nil
				}
				step := fixture.Select(q, []string{"equal"}, []string{"BOOL"}, [][]any{{want}}, arg)
				run(step)
				run(fixture.Prepared(step))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, r, step)
			}
			s.Close()
			run(fixture.Select("SELECT "+call+" IS NULL AS missing,"+call+"=NULL::uuid AS equal", []string{"missing", "equal"}, []string{"BOOL", "BOOL"}, [][]any{{false, nil}}))
		}
		run(fixture.Exec("INSERT INTO namespace_values VALUES(5,NULL)", 1))
		want := make([][]any, 0, 6)
		for _, tc := range uuidOsspNamespaces {
			want = append(want, []any{tc.value})
		}
		want = append(want, []any{nil})
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT u FROM namespace_values ORDER BY id", []string{"u"}, []string{"UUID"}, want)
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { uuidOsspColumns(t, rows, pg, 1, true) }
			run(s)
			s = fixture.Select("SELECT u FROM namespace_values WHERE u=$1::uuid", []string{"u"}, []string{"UUID"}, nil, nil)
			s.Prepared = prepared
			run(s)
		}
	})
}

func TestReferenceUUIDOsspNamespaceDiagnostics(t *testing.T) {
	uuidOsspNamespaceCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		uuidOsspNamespaceInstall(t, p, c, r)
		for _, tc := range uuidOsspNamespaces {
			name := "uuid_ossp_part." + tc.name
			for _, arg := range []struct{ sql, typ string }{{"NULL", "unknown"}, {"1", "integer"}, {"NULL::uuid", "uuid"}, {"$1", "unknown"}} {
				var args []any
				if arg.sql == "$1" {
					args = []any{nil}
				}
				uuidOsspFailure(t, p, c, "SELECT "+name+"("+arg.sql+")", uuidOsspMissing(name+"("+arg.typ+")", 8), args...)
			}
			uuidOsspFailure(t, p, c, "/*é*/ SELECT "+name+"(NULL)", uuidOsspMissing(name+"(unknown)", 14))
			for _, decoration := range []struct{ sql, message string }{
				{" FILTER (WHERE TRUE)", "FILTER specified, but " + name + " is not an aggregate function"},
				{" OVER ()", "OVER specified, but " + name + " is not a window function nor an aggregate function"},
			} {
				d := fixture.ControlsReferenceDiagnostic("42809", decoration.message)
				d.Position = "8"
				uuidOsspFailure(t, p, c, "SELECT "+name+"()"+decoration.sql, d)
			}
			d := fixture.ControlsReferenceDiagnostic("54023", "cannot pass more than 100 arguments to a function")
			d.Position = "8"
			uuidOsspFailure(t, p, c, "SELECT "+name+"("+strings.Repeat("NULL,", 100)+"NULL)", d)
			uuidOsspFailure(t, p, c, `SELECT uuid_ossp_part."`+strings.ToUpper(tc.name)+`"()`, uuidOsspMissing("uuid_ossp_part."+strings.ToUpper(tc.name)+"()", 8))
		}
	})
}
