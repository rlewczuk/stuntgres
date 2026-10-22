package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTablespaceInfoWarnings(t *testing.T) {
	p := fixture.NewScenario(t)
	tablespaceInfoBackends(t, p, func(t *testing.T, b fixture.Backend) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, rec, s) }
		q := "SELECT v FROM pg_tablespace_databases($1::oid) AS t(v)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		tablespaceInfoNotices(t, notices)
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			arg     any
			message string
		}{
			{int64(1664), "global tablespace never has databases"}, {int64(0), "0 is not a tablespace OID"}, {int64(4294967295), "4294967295 is not a tablespace OID"}, {nil, ""},
		} {
			for range 2 {
				notices = nil
				fixture.RunStep(t, p.Ctx, held, rec, tablespaceInfoSelect(q, "OID", nil, tc.arg))
				if tc.message == "" {
					tablespaceInfoNotices(t, notices)
				} else {
					tablespaceInfoNotices(t, notices, tc.message)
				}
			}
			notices = nil
			run(tablespaceInfoSelect("SELECT pg_tablespace_databases($1::oid) AS v", "OID", nil, tc.arg))
			if tc.message == "" {
				tablespaceInfoNotices(t, notices)
			} else {
				tablespaceInfoNotices(t, notices, tc.message)
			}
		}
		for _, q := range []string{
			"SELECT pg_tablespace_databases(1664) AS v LIMIT 0",
			"SELECT v FROM pg_tablespace_databases(1664) AS t(v) LIMIT 0",
			"SELECT pg_tablespace_databases(1664) AS v WHERE false",
			"SELECT v FROM pg_tablespace_databases(1664) AS t(v) WHERE false",
		} {
			t.Run(q, func(t *testing.T) {
				notices = nil
				fixture.RunStep(t, p.Ctx, c, rec, tablespaceInfoSelect(q, "OID", nil))
				tablespaceInfoNotices(t, notices)
			})
		}
		run(fixture.Exec("SET client_min_messages TO error", 0))
		defer c.ExecContext(p.Ctx, "RESET client_min_messages")
		notices = nil
		fixture.RunStep(t, p.Ctx, held, rec, tablespaceInfoSelect(q, "OID", nil, int64(1664)))
		tablespaceInfoNotices(t, notices)
	})
}

func TestReferenceTablespaceInfoPhysicalBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	tablespaceInfoBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_tablespace WHERE oid='4294967295'::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT pg_tablespace_location('4294967295'::oid)", tablespaceInfoLocationError(b.PG))
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		for _, oid := range []int64{0, 1663, 1664} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, tablespaceInfoSelect(fmt.Sprintf("SELECT pg_tablespace_location(%d) AS v", oid), "TEXT", [][]any{{""}}))
		}
	})
}
