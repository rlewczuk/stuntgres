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
	ownershipProviderCases(t, func(s *fixture.RoleMembershipScenario, database string) {
		ownershipProviderSetup(s, database, true)
		s.Exec("GRANT own_other TO own_target WITH INHERIT TRUE, SET TRUE")
		ownershipProviderOwners(s, "own_source", "own_source")
		before := ownershipProviderIDs(s)
		s.Exec("REASSIGN OWNED BY own_source TO own_target")
		ownershipProviderOwners(s, "own_target", "own_target")
		ownershipProviderStable(s, before)
		s.Exec("ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO own_source")
		s.Exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_reader WITH GRANT OPTION")
		ownershipDatabaseOwner(s, "own_source")
		s.Actor("own_other")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_other", func() { s.Exec("CREATE TABLE own_provider_saved(v TEXT DEFAULT own_provider.unaccent('Hôtel'))") })
		s.Run(fixture.Exec("INSERT INTO own_provider_saved DEFAULT VALUES", 1))
		s.Actor("")
		ownershipHotel(s, "SELECT v FROM own_provider_saved")
		source := s.P.Native
		if s.Backend.PG {
			// This independently expected native input supplies its real public codec;
			// it does not replace any preceding genuine PostgreSQL owner operations.
			source = fixture.CatalogControlPool(s.T, stuntgres.NewDriver(), "ownership_provider_oracle_seed")
			ownershipProviderControlRoles(s, source)
			var name string
			if err := source.QueryRowContext(s.P.Ctx, "SELECT current_database()").Scan(&name); err != nil {
				s.T.Fatal(err)
			}
			for _, q := range []string{"GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(name) + " TO own_target", "GRANT own_other TO own_target WITH INHERIT TRUE, SET TRUE"} {
				if _, err := source.ExecContext(s.P.Ctx, q); err != nil {
					s.T.Fatal(err)
				}
			}
			seed := `CREATE SCHEMA own_provider AUTHORIZATION own_other; SET SESSION AUTHORIZATION "own_target"; CREATE EXTENSION unaccent SCHEMA own_provider; SET SESSION AUTHORIZATION postgres; SET SESSION AUTHORIZATION "own_other"; CREATE TABLE own_provider_saved(v TEXT DEFAULT own_provider.unaccent('Hôtel')); INSERT INTO own_provider_saved DEFAULT VALUES; SET SESSION AUTHORIZATION postgres;`
			if err := stuntgres.Restore(s.P.Ctx, source, strings.NewReader(seed)); err != nil {
				s.T.Fatal(err)
			}
			var first bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, source, &first); err != nil {
				s.T.Fatal(err)
			}
			if err := stuntgres.Restore(s.P.Ctx, source, strings.NewReader(ownershipProviderOwnerScript(s.T, first.String(), "own_target"))); err != nil {
				s.T.Fatal(err)
			}
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.P.Ctx, source, &dump); err != nil {
			s.T.Fatal(err)
		}
		if s.Backend.PG {
			s.Exec("DROP OWNED BY own_target,own_other CASCADE")
			s.Exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_target")
			fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_other", func() {
				if _, err := s.C.ExecContext(s.P.Ctx, dump.String()); err != nil {
					s.T.Fatal("genuine PG replay", err)
				}
			})
			s.Actor("")
			// PostgreSQL ignores native metadata comments, but the ordinary owner
			// statements in the dump still restore the captured target owners.
			ownershipProviderOwners(s, "own_target", "own_target")
			ownershipHotel(s, "SELECT v FROM own_provider_saved")
			ownershipDatabaseOwner(s, "own_source")
			ownershipBool(s, "SELECT has_database_privilege('own_reader',current_database(),'CREATE WITH GRANT OPTION')", true)
			return
		}
		retained := ownershipProviderHeld(s, "SELECT v FROM own_provider_saved")
		defer retained.Close()
		if err := stuntgres.Reset(s.P.Ctx, source); err != nil {
			s.T.Fatal(err)
		}
		ownershipProviderGone(s)
		ownershipDatabaseOwner(s, "own_source")
		s.Actor("own_reader")
		s.Exec("CREATE EXTENSION unaccent")
		ownershipHotel(s, "SELECT unaccent('Hôtel') AS v")
		s.Actor("")
		if err := stuntgres.Reset(s.P.Ctx, source); err != nil {
			s.T.Fatal(err)
		}
		if err := stuntgres.Restore(s.P.Ctx, source, bytes.NewReader(dump.Bytes())); err != nil {
			s.T.Fatal(err)
		}
		ownershipProviderOwners(s, "own_target", "own_target")
		ownershipHotel(s, "SELECT v FROM own_provider_saved")
		ownershipDatabaseOwner(s, "own_source")
		ownershipProviderHeldHotel(s, retained)
		var round bytes.Buffer
		if err := stuntgres.Dump(s.P.Ctx, source, &round); err != nil || !bytes.Equal(dump.Bytes(), round.Bytes()) {
			s.T.Fatal("named owners roundtrip", err)
		}
		otherDriver := stuntgres.NewDriver()
		other := fixture.CatalogControlPool(s.T, otherDriver, "ownership_provider_destination")
		ownershipProviderControlRoles(s, other)
		// The destination's different owner and explicit CREATE ACL survive import;
		// changed target attributes do not invalidate a still-valid symbolic owner.
		var otherName string
		if err := other.QueryRowContext(s.P.Ctx, "SELECT current_database()").Scan(&otherName); err != nil {
			s.T.Fatal(err)
		}
		for _, q := range []string{"ALTER ROLE own_target CREATEDB", "ALTER DATABASE " + pq.QuoteIdentifier(otherName) + " OWNER TO own_reader", "GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(otherName) + " TO own_actor WITH GRANT OPTION"} {
			if _, err := other.ExecContext(s.P.Ctx, q); err != nil {
				s.T.Fatal(err)
			}
		}
		if err := stuntgres.Restore(s.P.Ctx, other, bytes.NewReader(dump.Bytes())); err != nil {
			s.T.Fatal(err)
		}
		// SQL acceptance uses a recorded companion attached to the exact same
		// driver/DSN as the unwrapped native-control pool above.
		connector, err := otherDriver.OpenConnector("ownership_provider_destination")
		if err != nil {
			s.T.Fatal(err)
		}
		recorded := sql.OpenDB(fixture.MigrationRecordedConnector(s.T, connector))
		defer recorded.Close()
		conn, err := recorded.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer conn.Close()
		dest := *s
		dest.C = conn
		dest.Bootstrap = "postgres"
		ownershipProviderOwners(&dest, "own_target", "own_target")
		ownershipDatabaseOwner(&dest, "own_reader")
		ownershipHotel(&dest, "SELECT v FROM own_provider_saved")
		var imported bytes.Buffer
		if err := stuntgres.Dump(s.P.Ctx, other, &imported); err != nil || !bytes.Equal(dump.Bytes(), imported.Bytes()) {
			s.T.Fatal("independent named-owner redump", err)
		}
		for _, recreated := range []bool{false, true} {
			target := fixture.CatalogControlPool(s.T, stuntgres.NewDriver(), map[bool]string{false: "ownership_provider_missing", true: "ownership_provider_recreated"}[recreated])
			for _, role := range []string{"own_other", "own_source", "own_reader"} {
				if _, err := target.ExecContext(s.P.Ctx, "CREATE USER "+role); err != nil {
					s.T.Fatal(err)
				}
			}
			if recreated {
				if _, err := target.ExecContext(s.P.Ctx, "CREATE USER own_target"); err != nil {
					s.T.Fatal(err)
				}
			}
			if _, err := target.ExecContext(s.P.Ctx, "CREATE EXTENSION unaccent"); err != nil {
				s.T.Fatal(err)
			}
			var old bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, target, &old); err != nil {
				s.T.Fatal(err)
			}
			var reader io.Reader = bytes.NewReader(dump.Bytes())
			if recreated {
				reader = &ownershipProviderMutationReader{Reader: reader, mutate: func() error {
					for _, q := range []string{"DROP ROLE own_target", "CREATE USER own_target"} {
						if _, err := target.ExecContext(s.P.Ctx, q); err != nil {
							return err
						}
					}
					return nil
				}}
			}
			if err := stuntgres.Restore(s.P.Ctx, target, reader); err == nil {
				s.T.Fatal("missing/recreated owner accepted", recreated)
			}
			var unchanged bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, target, &unchanged); err != nil || !bytes.Equal(old.Bytes(), unchanged.Bytes()) {
				s.T.Fatal("failed owner import replaced data/providers", recreated, err)
			}
			var hotel string
			if err := target.QueryRowContext(s.P.Ctx, "SELECT unaccent('Hôtel')").Scan(&hotel); err != nil || hotel != "Hotel" {
				s.T.Fatal(hotel, err)
			}
		}
		if err := stuntgres.Reset(s.P.Ctx, other); err != nil {
			s.T.Fatal(err)
		}
		ownershipDatabaseOwner(&dest, "own_reader")
		dest.Actor("own_actor")
		dest.Exec("CREATE EXTENSION unaccent")
		ownershipHotel(&dest, "SELECT unaccent('Hôtel') AS v")
		dest.Actor("")
		if err := stuntgres.Reset(s.P.Ctx, source); err != nil {
			s.T.Fatal(err)
		}
		s.Actor("own_reader")
		s.Exec("CREATE EXTENSION unaccent")
		ownershipHotel(s, "SELECT unaccent('Hôtel') AS v")
		s.Actor("")
	})
}
func ownershipProviderControlRoles(s *fixture.RoleMembershipScenario, db *sql.DB) {
	s.T.Helper()
	for _, role := range []string{"own_source", "own_target", "own_other", "own_reader", "own_actor"} {
		if _, err := db.ExecContext(s.P.Ctx, "CREATE USER "+role); err != nil {
			s.T.Fatal(err)
		}
	}
}
