package reference_test

import (
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

type ownershipTransactionalFamily struct {
	name, create, prefix, catalog, key, ownerColumn, comment, noun string
	drop                                                           bool
}

var ownershipTransactionalFamilies = []ownershipTransactionalFamily{
	{name: "table", create: "CREATE TABLE tx_own_table(id INT)", prefix: "ALTER TABLE tx_own_table", catalog: "pg_class", key: "relname='tx_own_table'", ownerColumn: "relowner", comment: "TABLE tx_own_table", noun: "table tx_own_table"},
	{name: "enum", create: "CREATE TYPE tx_own_enum AS ENUM('a','b')", prefix: "ALTER TYPE tx_own_enum", catalog: "pg_type", key: "typname='tx_own_enum'", ownerColumn: "typowner", comment: "TYPE tx_own_enum", noun: "type tx_own_enum"},
	{name: "composite", create: "CREATE TYPE tx_own_composite AS(id INT)", prefix: "ALTER TYPE tx_own_composite", catalog: "pg_type", key: "typname='tx_own_composite'", ownerColumn: "typowner", comment: "TYPE tx_own_composite", noun: "type tx_own_composite"},
	{name: "domain", create: "CREATE DOMAIN tx_own_domain AS INT", prefix: "ALTER DOMAIN tx_own_domain", catalog: "pg_type", key: "typname='tx_own_domain'", ownerColumn: "typowner", comment: "DOMAIN tx_own_domain", noun: "type tx_own_domain"},
	{name: "view", create: "CREATE VIEW tx_own_view AS SELECT 7::INT AS id", prefix: "ALTER VIEW tx_own_view", catalog: "pg_class", key: "relname='tx_own_view'", ownerColumn: "relowner", comment: "VIEW tx_own_view", noun: "view tx_own_view"},
	{name: "materialized", create: "CREATE MATERIALIZED VIEW tx_own_mv AS SELECT 7::INT AS id", prefix: "ALTER MATERIALIZED VIEW tx_own_mv", catalog: "pg_class", key: "relname='tx_own_mv'", ownerColumn: "relowner", comment: "MATERIALIZED VIEW tx_own_mv", noun: "materialized view tx_own_mv"},
	{name: "database", catalog: "pg_database", key: "datname=current_database()", ownerColumn: "datdba"},
	{name: "schema", create: "CREATE SCHEMA own_schema", prefix: "ALTER SCHEMA own_schema", catalog: "pg_namespace", key: "nspname='own_schema'", ownerColumn: "nspowner", comment: "SCHEMA own_schema", noun: "schema own_schema"},
	{name: "sequence", create: "CREATE SEQUENCE tx_own_seq", prefix: "ALTER SEQUENCE tx_own_seq", catalog: "pg_class", key: "relname='tx_own_seq'", ownerColumn: "relowner", comment: "SEQUENCE tx_own_seq", noun: "sequence tx_own_seq"},
	{name: "reassign", create: "CREATE TABLE tx_own_bulk(id INT)", prefix: "ALTER TABLE tx_own_bulk", catalog: "pg_class", key: "relname='tx_own_bulk'", ownerColumn: "relowner", comment: "TABLE tx_own_bulk", noun: "table tx_own_bulk"},
	{name: "restrict", create: "CREATE TABLE tx_own_bulk(id INT)", prefix: "ALTER TABLE tx_own_bulk", catalog: "pg_class", key: "relname='tx_own_bulk'", ownerColumn: "relowner", drop: true},
	{name: "cascade", create: "CREATE TABLE tx_own_bulk(id INT)", prefix: "ALTER TABLE tx_own_bulk", catalog: "pg_class", key: "relname='tx_own_bulk'", ownerColumn: "relowner", drop: true},
}

func TestReferenceRoleOwnershipTransactionalFamilies(t *testing.T) {
	for _, family := range ownershipTransactionalFamilies {
		t.Run(family.name, func(t *testing.T) {
			ownershipTransactionCases(t, func(s *membershipScenario, route string) {
				f := family
				if f.name == "database" {
					database := ownershipDatabaseName(s)
					f.prefix = "ALTER DATABASE " + pq.QuoteIdentifier(database)
					defer func() {
						s.c.ExecContext(s.p.Ctx, "ROLLBACK")
						s.c.ExecContext(s.p.Ctx, f.prefix+" OWNER TO "+pq.QuoteIdentifier(s.bootstrap))
					}()
				} else {
					ownershipDefinition(s, f.create)
				}
				if f.name == "table" || f.name == "reassign" || f.drop {
					s.run(fixture.Exec("INSERT INTO "+map[bool]string{true: "tx_own_bulk", false: "tx_own_table"}[f.name != "table"]+" VALUES(7)", 1))
				}
				s.exec(f.prefix + " OWNER TO own_source")
				q := f.prefix + " OWNER TO own_target"
				switch f.name {
				case "reassign":
					q = "REASSIGN OWNED BY own_source,own_source TO own_target"
				case "restrict":
					q = "DROP OWNED BY own_source,own_source RESTRICT"
				case "cascade":
					q = "DROP OWNED BY own_source,own_source CASCADE"
				}
				probe := "SELECT r.rolname FROM " + f.catalog + " c JOIN pg_roles r ON r.oid=c." + f.ownerColumn + " WHERE c." + f.key
				idQuery := "SELECT oid::BIGINT FROM " + f.catalog + " WHERE " + f.key
				var original int64
				if err := s.c.QueryRowContext(s.p.Ctx, idQuery).Scan(&original); err != nil || original <= 0 {
					s.t.Fatal("positive original identity", original, err)
				}
				peer, err := s.backend.DB.Conn(s.p.Ctx)
				if err != nil {
					s.t.Fatal(err)
				}
				defer peer.Close()
				state := func(e fixture.Executor, owner string, gone bool) {
					s.t.Helper()
					var rows [][]any
					if !gone {
						rows = [][]any{{owner}}
					}
					fixture.RunStep(s.t, s.p.Ctx, e, s.record, fixture.Select(probe, []string{"rolname"}, []string{"NAME"}, rows))
					if !gone {
						fixture.RunStep(s.t, s.p.Ctx, e, s.record, fixture.Select(idQuery, []string{"oid"}, []string{"INT8"}, [][]any{{original}}))
					}
				}
				ownerWitness := f.name == "enum" || f.name == "composite" || f.name == "domain" || f.name == "view" || f.name == "materialized"
				if ownerWitness {
					s.exec("GRANT own_source TO own_target WITH INHERIT FALSE,SET TRUE")
					s.exec("GRANT CREATE ON SCHEMA public TO own_source")
				}
				for _, commit := range []bool{false, true} {
					if route == "auto" && !commit {
						continue
					}
					x := ownershipBegin(s, route, q)
					defer x.abort()
					state(x.e, "own_source", false) // Prepare and Bind are effect-free.
					if route != "auto" {
						x.exec("SAVEPOINT before_owner")
					}
					x.exec(q)
					x.run(fixture.Select(q, []string{}, []string{}, nil))
					state(x.e, "own_target", f.drop)
					if route != "auto" {
						state(peer, "own_source", false)
						x.exec("ROLLBACK TO before_owner")
						state(x.e, "own_source", false)
						x.exec(q)
						x.exec("ROLLBACK TO before_owner")
						state(x.e, "own_source", false)
						x.exec("RELEASE before_owner")
						x.exec(q)
					}
					if !f.drop && f.comment != "" {
						x.exec("SET ROLE own_target")
						if ownerWitness {
							x.exec(f.prefix + " OWNER TO own_source")
							x.exec("RESET ROLE")
							x.exec(q)
						} else {
							x.exec("COMMENT ON " + f.comment + " IS 'successor authority'")
						}
						x.exec("RESET ROLE")
						if route != "auto" {
							x.exec("SAVEPOINT denied_owner")
						}
						x.exec("SET ROLE own_source")
						deniedQuery := "COMMENT ON " + f.comment + " IS 'denied'"
						if ownerWitness {
							deniedQuery = f.prefix + " OWNER TO own_other"
						}
						step := fixture.ImplicitError(deniedQuery, fixture.Diagnostic{Code: "42501", Message: "must be owner of " + f.noun})
						step.ErrorStage = "execute"
						x.run(step)
						if route != "auto" {
							x.exec("ROLLBACK TO denied_owner")
						} else {
							x.exec("RESET ROLE")
						}
					}
					x.end(commit)
					if commit {
						state(peer, "own_target", f.drop)
					} else {
						state(peer, "own_source", false)
					}
					if !f.drop && (f.name == "table" || f.name == "reassign" || f.name == "view" || f.name == "materialized") {
						name := map[string]string{"table": "tx_own_table", "reassign": "tx_own_bulk", "view": "tx_own_view", "materialized": "tx_own_mv"}[f.name]
						ownershipRows(s, name)
					}
				}
			})
		})
	}
}

func TestReferenceRoleOwnershipTransactionalPrivateObjects(t *testing.T) {
	ownershipTransactionCases(t, func(s *membershipScenario, route string) {
		if route == "auto" {
			return
		} // Earlier-private objects require an explicit owner.
		peer, err := s.backend.DB.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer peer.Close()
		peerAbsent := func() {
			fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname='own_schema'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Select("SELECT count(*) FROM pg_class WHERE relname IN('tx_own_private','private_seq','private_view','private_mv')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		for _, commit := range []bool{false, true} {
			x := ownershipBegin(s, route, "ALTER TABLE tx_own_private OWNER TO own_source", "ALTER SCHEMA own_schema OWNER TO own_source", "ALTER SEQUENCE own_schema.private_seq OWNER TO own_source", "ALTER VIEW own_schema.private_view OWNER TO own_source", "ALTER MATERIALIZED VIEW own_schema.private_mv OWNER TO own_source", "REASSIGN OWNED BY own_source TO own_target", "DROP OWNED BY own_target")
			defer x.abort()
			x.exec("CREATE TABLE tx_own_private(id INT)")
			x.run(fixture.Exec("INSERT INTO tx_own_private VALUES(7)", 1))
			x.exec("ALTER TABLE tx_own_private OWNER TO own_source")
			x.exec("CREATE SCHEMA own_schema")
			x.exec("CREATE SEQUENCE own_schema.private_seq")
			x.exec("ALTER SCHEMA own_schema OWNER TO own_source")
			x.exec("ALTER SEQUENCE own_schema.private_seq OWNER TO own_source")
			x.exec("CREATE VIEW own_schema.private_view AS SELECT id FROM tx_own_private")
			x.run(fixture.Exec("CREATE MATERIALIZED VIEW own_schema.private_mv AS SELECT id FROM tx_own_private", 1))
			x.exec("ALTER VIEW own_schema.private_view OWNER TO own_source")
			x.exec("ALTER MATERIALIZED VIEW own_schema.private_mv OWNER TO own_source")
			privateIDsQuery := "SELECT c.oid::BIGINT,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='own_schema' AND c.relname IN('private_view','private_mv') ORDER BY c.relname"
			privateIDs := ownershipTransactionSnapshot(s, x.e, privateIDsQuery)
			if len(privateIDs) != 2 || privateIDs[0][0].(int64) <= 0 || privateIDs[1][0].(int64) <= 0 || privateIDs[0][0] == privateIDs[1][0] {
				s.t.Fatal("earlier-private view/MV identities", privateIDs)
			}
			x.exec("REASSIGN OWNED BY own_source TO own_target")
			peerAbsent()
			fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Select(privateIDsQuery, []string{"oid", "relname"}, []string{"INT8", "NAME"}, nil))
			x.exec("SAVEPOINT keep_private")
			x.exec("DROP OWNED BY own_target")
			x.exec("ROLLBACK TO keep_private")
			x.run(fixture.Select("SELECT id FROM tx_own_private", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			x.run(fixture.Select("SELECT r.rolname FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname='own_schema'", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_target"}}))
			x.run(fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='own_schema.private_seq'::regclass", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_target"}}))
			for _, name := range []string{"own_schema.private_view", "own_schema.private_mv"} {
				x.run(fixture.Select("SELECT id FROM "+name, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				x.run(fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='"+name+"'::regclass", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_target"}}))
			}
			x.run(fixture.Select(privateIDsQuery, []string{"oid", "relname"}, []string{"INT8", "NAME"}, privateIDs))
			peerAbsent()
			x.exec("RELEASE keep_private")
			x.end(commit)
			ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='tx_own_private'", map[bool]int64{true: 1, false: 0}[commit])
			ownershipCount(s, "SELECT count(*) FROM pg_namespace WHERE nspname='own_schema'", map[bool]int64{true: 1, false: 0}[commit])
			if commit {
				ownershipOwner(s, "tx_own_private", "own_target")
				ownershipRows(s, "own_schema.private_view")
				ownershipRows(s, "own_schema.private_mv")
				s.run(fixture.Select(privateIDsQuery, []string{"oid", "relname"}, []string{"INT8", "NAME"}, privateIDs))
				fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Select(privateIDsQuery, []string{"oid", "relname"}, []string{"INT8", "NAME"}, privateIDs))
				for _, name := range []string{"own_schema.private_view", "own_schema.private_mv"} {
					fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Select("SELECT id FROM "+name, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='"+name+"'::regclass", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_target"}}))
				}
			} else {
				peerAbsent()
				fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Select(privateIDsQuery, []string{"oid", "relname"}, []string{"INT8", "NAME"}, nil))
			}
		}
	})
}
