package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func sessionIdentityMetadata(pg bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, typ := range types {
			if typ.DatabaseTypeName() != "NAME" && typ.DatabaseTypeName() != "TEXT" {
				continue
			}
			scan := reflect.TypeFor[string]()
			if pg && typ.DatabaseTypeName() == "NAME" {
				scan = reflect.TypeFor[any]()
			}
			if typ.ScanType() != scan {
				t.Fatal(typ.Name(), typ.ScanType(), scan)
			}
		}
	}
}

func TestReferenceSessionIdentityFormsMetadataAndScalarConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			bootstrap, database := "postgres", "reference/"+t.Name()[:len(t.Name())-len("/"+backend.name)]
			database = database[:min(len(database), 63)]
			var system any
			if backend.pg {
				bootstrap, database, system = "stuntgres", "stuntgres_reference", "scram-sha-256:stuntgres"
			}
			run := func(s referenceStep) {
				if s.selectRows {
					s.checkMetadata = sessionIdentityMetadata(backend.pg)
				}
				referenceRunStep(t, p.ctx, conn, backend.recorder, s)
			}
			query := `SELECT current_catalog,current_database(),current_role,current_user,session_user,user,system_user`
			forms := referenceSelect(query, []string{"current_catalog", "current_database", "current_role", "current_user", "session_user", "user", "system_user"}, []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "TEXT"}, [][]any{{database, database, bootstrap, bootstrap, bootstrap, bootstrap, system}})
			forms.checkScan = func(t *testing.T, rows *sql.Rows, _ int) {
				var catalog, db, role, current, session, user string
				var auth any
				if err := rows.Scan(&catalog, &db, &role, &current, &session, &user, &auth); err != nil {
					t.Fatal(err)
				}
				if catalog != database || db != database || role != bootstrap || current != bootstrap || session != bootstrap || user != bootstrap || referenceNormalize(auth) != system {
					t.Fatal(catalog, db, role, current, session, user, auth)
				}
			}
			run(forms)
			var catalog, role, session bool
			if err := conn.QueryRowContext(p.ctx, `SELECT current_catalog=current_database(), current_role=current_user, session_user=user`).Scan(&catalog, &role, &session); err != nil || !catalog || !role || !session {
				t.Fatal(catalog, role, session, err)
			}
			for _, q := range []string{`SELECT pg_catalog.current_user()`, `SELECT "current_user"()`, `SELECT pg_catalog.current_database()`, `SELECT "current_database"()`} {
				var value string
				want := bootstrap
				if q == `SELECT pg_catalog.current_database()` || q == `SELECT "current_database"()` {
					want = database
				}
				if err := conn.QueryRowContext(p.ctx, q).Scan(&value); err != nil || value != want {
					t.Fatal(q, value, err)
				}
			}
			run(referenceExec(`CREATE TABLE identity_t(id INT,who TEXT)`, 0))
			run(referenceExec(`INSERT INTO identity_t VALUES(1,$1),(2,'other')`, 2, bootstrap))
			var id int
			var who string
			if err := conn.QueryRowContext(p.ctx, `SELECT id,current_user FROM identity_t WHERE who=current_user ORDER BY id`).Scan(&id, &who); err != nil || id != 1 || who != bootstrap {
				t.Fatal(id, who, err)
			}
			run(referenceSelect(`SELECT current_user,system_user FROM identity_t WHERE FALSE`, []string{"current_user", "system_user"}, []string{"NAME", "TEXT"}, [][]any{}))
			run(referenceExec(`CREATE TABLE identity_quoted("current_user" TEXT,current_database TEXT)`, 0))
			run(referenceExec(`INSERT INTO identity_quoted VALUES('stored user','stored database')`, 1))
			var quoted, keyword, column, db string
			if err := conn.QueryRowContext(p.ctx, `SELECT "current_user",CURRENT_USER,current_database,current_database() FROM identity_quoted`).Scan(&quoted, &keyword, &column, &db); err != nil || quoted != "stored user" || keyword != bootstrap || column != "stored database" || db != database {
				t.Fatal(quoted, keyword, column, db, err)
			}
			run(referencePrepared(referenceExec(`SELECT current_catalog=current_database(),current_role=current_user,session_user=user`, 1)))
			trusted := any("trusted")
			if backend.pg {
				trusted = system
			}
			if err := conn.QueryRowContext(p.ctx, `SELECT CAST(COALESCE(system_user,'trusted') AS TEXT) FROM identity_t WHERE id=1`).Scan(&who); err != nil || who != trusted {
				t.Fatal(who, err)
			}
			run(referenceExec(`UPDATE identity_t SET who=current_user WHERE id=2`, 1))
			if err := conn.QueryRowContext(p.ctx, `DELETE FROM identity_t WHERE who=current_user RETURNING id,current_user`).Scan(&id, &who); err != nil || (id != 1 && id != 2) || who != bootstrap {
				t.Fatal(id, who, err)
			}
			run(referenceSelect(`SELECT id FROM identity_t`, []string{"id"}, []string{"INT4"}, [][]any{}))
		})
	}
}
