package reference_test

import (
	"strconv"
	"strings"

	"stuntgres/reference/fixture"
)

// RSR-MI goldens are prescribed independently of provider output. A request is
// one reached nonempty source Open; mutation bytes are accepted row-slot images,
// not payload length. PostgreSQL has separate physical invariants, not parity.
type pgssCounterWant struct {
	hit, read, dirty, localHit, localRead, localDirty int64
	records, bytes, planned, launched                int64
	sharedRequests                                 int64
	anySharedSplit                                 bool
	pgShared, pgLocal, pgWAL                         bool
	pgNoWAL, serial                                bool
}

func pgssCounterSetup(s *pgssScenario) {
	s.t.Helper()
	for _, q := range []string{"SET jit=off", "SET max_parallel_workers_per_gather=0"} {
		s.work(fixture.Exec(q, 0))
	}
}

func pgssCounterCheck(s *pgssScenario, pattern string, calls, rows int64, want pgssCounterWant) {
	s.t.Helper()
	pgssCounterCheckTop(s, pattern, true, calls, rows, want)
}

func pgssCounterCheckTop(s *pgssScenario, pattern string, top bool, calls, rows int64, want pgssCounterWant) {
	s.t.Helper()
	native := map[string]int64{
		"shared_blks_hit": want.hit, "shared_blks_read": want.read, "shared_blks_dirtied": want.dirty,
		"local_blks_hit": want.localHit, "local_blks_read": want.localRead, "local_blks_dirtied": want.localDirty,
		"wal_records": want.records, "wal_bytes": want.bytes,
		"parallel_workers_to_launch": want.planned, "parallel_workers_launched": want.launched,
	}
	names := []string{"calls", "rows"}
	types := []string{"INT8", "INT8"}
	values := []any{calls, rows}
	selects := []string{"calls", "rows"}
	for i, name := range pgssNames[18:50] {
		condition := name + "=" + strconv.FormatInt(native[name], 10)
		if !s.b.PG && want.anySharedSplit && (name == "shared_blks_hit" || name == "shared_blks_read") {
			condition = "shared_blks_hit+shared_blks_read=" + strconv.FormatInt(want.sharedRequests, 10)
		}
		if s.b.PG {
			condition = name + ">=0"
			if pgssTypes[i+18] == "FLOAT8" {
				condition += " AND " + name + "<'Infinity'::double precision"
			}
			// Every shared scenario explicitly disables the genuine JIT producer.
			if strings.HasPrefix(name, "jit_") || (want.serial && strings.HasPrefix(name, "parallel_workers_")) || (want.pgNoWAL && strings.HasPrefix(name, "wal_")) {
				condition = name + "=0"
			}
		}
		selects = append(selects, "("+condition+") AS "+name)
		names, types, values = append(names, name), append(types, "BOOL"), append(values, true)
	}
	for _, extra := range []struct {
		name, condition string
		enabled         bool
	}{
		{"shared_work", "shared_blks_hit+shared_blks_read>0", want.pgShared},
		{"local_work", "local_blks_hit+local_blks_read>0", want.pgLocal},
		{"logged_work", "wal_records>0 AND wal_bytes>0", want.pgWAL},
		{"worker_bounds", "parallel_workers_launched<=parallel_workers_to_launch", true},
	} {
		if extra.enabled {
			selects = append(selects, "("+extra.condition+") AS "+extra.name)
			names, types, values = append(names, extra.name), append(types, "BOOL"), append(values, true)
		}
	}
	q := "SELECT " + strings.Join(selects, ",") + " FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND toplevel=$2 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
	s.both(fixture.Select(q, names, types, [][]any{values}, pattern, top))
}

// Counter-only snapshots permit exact same-server lifecycle comparisons without
// equating physical values or clock instants between independent backends.
func pgssCounterSnapshot(s *pgssScenario, pattern string) [][]any {
	s.t.Helper()
	return pgssResetRead(s, "SELECT "+strings.Join(pgssNames[18:50], ",")+" FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) ORDER BY queryid,toplevel", pattern)
}

func pgssCounterRequests(pg bool, schema, pattern string, requests int64) fixture.Step {
	condition := "COALESCE(sum(shared_blks_hit+shared_blks_read),0)=$2::bigint"
	if pg {
		condition = "COALESCE(sum(shared_blks_hit+shared_blks_read),0)>=0 AND $2::bigint>=0"
		if requests > 0 {
			condition = "COALESCE(sum(shared_blks_hit+shared_blks_read),0)>0 AND $2::bigint>0"
		}
	}
	q := "SELECT " + condition + " AS requests FROM " + schema + ".pg_stat_statements WHERE query LIKE $1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
	return fixture.Select(q, []string{"requests"}, []string{"BOOL"}, [][]any{{true}}, pattern, requests)
}
