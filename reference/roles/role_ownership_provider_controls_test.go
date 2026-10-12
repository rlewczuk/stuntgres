package reference_test

import (
	"bytes"
	"database/sql"
	"github.com/lib/pq"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

type ownershipProviderMutationReader struct {
	io.Reader
	mutate func() error
	done   bool
}

func (r *ownershipProviderMutationReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		if err := r.mutate(); err != nil {
			return 0, err
		}
	}
	return r.Reader.Read(p)
}

func TestReferenceRoleOwnershipProviderControls(t *testing.T) {
	ownershipProviderCases(t, func(s *membershipScenario, database string) {
		ownershipProviderSetup(s, database, true)
		s.exec("GRANT own_other TO own_target WITH INHERIT TRUE, SET TRUE")
		ownershipProviderOwners(s, "own_source", "own_source")
		before := ownershipProviderIDs(s)
		s.exec("REASSIGN OWNED BY own_source TO own_target")
		ownershipProviderOwners(s, "own_target", "own_target")
		ownershipProviderStable(s, before)
		s.exec("ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO own_source")
		s.exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_reader WITH GRANT OPTION")
		ownershipDatabaseOwner(s, "own_source")
		s.actor("own_other")
		s.exec("CREATE TABLE own_provider_saved(v TEXT DEFAULT own_provider.unaccent('Hôtel'))")
		s.run(fixture.Exec("INSERT INTO own_provider_saved DEFAULT VALUES", 1))
		s.actor("")
		ownershipHotel(s, "SELECT v FROM own_provider_saved")
		source := s.p.Native
		if s.backend.PG {
			// This independently expected native input supplies its real public codec;
			// it does not replace any preceding genuine PostgreSQL owner operations.
			source = fixture.CatalogControlPool(s.t, stuntgres.NewDriver(), "ownership_provider_oracle_seed")
			ownershipProviderControlRoles(s, source)
			var name string
			if err := source.QueryRowContext(s.p.Ctx, "SELECT current_database()").Scan(&name); err != nil {
				s.t.Fatal(err)
			}
			for _, q := range []string{"GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(name) + " TO own_target", "GRANT own_other TO own_target WITH INHERIT TRUE, SET TRUE"} {
				if _, err := source.ExecContext(s.p.Ctx, q); err != nil {
					s.t.Fatal(err)
				}
			}
			seed := `CREATE SCHEMA own_provider AUTHORIZATION own_other; SET SESSION AUTHORIZATION "own_target"; CREATE EXTENSION unaccent SCHEMA own_provider; SET SESSION AUTHORIZATION postgres; SET SESSION AUTHORIZATION "own_other"; CREATE TABLE own_provider_saved(v TEXT DEFAULT own_provider.unaccent('Hôtel')); INSERT INTO own_provider_saved DEFAULT VALUES; SET SESSION AUTHORIZATION postgres;`
			if err := stuntgres.Restore(s.p.Ctx, source, strings.NewReader(seed)); err != nil {
				s.t.Fatal(err)
			}
			var first bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, source, &first); err != nil {
				s.t.Fatal(err)
			}
			if err := stuntgres.Restore(s.p.Ctx, source, strings.NewReader(ownershipProviderOwnerScript(s.t, first.String(), "own_target"))); err != nil {
				s.t.Fatal(err)
			}
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx, source, &dump); err != nil {
			s.t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "-- stuntgres-extension-owners-v1 ") || strings.Contains(dump.String(), "ALTER DATABASE") || strings.Contains(dump.String(), "GRANT CREATE ON DATABASE") {
			s.t.Fatal("portable local owners, not destination DB metadata", dump.String())
		}
		if s.backend.PG {
			s.exec("DROP OWNED BY own_target,own_other CASCADE")
			s.exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_target")
			if _, err := s.c.ExecContext(s.p.Ctx, dump.String()); err != nil {
				s.t.Fatal("genuine PG replay", err)
			}
			s.actor("")
			// PostgreSQL ignores native member metadata comments. Trusted recreation by
			// an ordinary target really gives bootstrap members, not target members.
			ownershipProviderOwners(s, "own_target", s.bootstrap)
			ownershipHotel(s, "SELECT v FROM own_provider_saved")
			ownershipDatabaseOwner(s, "own_source")
			ownershipBool(s, "SELECT has_database_privilege('own_reader',current_database(),'CREATE WITH GRANT OPTION')", true)
			return
		}
		retained := ownershipProviderHeld(s, "SELECT v FROM own_provider_saved")
		defer retained.Close()
		if err := stuntgres.Reset(s.p.Ctx, source); err != nil {
			s.t.Fatal(err)
		}
		ownershipProviderGone(s)
		ownershipDatabaseOwner(s, "own_source")
		s.actor("own_reader")
		s.exec("CREATE EXTENSION unaccent")
		ownershipHotel(s, "SELECT unaccent('Hôtel') AS v")
		s.actor("")
		if err := stuntgres.Reset(s.p.Ctx, source); err != nil {
			s.t.Fatal(err)
		}
		if err := stuntgres.Restore(s.p.Ctx, source, bytes.NewReader(dump.Bytes())); err != nil {
			s.t.Fatal(err)
		}
		ownershipProviderOwners(s, "own_target", "own_target")
		ownershipHotel(s, "SELECT v FROM own_provider_saved")
		ownershipDatabaseOwner(s, "own_source")
		ownershipProviderHeldHotel(s, retained)
		var round bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx, source, &round); err != nil || !bytes.Equal(dump.Bytes(), round.Bytes()) {
			s.t.Fatal("named owners roundtrip", err)
		}
		other := fixture.CatalogControlPool(s.t, stuntgres.NewDriver(), "ownership_provider_destination")
		ownershipProviderControlRoles(s, other)
		// The destination's different owner and explicit CREATE ACL survive import;
		// changed target attributes do not invalidate a still-valid symbolic owner.
		var otherName string
		if err := other.QueryRowContext(s.p.Ctx, "SELECT current_database()").Scan(&otherName); err != nil {
			s.t.Fatal(err)
		}
		for _, q := range []string{"ALTER ROLE own_target CREATEDB", "ALTER DATABASE " + pq.QuoteIdentifier(otherName) + " OWNER TO own_reader", "GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(otherName) + " TO own_actor WITH GRANT OPTION"} {
			if _, err := other.ExecContext(s.p.Ctx, q); err != nil {
				s.t.Fatal(err)
			}
		}
		if err := stuntgres.Restore(s.p.Ctx, other, bytes.NewReader(dump.Bytes())); err != nil {
			s.t.Fatal(err)
		}
		conn, err := other.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer conn.Close()
		dest := *s
		dest.c = conn
		dest.bootstrap = "postgres"
		ownershipProviderOwners(&dest, "own_target", "own_target")
		ownershipDatabaseOwner(&dest, "own_reader")
		ownershipHotel(&dest, "SELECT v FROM own_provider_saved")
		var imported bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx, other, &imported); err != nil || !bytes.Equal(dump.Bytes(), imported.Bytes()) {
			s.t.Fatal("independent named-owner redump", err)
		}
		for _, recreated := range []bool{false, true} {
			target := fixture.CatalogControlPool(s.t, stuntgres.NewDriver(), map[bool]string{false: "ownership_provider_missing", true: "ownership_provider_recreated"}[recreated])
			for _, role := range []string{"own_other", "own_source", "own_reader"} {
				if _, err := target.ExecContext(s.p.Ctx, "CREATE USER "+role); err != nil {
					s.t.Fatal(err)
				}
			}
			if recreated {
				if _, err := target.ExecContext(s.p.Ctx, "CREATE USER own_target"); err != nil {
					s.t.Fatal(err)
				}
			}
			if _, err := target.ExecContext(s.p.Ctx, "CREATE EXTENSION unaccent"); err != nil {
				s.t.Fatal(err)
			}
			var old bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, target, &old); err != nil {
				s.t.Fatal(err)
			}
			var reader io.Reader = bytes.NewReader(dump.Bytes())
			if recreated {
				reader = &ownershipProviderMutationReader{Reader: reader, mutate: func() error {
					for _, q := range []string{"DROP ROLE own_target", "CREATE USER own_target"} {
						if _, err := target.ExecContext(s.p.Ctx, q); err != nil {
							return err
						}
					}
					return nil
				}}
			}
			if err := stuntgres.Restore(s.p.Ctx, target, reader); err == nil {
				s.t.Fatal("missing/recreated owner accepted", recreated)
			}
			var unchanged bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, target, &unchanged); err != nil || !bytes.Equal(old.Bytes(), unchanged.Bytes()) {
				s.t.Fatal("failed owner import replaced data/providers", recreated, err)
			}
			var hotel string
			if err := target.QueryRowContext(s.p.Ctx, "SELECT unaccent('Hôtel')").Scan(&hotel); err != nil || hotel != "Hotel" {
				s.t.Fatal(hotel, err)
			}
		}
		if err := stuntgres.Reset(s.p.Ctx, other); err != nil {
			s.t.Fatal(err)
		}
		ownershipDatabaseOwner(&dest, "own_reader")
		dest.actor("own_actor")
		dest.exec("CREATE EXTENSION unaccent")
		ownershipHotel(&dest, "SELECT unaccent('Hôtel') AS v")
		dest.actor("")
		if err := stuntgres.Reset(s.p.Ctx, source); err != nil {
			s.t.Fatal(err)
		}
		s.actor("own_reader")
		s.exec("CREATE EXTENSION unaccent")
		ownershipHotel(s, "SELECT unaccent('Hôtel') AS v")
		s.actor("")
	})
}
func ownershipProviderControlRoles(s *membershipScenario, db *sql.DB) {
	s.t.Helper()
	for _, role := range []string{"own_source", "own_target", "own_other", "own_reader", "own_actor"} {
		if _, err := db.ExecContext(s.p.Ctx, "CREATE USER "+role); err != nil {
			s.t.Fatal(err)
		}
	}
}
