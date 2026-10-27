package reference_test

import (
	"bytes"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Profiles are stored configuration, never the cause of access-method selection.
var geqoProfiles = [][]string{
	{"off","1","0","1","0","1.5","2"},
	{"on","10","1","2","1","2","2"},
}

func (s *physicalCase) geqoProfile(values []string, local bool) {
	prefix := "SET "
	if local { prefix = "SET LOCAL " }
	for i, d := range geqoSettings {
		s.run(fixture.Exec(prefix+d.name+"="+physicalLiteral(values[i]),0))
		s.geqoValue(d,values[i],"session",true)
	}
}

func (s *physicalCase) geqoChoices() {
	s.plannerCostSeed()
	const query = "SELECT n,s FROM planner_cost_rows WHERE n=$1"
	held, err := s.conn.PrepareContext(s.p.Ctx,query)
	if err != nil { s.t.Fatal(err) }
	defer held.Close()
	check := func(hash bool) {
		for _, prepared := range []bool{false,true} {
			plan := s.plannerCostExplain(hash,prepared)
			if !s.pg && strings.Contains(strings.ToLower(plan),"geqo") { s.t.Fatal("fabricated native genetic plan",plan) }
			step := plannerCostBag("SELECT n,s FROM planner_cost_rows WHERE n=2"); step.Prepared = prepared; s.run(step)
			bound := plannerCostBag(query,2); bound.Prepared = prepared; s.run(bound)
		}
		fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:held,SQL:query},s.recorder,plannerCostBag(query,2))
		for _, tc := range []struct{ arg any; rows [][]any }{{1,[][]any{{int64(1),"a"}}},{nil,nil}} {
			fixture.RunStep(s.t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:held,SQL:query},s.recorder,fixture.Select(query,[]string{"n","s"},[]string{"INT4","TEXT"},tc.rows,tc.arg))
		}
	}
	cycle := func() {
		for _, cost := range []string{"0.005","1000000","0.005"} {
			// This CPU cost, not GEQO, controls the real native access method.
			s.plannerCostSet("cpu_index_tuple_cost",cost,false)
			check(cost == "0.005")
		}
	}
	for _, profile := range geqoProfiles { s.geqoProfile(profile,false); cycle() }
	// Use the expensive profile around LOCAL state changes. Explicit native
	// transactions do not admit published hash roots (query_admission.go);
	// the cheap/hash witness remains the autocommit cycles above and below.
	s.plannerCostSet("cpu_index_tuple_cost","1000000",false)
	check(false)
	s.run(fixture.Exec("BEGIN",0)); s.geqoProfile(geqoProfiles[0],true)
	// GEQO LOCAL changes do not turn this full scan into a genetic plan.
	check(false)
	s.run(fixture.Exec("ROLLBACK",0))
	for i, d := range geqoSettings { s.geqoValue(d,geqoProfiles[1][i],"session",true) }
	check(false)
	cycle()
}

func TestReferenceGeqoBoundaries(t *testing.T) {
	physicalCases(t,nil,func(s *physicalCase) {
		s.geqoChoices()
		if s.pg { return } // Logical controls have no PostgreSQL equivalent.
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx,s.p.Native,&dump); err != nil { s.t.Fatal(err) }
		for _, d := range geqoSettings { if strings.Contains(dump.String(),d.name) { s.t.Fatal("serialized live GEQO state",d.name) } }
		if err := stuntgres.Reset(s.p.Ctx,s.p.Native); err != nil { s.t.Fatal(err) }
		for i, d := range geqoSettings { s.geqoValue(d,geqoProfiles[1][i],"session",true) }
		if err := stuntgres.Restore(s.p.Ctx,s.p.Native,bytes.NewReader(dump.Bytes())); err != nil { s.t.Fatal(err) }
		for i, d := range geqoSettings { s.geqoValue(d,geqoProfiles[1][i],"session",true) }
		s.run(plannerCostBag("SELECT n,s FROM planner_cost_rows WHERE n=2"))
		// Returning a borrower does not reset its physical session configuration.
		pool := s.p.Memory
		pool.SetMaxOpenConns(1); pool.SetMaxIdleConns(0); pool.SetMaxIdleConns(1)
		identity := fixture.SettingsPhysical(s.t,s.conn)
		s.conn.Close()
		reused, err := pool.Conn(s.p.Ctx); if err != nil { s.t.Fatal(err) }; defer reused.Close(); s.conn = reused
		if fixture.SettingsPhysical(s.t,reused) != identity { s.t.Fatal("physical session not reused") }
		for i, d := range geqoSettings { s.geqoValue(d,geqoProfiles[1][i],"session",true) }
		if err := reused.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err,driver.ErrBadConn) { s.t.Fatal(err) }
		reused.Close()
		replacement, err := pool.Conn(s.p.Ctx); if err != nil { s.t.Fatal(err) }; defer replacement.Close(); s.conn = replacement
		if fixture.SettingsPhysical(s.t,replacement) == identity { s.t.Fatal("physical session not replaced") }
		for _, d := range geqoSettings { s.geqoValue(d,d.boot,"default",true) }
	})
}

func TestReferenceGeqoFeatureSmoke(t *testing.T) {
	physicalCases(t,[]string{"geqo_smoke_login"},func(s *physicalCase) {
		s.geqoInventory(false)
		s.run(fixture.Exec("CREATE ROLE geqo_smoke_login LOGIN PASSWORD 'role-core-fixture'",0))
		ordinary := s.login("geqo_smoke_login")
		ordinary.plannerCostSet("geqo_pool_size","1",true)
		ordinary.resourceValue("geqo_pool_size","1","1","session",true)
		s.geqoChoices()
		d := geqoSettings[4]
		s.run(physicalError("SET geqo_seed='1.01'",geqoBounds(d,"1.01"),true,false))
		s.geqoValue(d,"1","session",true)
	})
}
