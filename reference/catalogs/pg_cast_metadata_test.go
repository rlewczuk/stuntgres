package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

const pgCastProbe = "SELECT * FROM pg_catalog.pg_cast WHERE castsource=$1::oid AND casttarget=$2::oid"

// Independent PostgreSQL REL_18_6 contract for executable builtin casts.
// Row OIDs are generated identities, intentionally not PostgreSQL goldens.
func pgCastFacts() [][]any {
	rows := [][]any{
		{int64(18), int64(25), int64(946), "i", "f"},
		{int64(194), int64(25), int64(0), "i", "b"},
		{int64(1083), int64(1186), int64(1370), "i", "f"},
		{int64(21), int64(23), int64(313), "i", "f"},
		{int64(21), int64(20), int64(754), "i", "f"},
		{int64(21), int64(700), int64(236), "i", "f"},
		{int64(21), int64(701), int64(235), "i", "f"},
		{int64(21), int64(1700), int64(1782), "i", "f"},
		{int64(23), int64(21), int64(314), "a", "f"},
		{int64(23), int64(20), int64(481), "i", "f"},
		{int64(23), int64(700), int64(318), "i", "f"},
		{int64(23), int64(701), int64(316), "i", "f"},
		{int64(23), int64(1700), int64(1740), "i", "f"},
		{int64(20), int64(21), int64(714), "a", "f"},
		{int64(20), int64(23), int64(480), "a", "f"},
		{int64(20), int64(700), int64(652), "i", "f"},
		{int64(20), int64(701), int64(482), "i", "f"},
		{int64(20), int64(1700), int64(1781), "i", "f"},
		{int64(700), int64(21), int64(238), "a", "f"},
		{int64(700), int64(23), int64(319), "a", "f"},
		{int64(700), int64(20), int64(653), "a", "f"},
		{int64(700), int64(701), int64(311), "i", "f"},
		{int64(700), int64(1700), int64(1742), "a", "f"},
		{int64(701), int64(21), int64(237), "a", "f"},
		{int64(701), int64(23), int64(317), "a", "f"},
		{int64(701), int64(20), int64(483), "a", "f"},
		{int64(701), int64(700), int64(312), "a", "f"},
		{int64(701), int64(1700), int64(1743), "a", "f"},
		{int64(1700), int64(21), int64(1783), "a", "f"},
		{int64(1700), int64(23), int64(1744), "a", "f"},
		{int64(1700), int64(20), int64(1779), "a", "f"},
		{int64(1700), int64(700), int64(1745), "i", "f"},
		{int64(1700), int64(701), int64(1746), "i", "f"},
		{int64(21), int64(26), int64(313), "i", "f"},
		{int64(23), int64(26), int64(0), "i", "b"},
		{int64(20), int64(26), int64(1287), "i", "f"},
		{int64(26), int64(23), int64(0), "a", "b"},
		{int64(26), int64(20), int64(1288), "a", "f"},
		{int64(1082), int64(1114), int64(2024), "i", "f"},
		{int64(1082), int64(1184), int64(1174), "i", "f"},
		{int64(1083), int64(1266), int64(2047), "i", "f"},
		{int64(1114), int64(1082), int64(2029), "a", "f"},
		{int64(1114), int64(1083), int64(1316), "a", "f"},
		{int64(1114), int64(1184), int64(2028), "i", "f"},
		{int64(1184), int64(1082), int64(1178), "a", "f"},
		{int64(1184), int64(1083), int64(2019), "a", "f"},
		{int64(1184), int64(1114), int64(2027), "a", "f"},
		{int64(1184), int64(1266), int64(1388), "a", "f"},
		{int64(1266), int64(1083), int64(2046), "a", "f"},
		{int64(25), int64(1042), int64(0), "i", "b"},
		{int64(25), int64(1043), int64(0), "i", "b"},
		{int64(1042), int64(25), int64(401), "i", "f"},
		{int64(1042), int64(1043), int64(401), "i", "f"},
		{int64(1043), int64(25), int64(0), "i", "b"},
		{int64(1043), int64(1042), int64(0), "i", "b"},
		{int64(23), int64(16), int64(2557), "e", "f"},
		{int64(16), int64(23), int64(2558), "e", "f"},
		{int64(16), int64(25), int64(2971), "a", "f"},
		{int64(1042), int64(1042), int64(668), "i", "f"},
		{int64(1043), int64(1043), int64(669), "i", "f"},
		{int64(1083), int64(1083), int64(1968), "i", "f"},
		{int64(1114), int64(1114), int64(1961), "i", "f"},
		{int64(1184), int64(1184), int64(1967), "i", "f"},
		{int64(1186), int64(1186), int64(1200), "i", "f"},
		{int64(1266), int64(1266), int64(1969), "i", "f"},
		{int64(1700), int64(1700), int64(1703), "i", "f"},
		{int64(114), int64(3802), int64(0), "a", "i"},
		{int64(3802), int64(114), int64(0), "a", "i"},
		{int64(20), int64(790), int64(3812), "a", "f"},
		{int64(23), int64(790), int64(3811), "a", "f"},
		{int64(790), int64(1700), int64(3823), "a", "f"},
		{int64(1700), int64(790), int64(3824), "a", "f"},
		{int64(5069), int64(28), int64(5071), "e", "f"},
	}
	return pgCastReleasedFacts(rows)
}

func pgCastScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

// Independently assert the fixed fields and identity invariants. Actual generated
// row IDs are remembered only for subsequent within-backend stability checks.
func pgCastIdentities(t *testing.T, p *fixture.Pair, b fixture.Backend) map[[2]int64]int64 {
	t.Helper()
	facts := fixture.PgCastOrdered(pgCastFacts())
	query := "SELECT * FROM pg_catalog.pg_cast"
	if b.PG {
		query += fixture.PgCastPredicate(facts)
	}
	query += " ORDER BY castsource,casttarget"
	rows, err := b.DB.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	pgCastMetadata(t, rows, b.PG)
	ids, seen := map[[2]int64]int64{}, map[int64]bool{}
	index := 0
	for rows.Next() {
		var oid, source, target, function int64
		var context, method string
		if err := rows.Scan(&oid, &source, &target, &function, &context, &method); err != nil {
			t.Fatal(err)
		}
		if index >= len(facts) {
			t.Fatal("unexpected extra cast", source, target)
		}
		got := []any{source, target, function, context, method}
		if !reflect.DeepEqual(got, facts[index]) {
			t.Fatalf("cast %d got %v want %v", index, got, facts[index])
		}
		pair := [2]int64{source, target}
		if oid <= 0 || oid > 4294967295 || seen[oid] || ids[pair] != 0 {
			t.Fatal("invalid/duplicate cast identity", oid, pair)
		}
		if (method == "f") != (function != 0) {
			t.Fatal("function/method mismatch", got)
		}
		ids[pair], seen[oid] = oid, true
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if index != len(facts) {
		t.Fatal("complete supported cast count", index)
	}
	return ids
}

func pgCastHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, ids map[[2]int64]int64) {
	t.Helper()
	stmt, err := b.DB.PrepareContext(p.Ctx, pgCastProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgCastProbe}
	for _, tc := range []struct {
		source, target any
		facts          []any
	}{
		{int64(23), int64(20), []any{int64(23), int64(20), int64(481), "i", "f"}},
		{int64(23), int64(23), nil}, {nil, int64(20), nil}, {int64(23), nil, nil},
		{int64(26), int64(23), []any{int64(26), int64(23), int64(0), "a", "b"}},
		{int64(5069), int64(28), []any{int64(5069), int64(28), int64(5071), "e", "f"}},
	} {
		want := [][]any{}
		if tc.facts != nil {
			pair := [2]int64{tc.source.(int64), tc.target.(int64)}
			want = append(want, append([]any{ids[pair]}, tc.facts...))
		}
		s := fixture.Select(pgCastProbe, fixture.PgCastColumns, fixture.PgCastTypes, want, tc.source, tc.target)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgCastMetadata(t, rows, b.PG) }
		fixture.PgDescriptionExecutorRows(t, p, b, held, s)
	}
	for _, value := range []string{"bad", ""} {
		s := pgCastParameterError(pgCastProbe, "oid", value, value, int64(20))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	fixture.PgDescriptionExecutorRows(t, p, b, held, fixture.Select(pgCastProbe, fixture.PgCastColumns, fixture.PgCastTypes,
		[][]any{{ids[[2]int64{23, 20}], int64(23), int64(20), int64(481), "i", "f"}}, int64(23), int64(20)))
}

func TestReferencePGCastMetadata(t *testing.T) {
	pgCastScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.PgCastCheckFacts(t, p, b, b.DB, pgCastFacts(), true)
		ids := pgCastIdentities(t, p, b)
		pgCastCheckIdentityRows(t, p, b, ids)
		pgCastHeld(t, p, b, ids)
		if again := pgCastIdentities(t, p, b); !reflect.DeepEqual(again, ids) {
			t.Fatal("unstable cast identities", ids, again)
		}
	})
}

func pgCastCheckIdentityRows(t *testing.T, p *fixture.Pair, b fixture.Backend, ids map[[2]int64]int64) {
	t.Helper()
	facts := fixture.PgCastOrdered(pgCastFacts())
	want := make([][]any, len(facts))
	for i, fact := range facts {
		want[i] = append([]any{ids[[2]int64{fact[0].(int64), fact[1].(int64)}]}, fact...)
	}
	query := "SELECT * FROM pg_catalog.pg_cast"
	if b.PG {
		query += fixture.PgCastPredicate(facts)
	}
	query += " ORDER BY castsource,casttarget"
	s := fixture.Select(query, fixture.PgCastColumns, fixture.PgCastTypes, want)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgCastMetadata(t, rows, b.PG) }
	fixture.PgDescriptionRows(t, p, b, s)
	fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
}
