package reference_test

import (
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMACOperationsCatalog(t *testing.T) {
	macOperationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
		// Native exports OID as int64; lib/pq exports its decimal text as bytes.
		// The fixture checks those raw representations before normalization.
		oid := func(n int64) any {
			if b.PG {
				return strconv.FormatInt(n, 10)
			}
			return n
		}
		run(fixture.Select("SELECT oid AS id,proname AS name,pronargs AS nargs,prorettype AS result,proisstrict AS strict,provolatile AS volatility,proparallel AS parallel FROM pg_proc WHERE oid IN (753,3144,3145,3146,4112,4120,4121,4122,4125) ORDER BY oid", []string{"id", "name", "nargs", "result", "strict", "volatility", "parallel"}, []string{"OID", "NAME", "INT2", "OID", "BOOL", "CHAR", "CHAR"}, [][]any{
			{oid(753), "trunc", int64(1), oid(829), true, "i", "s"},
			{oid(3144), "macaddr_not", int64(1), oid(829), true, "i", "s"},
			{oid(3145), "macaddr_and", int64(2), oid(829), true, "i", "s"},
			{oid(3146), "macaddr_or", int64(2), oid(829), true, "i", "s"},
			{oid(4112), "trunc", int64(1), oid(774), true, "i", "s"},
			{oid(4120), "macaddr8_not", int64(1), oid(774), true, "i", "s"},
			{oid(4121), "macaddr8_and", int64(2), oid(774), true, "i", "s"},
			{oid(4122), "macaddr8_or", int64(2), oid(774), true, "i", "s"},
			{oid(4125), "macaddr8_set7bit", int64(1), oid(774), true, "i", "s"},
		}))
		// Native pg_proc has no proargtypes projection. Its exact formal tuples
		// are checked against genuine declarations by TestMACOperationsRoutineMetadata;
		// this oracle-only metadata phase independently asserts the released tuples.
		if b.PG {
			run(fixture.Select("SELECT oid AS id,proargtypes::text AS args FROM pg_proc WHERE oid IN (753,3144,3145,3146,4112,4120,4121,4122,4125) ORDER BY oid", []string{"id", "args"}, []string{"OID", "TEXT"}, [][]any{
				{oid(753), "829"}, {oid(3144), "829"}, {oid(3145), "829 829"}, {oid(3146), "829 829"},
				{oid(4112), "774"}, {oid(4120), "774"}, {oid(4121), "774 774"}, {oid(4122), "774 774"}, {oid(4125), "774"},
			}))
		}
		run(fixture.Select("SELECT oid AS id,oprname AS name,oprkind AS kind,oprleft AS l,oprright AS r,oprresult AS result,oprcode AS routine,oprcom AS comm,oprnegate AS neg,oprrest AS restriction,oprjoin AS joiner,oprcanhash AS hash,oprcanmerge AS merge FROM pg_operator WHERE oid IN (3147,3148,3149,3368,3369,3370) ORDER BY oid", []string{"id", "name", "kind", "l", "r", "result", "routine", "comm", "neg", "restriction", "joiner", "hash", "merge"}, []string{"OID", "NAME", "CHAR", "OID", "OID", "OID", "REGPROC", "OID", "OID", "REGPROC", "REGPROC", "BOOL", "BOOL"}, [][]any{
			{oid(3147), "~", "l", oid(0), oid(829), oid(829), "macaddr_not", oid(0), oid(0), "-", "-", false, false},
			{oid(3148), "&", "b", oid(829), oid(829), oid(829), "macaddr_and", oid(0), oid(0), "-", "-", false, false},
			{oid(3149), "|", "b", oid(829), oid(829), oid(829), "macaddr_or", oid(0), oid(0), "-", "-", false, false},
			{oid(3368), "~", "l", oid(0), oid(774), oid(774), "macaddr8_not", oid(0), oid(0), "-", "-", false, false},
			{oid(3369), "&", "b", oid(774), oid(774), oid(774), "macaddr8_and", oid(0), oid(0), "-", "-", false, false},
			{oid(3370), "|", "b", oid(774), oid(774), oid(774), "macaddr8_or", oid(0), oid(0), "-", "-", false, false},
		}))
	})
}
