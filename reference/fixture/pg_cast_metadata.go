package fixture

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

var PgCastColumns = []string{"oid", "castsource", "casttarget", "castfunc", "castcontext", "castmethod"}

var PgCastTypes = []string{"OID", "OID", "OID", "OID", "CHAR", "CHAR"}

var PgCastFactColumns = PgCastColumns[1:]

var PgCastFactTypes = PgCastTypes[1:]

func PgCastOrdered(facts [][]any) [][]any {
	sort.Slice(facts, func(i, j int) bool {
		if facts[i][0] != facts[j][0] {
			return facts[i][0].(int64) < facts[j][0].(int64)
		}
		return facts[i][1].(int64) < facts[j][1].(int64)
	})
	return facts
}

func PgCastPredicate(facts [][]any) string {
	pairs := make([]string, len(facts))
	for i, row := range facts {
		pairs[i] = fmt.Sprintf("(castsource=%d AND casttarget=%d)", row[0], row[1])
	}
	return " WHERE " + strings.Join(pairs, " OR ")
}

func PgCastCheckFacts(t *testing.T, p *Pair, b Backend, e Executor, facts [][]any, complete bool) {
	t.Helper()
	query := "SELECT castsource,casttarget,castfunc,castcontext,castmethod FROM pg_catalog.pg_cast"
	if b.PG || !complete {
		query += PgCastPredicate(facts)
	}
	query += " ORDER BY castsource,casttarget"
	s := Select(query, PgCastFactColumns, PgCastFactTypes, PgCastOrdered(facts))
	PgDescriptionExecutorRows(t, p, b, e, s)
	PgDescriptionExecutorRows(t, p, b, e, Prepared(s))
}
