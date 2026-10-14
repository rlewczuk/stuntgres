package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceTemporaryTableCapability(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Exec(s.C, "CREATE TEMP SEQUENCE seed START 31 CACHE 5", 0)
		s.Exec(s.Peer, "CREATE TEMP SEQUENCE peer_seed START 81 CACHE 5", 0)
		s.Exec(s.C, "SET search_path TO pg_temp", 0)
		s.Exec(s.Peer, "SET search_path TO pg_temp", 0)
		own, foreign := fixture.TempReferenceNamespace(s.T, s, false), fixture.TempReferenceNamespace(s.T, s, true)
		if own == foreign {
			s.T.Fatal(own, foreign)
		}
		s.Value(s.C, "SELECT nextval('seed')", 31)
		log := s.State(s.C, "seed", 35, true)
		for _, c := range []struct{ q, position string }{
			{"CREATE TABLE " + foreign + ".forbidden(v INT)", "14"},
			{"CREATE TABLE IF NOT EXISTS " + foreign + ".peer_seed(v INT)", "28"},
		} {
			s.Utility(c.q, fixture.TempReferenceDiagnostic("42P16", "cannot create relations in temporary schemas of other sessions", "", c.position))
		}
		for _, c := range []struct{ q, name string }{
			{"CREATE TABLE " + own + ".qualified(v INT)", "qualified"},
			{"CREATE TABLE path_table(v INT)", "path_table"},
		} {
			s.Utility(c.q, nil)
			s.Exec(s.C, "INSERT INTO "+c.name+" VALUES(7)", 1)
			fixture.TemporaryTableRows(s, s.C, own+"."+c.name, 7)
		}
		if after := s.State(s.C, "seed", 35, true); after != log {
			s.T.Fatal(log, after)
		}
		s.Value(s.C, "SELECT currval('seed')", 31)
		s.Value(s.C, "SELECT lastval()", 31)
		s.Value(s.C, "SELECT nextval('seed')", 32)
		s.Utility("CREATE TABLE public.persistent(v INT)", nil)
		s.Retire()
		retained := fixture.Select("SELECT nspowner::bigint AS nspowner,nspacl FROM pg_catalog.pg_namespace WHERE nspname=$1",
			[]string{"nspowner", "nspacl"}, []string{"INT8", "_ACLITEM"}, [][]any{{int64(10), nil}}, own)
		retained.Prepared = s.Prepared
		fixture.RunStep(s.T, s.P.Ctx, s.Peer, s.Record, retained)
		for _, name := range []string{"seed", "qualified", "path_table"} {
			if s.Postgres {
				var absent bool
				if err := s.Peer.QueryRowContext(s.P.Ctx, "SELECT to_regclass($1) IS NULL", own+"."+name).Scan(&absent); err != nil || !absent {
					s.T.Fatal(name, absent, err)
				}
			}
			// Physical teardown removes children, not the namespace identity.
			fixture.RunStep(s.T, s.P.Ctx, s.Peer, s.Record, fixture.Select(
				"SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2",
				[]string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, own, name))
			column, position := "v", "15"
			if name == "seed" {
				column, position = "last_value", "24"
			}
			missing := fixture.ImplicitError("SELECT "+column+" FROM "+own+"."+name,
				fixture.Diagnostic{Code: "42P01", Message: `relation "` + own + "." + name + `" does not exist`, Position: position})
			missing.SelectRows, missing.Prepared = true, s.Prepared
			fixture.RunStep(s.T, s.P.Ctx, s.Peer, s.Record, missing)
		}
		s.Value(s.Peer, "SELECT nextval('peer_seed')", 81)
		fixture.TemporaryTableRows(s, s.Peer, "public.persistent")
	})
}
