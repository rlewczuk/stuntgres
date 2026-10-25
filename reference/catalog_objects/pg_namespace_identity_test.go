package reference_test

import (
	"database/sql"
	"net/url"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGNamespaceShape(t *testing.T) {
	pgNamespaceScenario(t, nil, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{"SELECT * FROM pg_catalog.pg_namespace WHERE FALSE", "SELECT * FROM pg_namespace WHERE nspname=$1"} {
			s := fixture.Select(q, fixture.PGNamespaceColumns, pgNamespaceTypes, nil)
			if q != "SELECT * FROM pg_catalog.pg_namespace WHERE FALSE" {
				s.Args = []any{"ns18_missing_shape"}
			}
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgNamespaceMetadata(t, rows, b.PG) }
			for _, plain := range []bool{false, true} {
				s.NonContext = plain
				pgNamespaceCheck(t, p, b, b.DB, s)
				pgNamespaceCheck(t, p, b, b.DB, fixture.Prepared(s))
			}
		}
		pgNamespaceRow(t, p, b, b.DB, "pg_toast", 99, 10, nil)
		pgNamespaceCheck(t, p, b, b.DB, fixture.Select("SELECT c.oid,c.relnamespace,c.relhasindex,n.nspname,c.reltype='pg_catalog.pg_namespace'::regtype::oid AS rowtype FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=2615", []string{"oid", "relnamespace", "relhasindex", "nspname", "rowtype"}, []string{"OID", "OID", "BOOL", "NAME", "BOOL"}, [][]any{{int64(2615), int64(11), true, "pg_catalog", true}}))
		pgNamespaceCheck(t, p, b, b.DB, fixture.Select("SELECT c.relnamespace,c.relhasindex,t.typrelid FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype WHERE c.oid=2615", []string{"relnamespace", "relhasindex", "typrelid"}, []string{"OID", "BOOL", "OID"}, [][]any{{int64(11), true, int64(2615)}}))
	})
}

func TestReferencePGNamespaceBuiltins(t *testing.T) {
	pgNamespaceScenario(t, nil, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		db := b.DB
		bootstrap := "postgres"
		if b.PG {
			u, err := url.Parse(fixture.DSN)
			if err != nil {
				t.Fatal(err)
			}
			u.Path = "/postgres"
			db, err = sql.Open("stuntgres_reference_postgres", u.String())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			bootstrap = "stuntgres"
		}
		acl := "{" + bootstrap + "=UC/" + bootstrap + ",=U/" + bootstrap + "}"
		pgNamespaceRow(t, p, b, db, "pg_catalog", 11, 10, acl)
		pgNamespaceRow(t, p, b, db, "pg_toast", 99, 10, nil)
		pgNamespaceRow(t, p, b, db, "public", 2200, 6171, "{pg_database_owner=UC/pg_database_owner,=U/pg_database_owner}")
		id := pgNamespaceID(t, p, db, "information_schema")
		if id == 11 || id == 99 || id == 2200 {
			t.Fatal("information_schema aliases fixed namespace", id)
		}
		pgNamespaceRow(t, p, b, db, "information_schema", id, 10, acl)
		pgNamespaceCheck(t, p, b, db, fixture.Select("SELECT nspname FROM pg_namespace ORDER BY nspname", []string{"nspname"}, []string{"NAME"}, [][]any{{"information_schema"}, {"pg_catalog"}, {"pg_toast"}, {"public"}}))
	})
}

