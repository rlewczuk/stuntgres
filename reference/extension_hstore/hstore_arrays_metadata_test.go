package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHstoreArraysMetadata(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		for _, tc := range []struct {
			input string
			rows  [][]any
		}{
			{"'a=>1'::hstore", [][]any{{"{a,1}", "{{a,1}}", "{a,1}", "{{a,1}}"}}},
			{"''::hstore", [][]any{{"{}", "{}", "{}", "{}"}}}, {"NULL::hstore", [][]any{{nil, nil, nil, nil}}},
		} {
			q := hstoreArraysQuery(tc.input)
			for _, prepared := range []bool{false, true} {
				s := hstoreArraysSelect(b, q, tc.rows)
				s.Prepared = prepared
				check := s.CheckMetadata
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					check(t, rows)
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for _, column := range columns {
						if p, s, known := column.DecimalSize(); p != 0 || s != 0 || known {
							t.Fatalf("decimal metadata %d/%d/%v", p, s, known)
						}
						if nullable, known := column.Nullable(); nullable || known {
							t.Fatalf("computed nullable metadata %v/%v", nullable, known)
						}
					}
				}
				hstoreRun(t, p, b, c, s)
			}
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			hstoreRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, hstoreArraysSelect(b, q, tc.rows))
			stmt.Close()
			hstoreRun(t, p, b, c, hstoreArraysSelect(b, q+" WHERE false", [][]any{}))
		}
		for _, name := range []string{"hstore_to_array", "hstore_to_matrix"} {
			identity := hstoreLiteral(name+"(hstore)") + "::regprocedure::oid"
			hstoreRun(t, p, b, c, fixture.Select("SELECT proargtypes[0]='hstore'::regtype AS formal,prorettype=1009 AS result FROM pg_proc WHERE oid="+identity, []string{"formal", "result"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
			hstoreRun(t, p, b, c, fixture.Select("SELECT deptype FROM pg_depend WHERE classid=1255 AND objid="+identity+" AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore')", []string{"deptype"}, []string{"CHAR"}, [][]any{{"e"}}))
			hstoreRun(t, p, b, c, fixture.Select("SELECT refobjid='hstore'::regtype AS formal,deptype FROM pg_depend WHERE classid=1255 AND objid="+identity+" AND refclassid=1247", []string{"formal", "deptype"}, []string{"BOOL", "CHAR"}, [][]any{{true, "n"}}))
		}
		for _, name := range []string{"%%", "%#"} {
			identity := hstoreLiteral(name+"(NONE,hstore)") + "::regoperator::oid"
			hstoreRun(t, p, b, c, fixture.Select("SELECT deptype FROM pg_depend WHERE classid=2617 AND objid="+identity+" AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore')", []string{"deptype"}, []string{"CHAR"}, [][]any{{"e"}}))
		}
		hstoreRun(t, p, b, c, fixture.Select("SELECT p.proname,p.pronargs,p.prorettype::integer AS result,p.proisstrict,p.provolatile,p.proparallel,p.proretset,l.lanname,p.prosrc FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.pronamespace='public'::regnamespace AND p.proname IN ('hstore_to_array','hstore_to_matrix') ORDER BY p.proname", []string{"proname", "pronargs", "result", "proisstrict", "provolatile", "proparallel", "proretset", "lanname", "prosrc"}, []string{"NAME", "INT2", "INT4", "BOOL", "CHAR", "CHAR", "BOOL", "NAME", "TEXT"}, [][]any{{"hstore_to_array", int64(1), int64(1009), true, "i", "s", false, "c", "hstore_to_array"}, {"hstore_to_matrix", int64(1), int64(1009), true, "i", "s", false, "c", "hstore_to_matrix"}}))
		hstoreRun(t, p, b, c, fixture.Select("SELECT o.oprname,o.oprkind,o.oprleft::integer AS left_type,o.oprright='hstore'::regtype AS right_type,o.oprresult::integer AS result,o.oprcode=p.oid AS backing,o.oprcom::integer AS commutator,o.oprnegate::integer AS negator,o.oprcanhash,o.oprcanmerge FROM pg_operator o JOIN pg_proc p ON p.oid=o.oprcode WHERE o.oprnamespace='public'::regnamespace AND o.oprname IN ('%%','%#') ORDER BY o.oprname", []string{"oprname", "oprkind", "left_type", "right_type", "result", "backing", "commutator", "negator", "oprcanhash", "oprcanmerge"}, []string{"NAME", "CHAR", "INT4", "BOOL", "INT4", "BOOL", "INT4", "INT4", "BOOL", "BOOL"}, [][]any{{"%#", "l", int64(0), true, int64(1009), true, int64(0), int64(0), false, false}, {"%%", "l", int64(0), true, int64(1009), true, int64(0), int64(0), false, false}}))
	})
}
