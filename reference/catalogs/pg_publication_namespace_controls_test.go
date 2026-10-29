package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGPublicationNamespaceNativeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			q := "SELECT * FROM pg_catalog.pg_publication_namespace"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			check := func() {
				publicationNamespaceShape(t, p, b, b.DB)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, publicationNamespaceEmpty(b.PG, q))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, publicationNamespaceEmpty(b.PG, q))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, publicationNamespaceCount())
			}
			check()
			// Native host replacement has no PostgreSQL equivalent.
			if b.PG {
				return
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE publication_namespace_sentinel(id integer,v text)", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO publication_namespace_sentinel VALUES(7,'kept')", 1))
			retained, err := p.Native.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			names, err := retained.Columns()
			if err != nil {
				t.Fatal(err)
			}
			columns, err := retained.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			publicationNamespaceMetadata(t, false, columns)
			savedNames := append([]string(nil), names...)
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if dump.Len() == 0 {
				t.Fatal("empty actual dump")
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			check()
			want := fixture.ControlsReferenceDiagnostic("42P01", `relation "publication_namespace_sentinel" does not exist`)
			want.Position = "15"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT * FROM publication_namespace_sentinel", want)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check()
			sentinel := fixture.Select("SELECT id,v FROM publication_namespace_sentinel", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, sentinel)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(sentinel))
			afterNames, err := retained.Columns()
			if err != nil || !reflect.DeepEqual(savedNames, afterNames) {
				t.Fatal("retained schema changed", afterNames, err)
			}
			after, err := retained.ColumnTypes()
			if err != nil || !reflect.DeepEqual(columns, after) {
				t.Fatal("retained descriptors changed", after, err)
			}
			publicationNamespaceMetadata(t, false, after)
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained catalog not empty", retained.Err())
			}
			if err := retained.Close(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(savedNames, names) {
				t.Fatal("captured names changed after close")
			}
			publicationNamespaceMetadata(t, false, columns)
			// A fresh execution of the original statement consults replacement roots.
			fixture.RunStep(t, p.Ctx, held, b.Recorder, publicationNamespaceEmpty(false, q))
		})
	}
}

func TestReferencePGPublicationNamespaceTransactions(t *testing.T) {
	withPublicationNamespaceReader(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, actor *sql.Conn) {
		run := func(q string) {
			fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec(q, 0))
		}
		q := "SELECT * FROM pg_catalog.pg_publication_namespace"
		held := make([]fixture.HeldStatement, 2)
		for i, e := range []fixture.Executor{owner, actor} {
			stmt, err := e.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held[i] = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
		}
		empty := func() {
			t.Helper()
			for i, e := range []fixture.Executor{owner, actor} {
				fixture.RunStep(t, p.Ctx, e, b.Recorder, publicationNamespaceEmpty(b.PG, q))
				fixture.RunStep(t, p.Ctx, held[i], b.Recorder, publicationNamespaceEmpty(b.PG, q))
				fixture.RunStep(t, p.Ctx, e, b.Recorder, publicationNamespaceCount())
			}
		}
		data := fixture.Select("SELECT id FROM publication_namespace_tx_schema.items", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		run("BEGIN")
		run("CREATE SCHEMA publication_namespace_tx_schema")
		run("CREATE TABLE publication_namespace_tx_schema.items(id integer)")
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("INSERT INTO publication_namespace_tx_schema.items VALUES(7)", 1))
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, data)
		empty()
		publicationNamespaceShape(t, p, b, owner)
		run("SAVEPOINT ordinary_ddl")
		run("CREATE TABLE publication_namespace_tx_schema.extra(id integer)")
		extra := fixture.Select("SELECT id FROM publication_namespace_tx_schema.extra", []string{"id"}, []string{"INT4"}, nil)
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, extra)
		run("ROLLBACK TO SAVEPOINT ordinary_ddl")
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Select("SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON c.relnamespace=n.oid WHERE n.nspname='publication_namespace_tx_schema' AND c.relname='extra'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		empty()
		run("COMMIT")
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, data)
		empty()
		run("BEGIN")
		run("DROP TABLE publication_namespace_tx_schema.items")
		run("DROP SCHEMA publication_namespace_tx_schema")
		empty()
		run("ROLLBACK")
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, data)
		empty()

		// Private ACL roots provide read-your-writes without publishing authority
		// to the independent actor connection before commit.
		run("BEGIN")
		run("REVOKE SELECT ON pg_catalog.pg_publication_namespace FROM PUBLIC")
		publicationNamespaceStoredACL(t, p, b, owner, false, false)
		publicationNamespaceStoredACL(t, p, b, actor, true, false)
		empty()
		run("ROLLBACK")
		publicationNamespaceStoredACL(t, p, b, actor, true, false)
		empty()
		run("BEGIN")
		run("REVOKE SELECT ON pg_catalog.pg_publication_namespace FROM PUBLIC")
		publicationNamespaceStoredACL(t, p, b, owner, false, false)
		publicationNamespaceStoredACL(t, p, b, actor, true, false)
		empty()
		run("COMMIT")
		publicationNamespaceStoredACL(t, p, b, actor, false, false)
		publicationNamespaceDenied(t, p, b, actor, q, false)
		publicationNamespaceDenied(t, p, b, actor, q, true)
		publicationNamespaceDenied(t, p, b, held[1], q, false)
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, publicationNamespaceEmpty(b.PG, q))
		run("GRANT SELECT ON pg_catalog.pg_publication_namespace TO PUBLIC")
		empty()
		run("BEGIN")
		run("SAVEPOINT catalog_acl")
		run("REVOKE SELECT ON pg_catalog.pg_publication_namespace FROM PUBLIC")
		publicationNamespaceStoredACL(t, p, b, owner, false, false)
		run("ROLLBACK TO SAVEPOINT catalog_acl")
		publicationNamespaceStoredACL(t, p, b, owner, true, false)
		run("COMMIT")
		publicationNamespaceStoredACL(t, p, b, actor, true, false)
		empty()
	})
}