func TestReferencePGNamespaceIdentity(t *testing.T) {
	pgNamespaceScenario(t, []string{"ns18_identity", "Ns18.Mixed", "名18", "ns18_identity_new"}, []string{"ns18_identity_owner"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgNamespaceExec(t, p, b, b.DB, "CREATE ROLE ns18_identity_owner", "CREATE SCHEMA ns18_identity", `CREATE SCHEMA "Ns18.Mixed"`, `CREATE SCHEMA "名18"`, "CREATE TABLE ns18_identity.ns18_same(i INT)", `CREATE TABLE "Ns18.Mixed".ns18_same(i INT)`)
		ownerName, owner := pgNamespaceCurrentRole(t, p, b, b.DB)
		ids := map[int64]bool{}
		for _, name := range []string{"ns18_identity", "Ns18.Mixed", "名18"} {
			id := pgNamespaceID(t, p, b.DB, name)
			if ids[id] {
				t.Fatal("schema identity alias", id)
			}
			ids[id] = true
			pgNamespaceRow(t, p, b, b.DB, name, id, owner, nil)
		}
		id := pgNamespaceID(t, p, b.DB, "ns18_identity")
		pgNamespaceCheck(t, p, b, b.DB, fixture.Select("SELECT c.relnamespace,c.relowner,r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.relname='ns18_same' AND c.relnamespace=$1", []string{"relnamespace", "relowner", "rolname"}, []string{"OID", "OID", "NAME"}, [][]any{{id, owner, ownerName}}, id))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO ns18_identity.ns18_same VALUES(18)", 1))
		pgNamespaceExec(t, p, b, b.DB, "ALTER SCHEMA ns18_identity RENAME TO ns18_identity_new", "ALTER SCHEMA ns18_identity_new OWNER TO ns18_identity_owner")
		nextOwner := pgNamespaceRoleID(t, p, b.DB, "ns18_identity_owner")
		pgNamespaceRow(t, p, b, b.DB, "ns18_identity_new", id, nextOwner, nil)
		pgNamespaceCheck(t, p, b, b.DB, fixture.Select("SELECT relowner FROM pg_class WHERE relname='ns18_same' AND relnamespace=$1", []string{"relowner"}, []string{"OID"}, [][]any{{owner}}, id))
		pgNamespaceCheck(t, p, b, b.DB, fixture.Select("SELECT nspname FROM pg_namespace WHERE nspname='ns18_identity'", []string{"nspname"}, []string{"NAME"}, nil))
	})
}

func TestReferencePGNamespaceLifecycle(t *testing.T) {
	pgNamespaceScenario(t, []string{"ns18_life", "ns18_life_new", "ns18_public_new"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgNamespaceExec(t, p, b, b.DB, "CREATE SCHEMA ns18_life")
		id := pgNamespaceID(t, p, b.DB, "ns18_life")
		q := "SELECT nspname FROM pg_namespace WHERE oid=$1"
		held := fixture.ViewHold(t, p, b, q)
		pgNamespaceCheck(t, p, b, held, fixture.Select(q, []string{"nspname"}, []string{"NAME"}, [][]any{{"ns18_life"}}, id))
		var retained *sql.Rows
		if !b.PG {
			var err error
			retained, err = b.DB.QueryContext(p.Ctx, q, id)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		pgNamespaceExec(t, p, b, b.DB, "ALTER SCHEMA ns18_life RENAME TO ns18_life_new")
		pgNamespaceCheck(t, p, b, held, fixture.Select(q, []string{"nspname"}, []string{"NAME"}, [][]any{{"ns18_life_new"}}, id))
		pgNamespaceExec(t, p, b, b.DB, "DROP SCHEMA ns18_life_new", "CREATE SCHEMA ns18_life_new")
		if pgNamespaceID(t, p, b.DB, "ns18_life_new") == id {
			t.Fatal("retired identity reused")
		}
		pgNamespaceCheck(t, p, b, held, fixture.Select(q, []string{"nspname"}, []string{"NAME"}, nil, id))
		if retained != nil {
			if !retained.Next() {
				t.Fatal("retained namespace missing", retained.Err())
			}
			var name string
			if err := retained.Scan(&name); err != nil || name != "ns18_life" {
				t.Fatal("retained pre-rename namespace", name, err)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained namespace extra/error", retained.Err())
			}
			retained.Close()
		}
		public := pgNamespaceID(t, p, b.DB, "public")
		pgNamespaceExec(t, p, b, b.DB, "ALTER SCHEMA public RENAME TO ns18_public_new")
		if pgNamespaceID(t, p, b.DB, "ns18_public_new") != public {
			t.Fatal("public rename changed identity")
		}
		pgNamespaceExec(t, p, b, b.DB, "DROP SCHEMA ns18_public_new", "CREATE SCHEMA public")
		if pgNamespaceID(t, p, b.DB, "public") == public {
			t.Fatal("public recreate reused identity")
		}
	})
}
