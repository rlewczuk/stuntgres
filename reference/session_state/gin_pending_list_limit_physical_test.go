package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceGINPendingListLimitPhysicalBoundary(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.run(fixture.Exec("SET gin_pending_list_limit='64kB'", 0))
		const heldSQL = "SELECT gin_clean_pending_list($1::regclass)>0 AS cleaned"
		statement, err := s.conn.PrepareContext(s.p.Ctx, heldSQL)
		if err != nil {
			s.t.Fatal("prepare cleanup at low threshold without demand", err)
		}
		defer statement.Close()
		s.ginInsertPair()
		s.run(fixture.Exec("SET gin_pending_list_limit='64kB'", 0))
		// This first high measurement executes the statement prepared at 64kB,
		// after the high insertion at 8MB; it is the only positive high drain.
		// PostgreSQL already flushed low during insertion; native automatic
		// threshold flushing is unavailable, so its first explicit drain has work.
		s.run(ginLimitDemand(s.pg, "SELECT gin_clean_pending_list('gin_limit_low_i'::regclass)>0 AS cleaned", "BOOL", !s.pg))
		s.ginLogical()
		s.run(fixture.Exec("SET gin_pending_list_limit='8MB'", 0))
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: statement, SQL: heldSQL}, s.recorder, ginLimitDemand(s.pg, heldSQL, "BOOL", true, "gin_limit_high_i"))
		s.ginLogical()
		for _, value := range []string{"64kB", "8MB"} {
			s.run(fixture.Exec("SET gin_pending_list_limit="+physicalLiteral(value), 0))
			for _, index := range []string{"gin_limit_low_i", "gin_limit_high_i"} {
				step := ginLimitDemand(s.pg, "SELECT pg_catalog.gin_clean_pending_list($1::regclass) AS cleaned", "INT8", int64(0), index)
				s.run(step)
				s.run(fixture.Prepared(step))
				s.ginHeld(step)
			}
			s.ginLogical()
		}
		for _, step := range []fixture.Step{
			ginLimitResult(s.pg, "SELECT gin_clean_pending_list(NULL::regclass) AS cleaned", "INT8", [][]any{{nil}}),
			ginLimitResult(s.pg, "SELECT pg_catalog.gin_clean_pending_list($1::regclass) AS cleaned", "INT8", [][]any{{nil}}, nil),
			ginLimitResult(s.pg, "SELECT gin_clean_pending_list($1::regclass) AS cleaned LIMIT 0", "INT8", nil, "gin_limit_low"),
		} {
			s.run(step)
			s.run(fixture.Prepared(step))
			s.ginHeld(step)
		}
		s.run(fixture.Select("SELECT oid::integer,proowner::integer,pronamespace::integer,prolang::integer,proargtypes::text,prorettype::integer,proisstrict,provolatile,proparallel,prosrc,procost,pronargdefaults,proallargtypes::text,proargmodes::text,proargnames::text,proargdefaults IS NULL AS no_defaults,proacl::text,proconfig::text FROM pg_proc WHERE oid=3789", []string{"oid", "proowner", "pronamespace", "prolang", "proargtypes", "prorettype", "proisstrict", "provolatile", "proparallel", "prosrc", "procost", "pronargdefaults", "proallargtypes", "proargmodes", "proargnames", "no_defaults", "proacl", "proconfig"}, []string{"INT4", "INT4", "INT4", "INT4", "TEXT", "INT4", "BOOL", "CHAR", "CHAR", "TEXT", "FLOAT4", "INT2", "TEXT", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT"}, [][]any{{int64(3789), int64(10), int64(11), int64(12), "2205", int64(20), true, "v", "u", "gin_clean_pending_list", float64(1), int64(0), nil, nil, nil, true, nil, nil}}))
		s.run(fixture.Exec("CREATE VIEW gin_limit_cleanup_view AS SELECT gin_clean_pending_list('gin_limit_high_i'::regclass) AS cleaned", 0))
		empty := ginLimitResult(s.pg, "SELECT cleaned FROM gin_limit_cleanup_view LIMIT 0", "INT8", nil)
		s.run(empty)
		s.run(fixture.Prepared(empty))
		s.ginHeld(empty)
		s.ginLogical()
		if s.pg {
			return
		}
		// Same SQL values, genuinely new produced version: native Dump/Restore
		// must retain its pending placement, not infer it from the threshold.
		s.run(fixture.Exec("UPDATE gin_limit_high SET a=a", 1))
		s.ginLogical()
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
			s.t.Fatal(err)
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("gin-limit-restored")
		if err != nil {
			s.t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		if err := stuntgres.Restore(s.p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
			s.t.Fatal(err)
		}
		recorded, recorder := fixture.RecordedNativePool(s.t, connector)
		c, err := recorded.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer c.Close()
		replay := &physicalCase{s.t, s.p, c, false, recorder}
		replay.ginState("4MB", "4096", "default", false)
		replay.run(empty)
		replay.run(fixture.Prepared(empty))
		replay.ginHeld(empty)
		replay.ginLogical()
		replay.run(ginLimitDemand(false, "SELECT cleaned>0 AS cleaned FROM gin_limit_cleanup_view", "BOOL", true))
		replay.run(ginLimitDemand(false, "SELECT cleaned FROM gin_limit_cleanup_view", "INT8", int64(0)))
		replay.ginLogical()
	})
}

func TestReferenceGINPendingListLimitFeatureSmoke(t *testing.T) {
	physicalCases(t, []string{"gin_limit_smoke_login"}, func(s *physicalCase) {
		s.ginInsertPair()
		s.run(ginLimitDemand(s.pg, "SELECT gin_clean_pending_list('gin_limit_low_i'::regclass)>0 AS cleaned", "BOOL", !s.pg))
		s.run(ginLimitDemand(s.pg, "SELECT gin_clean_pending_list('gin_limit_high_i'::regclass)>0 AS cleaned", "BOOL", true))
		s.run(ginLimitDemand(s.pg, "SELECT gin_clean_pending_list('gin_limit_high_i'::regclass) AS cleaned", "INT8", int64(0)))
		s.run(ginLimitResult(s.pg, "SELECT gin_clean_pending_list(NULL::regclass) AS cleaned", "INT8", [][]any{{nil}}))
		s.run(ginLimitResult(s.pg, "SELECT gin_clean_pending_list('gin_limit_low_i'::regclass) AS cleaned LIMIT 0", "INT8", nil))
		s.run(fixture.Exec("CREATE ROLE gin_limit_smoke_login LOGIN PASSWORD 'role-core-fixture'", 0))
		s.run(fixture.Exec("GRANT USAGE ON SCHEMA public TO gin_limit_smoke_login", 0))
		ordinary := s.login("gin_limit_smoke_login")
		ordinary.run(fixture.Exec("SET gin_pending_list_limit='64kB'", 0))
		ordinary.ginState("64kB", "64", "session", false)
		ordinary.run(physicalError("SELECT gin_clean_pending_list('gin_limit_low_i'::regclass)", physicalDiagnostic("42501", "must be owner of index gin_limit_low_i"), false, true))
		for _, q := range []string{"BEGIN", "SAVEPOINT gin_limit_smoke", "SET LOCAL gin_pending_list_limit='64kB'", "ROLLBACK TO gin_limit_smoke"} {
			s.run(fixture.Exec(q, 0))
		}
		s.ginState("8MB", "8192", "session", false)
		s.run(fixture.Exec("COMMIT", 0))
		s.ginLogical()
	})
}
