package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Snapshots are compared only within one backend. Allocation identities are
// opaque; each initial inventory also has independent cardinality/shape checks.
func ownershipTransactionSnapshot(s *fixture.RoleMembershipScenario, e fixture.Executor, query string) [][]any {
	s.T.Helper()
	rows, err := e.QueryContext(s.P.Ctx, query)
	if err != nil {
		s.T.Fatal(query, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		s.T.Fatal(err)
	}
	var out [][]any
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			s.T.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		out = append(out, values)
	}
	if err := rows.Err(); err != nil {
		s.T.Fatal(err)
	}
	return out
}
func ownershipTransactionSnapshotEqual(s *fixture.RoleMembershipScenario, e fixture.Executor, query string, want [][]any) {
	s.T.Helper()
	if got := ownershipTransactionSnapshot(s, e, query); !reflect.DeepEqual(got, want) {
		s.T.Fatalf("retained inventory %s\ngot %#v\nwant %#v", query, got, want)
	}
}

func TestReferenceRoleOwnershipTransactionalAssociations(t *testing.T) {
	ownershipTransactionCases(t, func(s *fixture.RoleMembershipScenario, route string) {
		s.Exec("CREATE SCHEMA own_schema AUTHORIZATION own_source")
		s.Actor("own_source")
		for _, q := range []string{
			"CREATE TABLE own_schema.parent(id SERIAL,identity_id INT GENERATED ALWAYS AS IDENTITY,payload INT DEFAULT 9)",
			"CREATE INDEX parent_idx ON own_schema.parent(payload)",
			"CREATE SEQUENCE own_schema.nearby",
			"CREATE TYPE own_schema.e AS ENUM('a','b')",
			"CREATE DOMAIN own_schema.d AS INT",
			"CREATE TYPE own_schema.c AS(id INT)",
			"COMMENT ON TABLE own_schema.parent IS 'sentinel'",
			"CREATE POLICY parent_policy ON own_schema.parent TO own_reader USING(payload>0)",
			"GRANT SELECT ON own_schema.parent TO own_reader WITH GRANT OPTION",
			"GRANT UPDATE(payload) ON own_schema.parent TO own_reader WITH GRANT OPTION",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA own_schema GRANT SELECT ON TABLES TO own_reader",
		} {
			s.Exec(q)
		}
		s.Run(fixture.Exec("INSERT INTO own_schema.parent(payload) VALUES(7)", 1))
		s.Actor("")
		const relations = "SELECT c.oid::BIGINT,c.relname,c.relowner::BIGINT,c.reltype::BIGINT,c.relacl::TEXT FROM pg_class c WHERE c.relnamespace='own_schema'::regnamespace ORDER BY c.relname"
		const types = "SELECT t.oid::BIGINT,t.typname,t.typowner::BIGINT,t.typarray::BIGINT,t.typelem::BIGINT,t.typrelid::BIGINT,t.typacl::TEXT FROM pg_type t WHERE t.typnamespace='own_schema'::regnamespace ORDER BY t.typname"
		const columns = "SELECT attnum,attname,attacl::TEXT FROM pg_attribute WHERE attrelid='own_schema.parent'::regclass AND attnum>0 ORDER BY attnum"
		const defaults = "SELECT oid::BIGINT,defaclrole::BIGINT,defaclnamespace::BIGINT,defaclobjtype,defaclacl::TEXT FROM pg_default_acl WHERE defaclnamespace='own_schema'::regnamespace ORDER BY oid"
		before := map[string][][]any{}
		for _, q := range []string{relations, types, columns, defaults} {
			before[q] = ownershipTransactionSnapshot(s, s.C, q)
		}
		if len(before[relations]) != 6 || len(before[columns]) != 3 || len(before[defaults]) != 1 {
			s.T.Fatal("complete companion inventory", before)
		}
		for _, row := range before[relations] {
			if row[0].(int64) <= 0 {
				s.T.Fatal("nonpositive relation identity", row)
			}
		}
		peer, err := s.Backend.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer peer.Close()
		q := "REASSIGN OWNED BY own_source TO own_target"
		for _, commit := range []bool{false, true} {
			if route == "auto" && !commit {
				continue
			}
			x := ownershipBegin(s, route, q, "ALTER TABLE own_schema.parent OWNER TO own_other")
			defer x.abort()
			x.exec(q)
			x.run(fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.relnamespace='own_schema'::regnamespace AND r.rolname='own_target'", []string{"count"}, []string{"INT8"}, [][]any{{int64(6)}}))
			x.run(fixture.Select("SELECT count(*) FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.typnamespace='own_schema'::regnamespace AND t.typname IN('parent','e','d','c') AND a.typelem=t.oid AND a.typowner=t.typowner", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
			x.run(fixture.Select("SELECT t.typowner=c.relowner FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='own_schema.c'::regclass", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}}))
			ownershipTransactionSnapshotEqual(s, x.e, defaults, before[defaults]) // REASSIGN never reapplies/removes defaults.
			if route != "auto" {
				for q, rows := range before {
					ownershipTransactionSnapshotEqual(s, peer, q, rows)
				}
				x.exec("SAVEPOINT companions")
				x.exec("ALTER TABLE own_schema.parent OWNER TO own_other")
				x.exec("ROLLBACK TO companions")
				x.exec("ROLLBACK TO companions")
				x.exec("RELEASE companions")
			}
			x.run(fixture.Select("SELECT id,identity_id,payload FROM own_schema.parent", []string{"id", "identity_id", "payload"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(7)}}))
			x.run(fixture.Select("SELECT currval('own_schema.parent_id_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			x.end(commit)
			if !commit {
				for q, rows := range before {
					ownershipTransactionSnapshotEqual(s, s.C, q, rows)
				}
			}
		}
		// Preserved creator defaults apply to genuinely future creation.
		s.Exec("GRANT USAGE,CREATE ON SCHEMA own_schema TO own_source")
		s.Actor("own_source")
		s.Exec("CREATE TABLE own_schema.future(id INT)")
		s.Run(fixture.Exec("INSERT INTO own_schema.future VALUES(7)", 1))
		s.Actor("")
		s.Exec("GRANT USAGE ON SCHEMA own_schema TO own_reader")
		s.Actor("own_reader")
		ownershipRows(s, "own_schema.future")
		s.Actor("")
	})
	t.Run("providers", ownershipTransactionalProviders)
}

func ownershipTransactionalProviders(t *testing.T) {
	for _, route := range ownershipTransactionRoutes {
		t.Run(route, func(t *testing.T) {
			ownershipProviderCasesFor(t, ownershipRouteModes(route), func(s *fixture.RoleMembershipScenario, database string) {
				ownershipProviderSetup(s, database, false)
				ownershipProviderMemberOwners(s, "own_source")
				initial := ownershipProviderIDs(s)
				s.Exec("CREATE TABLE tx_provider_survivor(id INT,v TEXT DEFAULT own_provider.unaccent('Hôtel'))")
				s.Run(fixture.Exec("INSERT INTO tx_provider_survivor(id) VALUES(7)", 1))
				s.Exec("ALTER TABLE tx_provider_survivor OWNER TO own_other")
				peer, err := s.Backend.DB.Conn(s.P.Ctx)
				if err != nil {
					s.T.Fatal(err)
				}
				defer peer.Close()
				owners := func(e fixture.Executor, owner string) {
					fixture.RunStep(s.T, s.P.Ctx, e, s.Record, fixture.Select("SELECT r.rolname FROM pg_extension x JOIN pg_roles r ON r.oid=x.extowner WHERE x.extname='unaccent'", []string{"rolname"}, []string{"NAME"}, [][]any{{owner}}))
					fixture.RunStep(s.T, s.P.Ctx, e, s.Record, fixture.Select("SELECT count(*) FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.pronamespace='own_provider'::regnamespace AND p.proname IN('unaccent','unaccent_init','unaccent_lexize') AND r.rolname='"+owner+"'", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
				}
				for _, commit := range []bool{false, true} {
					if route == "auto" && !commit {
						continue
					}
					x := ownershipBegin(s, route, "REASSIGN OWNED BY own_source TO own_target")
					defer x.abort()
					x.exec("REASSIGN OWNED BY own_source TO own_target")
					owners(x.e, "own_target")
					x.run(fixture.Select("SELECT own_provider.unaccent('Hôtel') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
					if route != "auto" {
						owners(peer, "own_source")
						x.exec("SAVEPOINT provider")
						x.exec("REASSIGN OWNED BY own_source TO own_target")
						x.exec("ROLLBACK TO provider")
						x.exec("RELEASE provider")
					}
					x.end(commit)
					if commit {
						owners(peer, "own_target")
					} else {
						owners(peer, "own_source")
					}
					ownershipProviderStable(s, initial)
				}
				// Independently owned extension members cannot be deleted alone.
				ownershipProviderMemberOwners(s, "own_source", ownershipProviderMembers[0])
				members := ownershipProviderIDs(s)
				memberOnly := "DROP OWNED BY own_source CASCADE"
				union := "DROP OWNED BY own_source,own_target CASCADE"
				for _, commit := range []bool{false, true} {
					if route == "auto" && !commit {
						continue
					}
					x := ownershipBegin(s, route, memberOnly, union)
					defer x.abort()
					if route != "auto" {
						x.exec("SAVEPOINT extension_member")
					}
					refusal := fixture.ImplicitError(memberOnly, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop text search dictionary own_provider.unaccent because extension unaccent requires it", Hint: "You can drop extension unaccent instead."})
					refusal.ErrorStage = "execute"
					x.run(refusal)
					if route != "auto" {
						x.exec("ROLLBACK TO extension_member")
					}
					s.ClearNotices()
					x.exec(union)
					ownershipNotices(s, ownershipCascadeNotice("drop cascades to default value for column v of table tx_provider_survivor", ""))
					x.run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='unaccent'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					x.run(fixture.Select("SELECT id,v FROM tx_provider_survivor", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "Hotel"}}))
					x.end(commit)
					if !commit {
						ownershipProviderStable(s, members)
					}
				}
				ownershipProviderGone(s)
				ownershipCount(s, "SELECT count(*) FROM pg_namespace WHERE nspname='own_provider'", 1)
				s.Run(fixture.Exec("INSERT INTO tx_provider_survivor(id) VALUES(9)", 1))
				ownershipSelect(s, "SELECT id,v FROM tx_provider_survivor ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "Hotel"}, {int64(9), nil}})
			})
		})
	}
}
