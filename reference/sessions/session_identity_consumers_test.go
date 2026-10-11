package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSessionIdentityFormsMetadataAndScalarConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			bootstrap, database := "postgres", "reference/"+t.Name()[:len(t.Name())-len("/"+backend.Name)]
			database = database[:min(len(database), 63)]
			var system any
			if backend.PG {
				bootstrap, database, system = "stuntgres", "stuntgres_reference", "scram-sha-256:stuntgres"
			}
			run := func(s fixture.Step) {
				if s.SelectRows {
					s.CheckMetadata = fixture.SessionIdentityMetadata(backend.PG)
				}
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			}
			query := `SELECT current_catalog,current_database(),current_role,current_user,session_user,user,system_user`
			forms := fixture.Select(query, []string{"current_catalog", "current_database", "current_role", "current_user", "session_user", "user", "system_user"}, []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "TEXT"}, [][]any{{database, database, bootstrap, bootstrap, bootstrap, bootstrap, system}})
			forms.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
				var catalog, db, role, current, session, user string
				var auth any
				if err := rows.Scan(&catalog, &db, &role, &current, &session, &user, &auth); err != nil {
					t.Fatal(err)
				}
				if catalog != database || db != database || role != bootstrap || current != bootstrap || session != bootstrap || user != bootstrap || fixture.Normalize(auth) != system {
					t.Fatal(catalog, db, role, current, session, user, auth)
				}
			}
			run(forms)
			var catalog, role, session bool
			if err := conn.QueryRowContext(p.Ctx, `SELECT current_catalog=current_database(), current_role=current_user, session_user=user`).Scan(&catalog, &role, &session); err != nil || !catalog || !role || !session {
				t.Fatal(catalog, role, session, err)
			}
			for _, q := range []string{`SELECT pg_catalog.current_user()`, `SELECT "current_user"()`, `SELECT pg_catalog.current_database()`, `SELECT "current_database"()`} {
				var value string
				want := bootstrap
				if q == `SELECT pg_catalog.current_database()` || q == `SELECT "current_database"()` {
					want = database
				}
				if err := conn.QueryRowContext(p.Ctx, q).Scan(&value); err != nil || value != want {
					t.Fatal(q, value, err)
				}
			}
			run(fixture.Exec(`CREATE TABLE identity_t(id INT,who TEXT)`, 0))
			run(fixture.Exec(`INSERT INTO identity_t VALUES(1,$1),(2,'other')`, 2, bootstrap))
			var id int
			var who string
			if err := conn.QueryRowContext(p.Ctx, `SELECT id,current_user FROM identity_t WHERE who=current_user ORDER BY id`).Scan(&id, &who); err != nil || id != 1 || who != bootstrap {
				t.Fatal(id, who, err)
			}
			run(fixture.Select(`SELECT current_user,system_user FROM identity_t WHERE FALSE`, []string{"current_user", "system_user"}, []string{"NAME", "TEXT"}, [][]any{}))
			run(fixture.Exec(`CREATE TABLE identity_quoted("current_user" TEXT,current_database TEXT)`, 0))
			run(fixture.Exec(`INSERT INTO identity_quoted VALUES('stored user','stored database')`, 1))
			var quoted, keyword, column, db string
			if err := conn.QueryRowContext(p.Ctx, `SELECT "current_user",CURRENT_USER,current_database,current_database() FROM identity_quoted`).Scan(&quoted, &keyword, &column, &db); err != nil || quoted != "stored user" || keyword != bootstrap || column != "stored database" || db != database {
				t.Fatal(quoted, keyword, column, db, err)
			}
			run(fixture.Prepared(fixture.Exec(`SELECT current_catalog=current_database(),current_role=current_user,session_user=user`, 1)))
			trusted := any("trusted")
			if backend.PG {
				trusted = system
			}
			if err := conn.QueryRowContext(p.Ctx, `SELECT CAST(COALESCE(system_user,'trusted') AS TEXT) FROM identity_t WHERE id=1`).Scan(&who); err != nil || who != trusted {
				t.Fatal(who, err)
			}
			run(fixture.Exec(`UPDATE identity_t SET who=current_user WHERE id=2`, 1))
			if err := conn.QueryRowContext(p.Ctx, `DELETE FROM identity_t WHERE who=current_user RETURNING id,current_user`).Scan(&id, &who); err != nil || (id != 1 && id != 2) || who != bootstrap {
				t.Fatal(id, who, err)
			}
			run(fixture.Select(`SELECT id FROM identity_t`, []string{"id"}, []string{"INT4"}, [][]any{}))
		})
	}
}
