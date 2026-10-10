package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

var pgCastColumns = []string{"oid", "castsource", "casttarget", "castfunc", "castcontext", "castmethod"}
var pgCastTypes = []string{"OID", "OID", "OID", "OID", "CHAR", "CHAR"}
var pgCastFactColumns = pgCastColumns[1:]
var pgCastFactTypes = pgCastTypes[1:]

const pgCastProbe = "SELECT * FROM pg_catalog.pg_cast WHERE castsource=$1::oid AND casttarget=$2::oid"

// Independently transcribed REL_18_0 pg_cast.dat / pg_proc.dat contract.
// Row OIDs are generated identities, intentionally not PostgreSQL goldens.
func pgCastFacts() [][]any {
	return [][]any{
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
	}
}

func pgCastOrdered(facts [][]any) [][]any {
	sort.Slice(facts, func(i, j int) bool {
		if facts[i][0] != facts[j][0] {
			return facts[i][0].(int64) < facts[j][0].(int64)
		}
		return facts[i][1].(int64) < facts[j][1].(int64)
	})
	return facts
}

func pgCastPredicate(facts [][]any) string {
	pairs := make([]string, len(facts))
	for i, row := range facts {
		pairs[i] = fmt.Sprintf("(castsource=%d AND casttarget=%d)", row[0], row[1])
	}
	return " WHERE " + strings.Join(pairs, " OR ")
}

func pgCastScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgCastCheckFacts(t *testing.T, p *referencePair, b referenceBackend, e referenceExecutor, facts [][]any, complete bool) {
	t.Helper()
	query := "SELECT castsource,casttarget,castfunc,castcontext,castmethod FROM pg_catalog.pg_cast"
	if b.pg || !complete {
		query += pgCastPredicate(facts)
	}
	query += " ORDER BY castsource,casttarget"
	s := referenceSelect(query, pgCastFactColumns, pgCastFactTypes, pgCastOrdered(facts))
	pgDescriptionExecutorRows(t, p, b, e, s)
	pgDescriptionExecutorRows(t, p, b, e, referencePrepared(s))
}

// Independently assert the fixed fields and identity invariants. Actual generated
// row IDs are remembered only for subsequent within-backend stability checks.
func pgCastIdentities(t *testing.T, p *referencePair, b referenceBackend) map[[2]int64]int64 {
	t.Helper()
	facts := pgCastOrdered(pgCastFacts())
	query := "SELECT * FROM pg_catalog.pg_cast"
	if b.pg {
		query += pgCastPredicate(facts)
	}
	query += " ORDER BY castsource,casttarget"
	rows, err := b.db.QueryContext(p.ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	pgCastMetadata(t, rows, b.pg)
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
	if index != 69 {
		t.Fatal("complete supported cast count", index)
	}
	return ids
}

func pgCastHeld(t *testing.T, p *referencePair, b referenceBackend, ids map[[2]int64]int64) {
	t.Helper()
	stmt, err := b.db.PrepareContext(p.ctx, pgCastProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: pgCastProbe}
	for _, tc := range []struct {
		source, target any
		facts          []any
	}{
		{int64(23), int64(20), []any{int64(23), int64(20), int64(481), "i", "f"}},
		{int64(23), int64(23), nil}, {nil, int64(20), nil}, {int64(23), nil, nil},
		{int64(26), int64(23), []any{int64(26), int64(23), int64(0), "a", "b"}},
	} {
		want := [][]any{}
		if tc.facts != nil {
			pair := [2]int64{tc.source.(int64), tc.target.(int64)}
			want = append(want, append([]any{ids[pair]}, tc.facts...))
		}
		s := referenceSelect(pgCastProbe, pgCastColumns, pgCastTypes, want, tc.source, tc.target)
		s.checkMetadata = func(t *testing.T, rows *sql.Rows) { pgCastMetadata(t, rows, b.pg) }
		pgDescriptionExecutorRows(t, p, b, held, s)
	}
	for _, value := range []string{"bad", ""} {
		s := pgCastParameterError(pgCastProbe, "oid", value, value, int64(20))
		referenceRunStep(t, p.ctx, held, b.recorder, s)
	}
	pgDescriptionExecutorRows(t, p, b, held, referenceSelect(pgCastProbe, pgCastColumns, pgCastTypes,
		[][]any{{ids[[2]int64{23, 20}], int64(23), int64(20), int64(481), "i", "f"}}, int64(23), int64(20)))
}

func TestReferencePGCastMetadata(t *testing.T) {
	pgCastScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		pgCastCheckFacts(t, p, b, b.db, pgCastFacts(), true)
		ids := pgCastIdentities(t, p, b)
		pgCastCheckIdentityRows(t, p, b, ids)
		pgCastHeld(t, p, b, ids)
		if again := pgCastIdentities(t, p, b); !reflect.DeepEqual(again, ids) {
			t.Fatal("unstable cast identities", ids, again)
		}
	})
}

func pgCastCheckIdentityRows(t *testing.T, p *referencePair, b referenceBackend, ids map[[2]int64]int64) {
	t.Helper()
	facts := pgCastOrdered(pgCastFacts())
	want := make([][]any, len(facts))
	for i, fact := range facts {
		want[i] = append([]any{ids[[2]int64{fact[0].(int64), fact[1].(int64)}]}, fact...)
	}
	query := "SELECT * FROM pg_catalog.pg_cast"
	if b.pg {
		query += pgCastPredicate(facts)
	}
	query += " ORDER BY castsource,casttarget"
	s := referenceSelect(query, pgCastColumns, pgCastTypes, want)
	s.checkMetadata = func(t *testing.T, rows *sql.Rows) { pgCastMetadata(t, rows, b.pg) }
	pgDescriptionRows(t, p, b, s)
	pgDescriptionRows(t, p, b, referencePrepared(s))
}
