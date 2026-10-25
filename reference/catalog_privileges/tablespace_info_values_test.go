package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTablespaceInfoValues(t *testing.T) {
	p := fixture.NewScenario(t)
	tablespaceInfoBackends(t, p, func(t *testing.T, b fixture.Backend) {
		for _, prepared := range []bool{false, true} {
			for _, prefix := range []string{"", "pg_catalog."} {
				for _, arg := range []string{"0", "1663", "1664", "(SELECT oid FROM pg_tablespace WHERE spcname='pg_default')"} {
					s := tablespaceInfoSelect("SELECT "+prefix+"pg_tablespace_location("+arg+") AS v", "TEXT", [][]any{{""}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
				for _, arg := range []string{"NULL", "NULL::oid"} {
					for _, tc := range []struct {
						q, typ string
						rows   [][]any
					}{
						{"SELECT " + prefix + "pg_tablespace_location(" + arg + ") AS v", "TEXT", [][]any{{nil}}},
						{"SELECT v FROM " + prefix + "pg_tablespace_location(" + arg + ") AS s(v)", "TEXT", [][]any{{nil}}},
						{"SELECT " + prefix + "pg_tablespace_databases(" + arg + ") AS v", "OID", nil},
						{"SELECT v FROM " + prefix + "pg_tablespace_databases(" + arg + ") AS s(v)", "OID", nil},
					} {
						s := tablespaceInfoSelect(tc.q, tc.typ, tc.rows)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					}
				}
			}
		}
		q := "SELECT pg_tablespace_location($1::oid) AS v"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, arg := range []any{int64(0), nil, int64(1664), int64(1663)} {
			var value any = ""
			if arg == nil {
				value = nil
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, tablespaceInfoSelect(q, "TEXT", [][]any{{value}}, arg))
		}
		rows := [][]any{
			{tablespaceInfoOID(b, 2556), "pg_tablespace_databases", int64(1), tablespaceInfoOID(b, 26), true, "s", "s"},
			{tablespaceInfoOID(b, 3778), "pg_tablespace_location", int64(1), tablespaceInfoOID(b, 25), true, "s", "s"},
		}
		s := fixture.Select("SELECT oid,proname,pronargs,prorettype,proisstrict,provolatile,proparallel FROM pg_proc WHERE oid IN (2556,3778) ORDER BY oid",
			[]string{"oid", "proname", "pronargs", "prorettype", "proisstrict", "provolatile", "proparallel"},
			[]string{"OID", "NAME", "INT2", "OID", "BOOL", "CHAR", "CHAR"}, rows)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		// Released ABI fields beyond the existing native nine-column projection.
		// Native formal/set behavior is independently exercised above and by R4/R7.
		if b.PG {
			// Raw routine ACL representation may retain explicit default-equivalent
			// grants from another scenario; effective EXECUTE is covered by Privileges.
			s = fixture.Select("SELECT oid,proargtypes::text AS args,proretset,prosecdef,proargnames,pronargdefaults,provariadic FROM pg_proc WHERE oid IN (2556,3778) ORDER BY oid",
				[]string{"oid", "args", "proretset", "prosecdef", "proargnames", "pronargdefaults", "provariadic"},
				[]string{"OID", "TEXT", "BOOL", "BOOL", "_TEXT", "INT2", "OID"},
				[][]any{{"2556", "26", true, false, nil, int64(0), "0"}, {"3778", "26", false, false, nil, int64(0), "0"}})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	})
}

func TestReferenceTablespaceInfoMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	tablespaceInfoBackends(t, p, func(t *testing.T, b fixture.Backend) {
		oid := tablespaceInfoCurrent(t, p, b.DB)
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q, typ string
				rows   [][]any
			}{
				{"SELECT pg_tablespace_location(0) AS v", "TEXT", [][]any{{""}}},
				{"SELECT pg_tablespace_location(NULL::oid) AS v", "TEXT", [][]any{{nil}}},
				{"SELECT v FROM pg_tablespace_location(NULL::oid) AS t(v)", "TEXT", [][]any{{nil}}},
				{"SELECT v FROM pg_tablespace_databases(NULL::oid) AS t(v)", "OID", nil},
				{"SELECT v FROM pg_tablespace_databases(1663) AS t(v) WHERE v=(SELECT oid FROM pg_database WHERE datname=current_database())", "OID", [][]any{{tablespaceInfoOID(b, oid)}}},
				{"SELECT ordinality AS v FROM pg_tablespace_location(0) WITH ORDINALITY", "INT8", [][]any{{int64(1)}}},
			} {
				s := tablespaceInfoSelect(tc.q, tc.typ, tc.rows)
				s.Prepared = prepared
				s.CheckMetadata = tablespaceInfoMetadata(b.PG)
				s.CheckScan = func(t *testing.T, r *sql.Rows, _ int) {
					var raw any
					if err := r.Scan(&raw); err != nil {
						t.Fatal(err)
					}
					fixture.AssertRawValue(t, b.PG, tc.typ, raw)
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		s := tablespaceInfoSelect("SELECT pg_tablespace_location(0) AS v", "TEXT", [][]any{{""}})
		s.NonContext = true
		s.CheckMetadata = tablespaceInfoMetadata(b.PG)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	})
}
