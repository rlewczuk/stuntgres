package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePublicationTransactions(t *testing.T) {
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := publicationConn(t, p, b)
		defer c.Close()
		peer := publicationConn(t, p, b)
		defer peer.Close()
		run := func(s ...fixture.Step) { publicationRun(t, p, b, c, s...) }
		run(fixture.Exec("CREATE TABLE publication_rt_t(id integer PRIMARY KEY,v integer)", 0), fixture.Exec("CREATE SCHEMA publication_rt_s", 0))
		q := "SELECT pubname,pubinsert,pubupdate FROM pg_catalog.pg_publication WHERE pubname='publication_rt_p'"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		state := func(e fixture.Executor, rows ...[]any) {
			publicationRun(t, p, b, e, fixture.Select(q, []string{"pubname", "pubinsert", "pubupdate"}, []string{"NAME", "BOOL", "BOOL"}, rows))
		}
		state(held)
		run(fixture.Exec("BEGIN", 0), fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t", 0))
		state(held, []any{"publication_rt_p", true, true})
		state(peer)
		run(fixture.Exec("SAVEPOINT before_change", 0), fixture.Exec("ALTER PUBLICATION publication_rt_p SET(publish='insert')", 0), fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLES IN SCHEMA publication_rt_s", 0), publicationNamespace("publication_rt_p", "publication_rt_s"))
		state(held, []any{"publication_rt_p", true, false})
		run(fixture.Exec("ROLLBACK TO before_change", 0), publicationNamespace("publication_rt_p"), publicationMembers("publication_rt_p", []any{"public", "publication_rt_t"}), fixture.Exec("COMMIT", 0))
		state(peer, []any{"publication_rt_p", true, true})
		run(fixture.Exec("BEGIN", 0), fixture.Exec("DROP PUBLICATION publication_rt_p", 0))
		state(held)
		state(peer, []any{"publication_rt_p", true, true})
		run(fixture.Exec("ROLLBACK", 0))
		state(held, []any{"publication_rt_p", true, true})
		// Go transaction routes share the same publication roots and rollback.
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		publicationRun(t, p, b, tx, fixture.Prepared(fixture.Exec("ALTER PUBLICATION publication_rt_p SET(publish='delete')", 0)), publicationOptions("publication_rt_p", false, false, false, true, false, false, "n"))
		state(peer, []any{"publication_rt_p", true, true})
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		state(held, []any{"publication_rt_p", true, true})
		run(fixture.Exec("BEGIN", 0), fixture.Exec("ALTER PUBLICATION publication_rt_p SET(publish='insert')", 0), publicationDiagnostic("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_t", "42710", `relation "publication_rt_t" is already member of publication "publication_rt_p"`, "", ""), fixture.Exec("ROLLBACK", 0))
		state(held, []any{"publication_rt_p", true, true})
		run(fixture.Exec("DROP PUBLICATION publication_rt_p", 0), fixture.Prepared(fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t WITH(publish='insert')", 0)))
		state(held, []any{"publication_rt_p", true, false})
		run(fixture.Exec("BEGIN READ ONLY", 0), publicationDiagnostic("CREATE PUBLICATION publication_rt_readonly", "25006", "cannot execute CREATE PUBLICATION in a read-only transaction", "", ""), fixture.Exec("ROLLBACK", 0))
		// Native pre-cancellation is an ordinary context error. lib/pq's
		// pre-canceled utility result is intentionally unconstrained by policy;
		// both backends instead prove actual admitted cancellation below.
		if !b.PG {
			canceledSQL := "ALTER PUBLICATION publication_rt_p SET(publish='delete')"
			change, err := c.PrepareContext(p.Ctx, canceledSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer change.Close()
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if _, err := c.ExecContext(ctx, canceledSQL); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled direct publication command", err)
			}
			if _, err := change.ExecContext(ctx); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled prepared publication command", err)
			}
		}
		publicationAdmittedCancellation(t, p, b)
		state(held, []any{"publication_rt_p", true, false})
	})
}

func publicationControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	publicationRun(t, p, b, b.DB,
		fixture.Exec("CREATE SCHEMA publication_rt_s", 0),
		fixture.Exec("CREATE TABLE publication_rt_t(id integer PRIMARY KEY,v integer)", 0),
		fixture.Exec("ALTER TABLE publication_rt_t REPLICA IDENTITY USING INDEX publication_rt_t_pkey", 0),
		fixture.Exec("CREATE TABLE publication_rt_s.t(id integer PRIMARY KEY)", 0),
		fixture.Exec("INSERT INTO publication_rt_t VALUES(1,10),(2,0),(3,NULL)", 3),
		fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t(v,id) WHERE(v>0) WITH(publish='insert,delete',publish_generated_columns=stored)", 0),
		fixture.Exec("CREATE PUBLICATION publication_rt_schema FOR TABLES IN SCHEMA publication_rt_s WITH(publish='insert',publish_via_partition_root=true)", 0),
		fixture.Exec("CREATE PUBLICATION publication_rt_all FOR ALL TABLES WITH(publish='truncate')", 0),
		fixture.Exec("ALTER PUBLICATION publication_rt_p OWNER TO postgres", 0),
		fixture.Exec("ALTER PUBLICATION publication_rt_schema OWNER TO postgres", 0),
		fixture.Exec("ALTER PUBLICATION publication_rt_all OWNER TO postgres", 0))
}

func publicationControlState(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Select("SELECT p.pubname,p.oid>0 AS valid_oid,r.rolname FROM pg_catalog.pg_publication p JOIN pg_catalog.pg_roles r ON r.oid=p.pubowner WHERE p.pubname LIKE 'publication_rt_%' ORDER BY p.pubname", []string{"pubname", "valid_oid", "rolname"}, []string{"NAME", "BOOL", "NAME"}, [][]any{{"publication_rt_all", true, "postgres"}, {"publication_rt_p", true, "postgres"}, {"publication_rt_schema", true, "postgres"}}),
		fixture.Select("SELECT relreplident FROM pg_catalog.pg_class WHERE oid='publication_rt_t'::regclass", []string{"relreplident"}, []string{"CHAR"}, [][]any{{"i"}}),
		fixture.Select("SELECT count(*) FROM pg_catalog.pg_index WHERE indrelid='publication_rt_t'::regclass AND indisreplident", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}),
		publicationOptions("publication_rt_p", false, true, false, true, false, false, "s"),
		publicationOptions("publication_rt_schema", false, true, false, false, false, true, "n"),
		publicationOptions("publication_rt_all", true, false, false, false, true, false, "n"),
		publicationNamespace("publication_rt_schema", "publication_rt_s"),
		publicationDefinitions("publication_rt_p", []any{"publication_rt_t", "{id,v}", "(v > 0)"}),
		fixture.Select("SELECT id,v FROM publication_rt_t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(0)}, {int64(3), nil}}),
	} {
		fixture.RunStep(t, p.Ctx, e, recorder, s)
		fixture.RunStep(t, p.Ctx, e, recorder, fixture.Prepared(s))
	}
}

func TestReferencePublicationNativeControls(t *testing.T) {
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		publicationControlSetup(t, p, b)
		publicationControlState(t, p, b, b.DB, b.Recorder)
		if b.PG {
			return
		}
		q := "SELECT pubname,pubinsert FROM pg_catalog.pg_publication WHERE pubname='publication_rt_p'"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		retained, err := p.Native.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		metadata, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		checkMetadata := func() {
			if len(metadata) != 2 || metadata[0].Name() != "pubname" || metadata[0].DatabaseTypeName() != "NAME" || metadata[1].Name() != "pubinsert" || metadata[1].DatabaseTypeName() != "BOOL" {
				t.Fatal("changed captured publication metadata", metadata)
			}
		}
		checkMetadata()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if dump.Len() == 0 {
			t.Fatal("empty actual publication dump")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		publicationRun(t, p, b, held, fixture.Select(q, []string{"pubname", "pubinsert"}, []string{"NAME", "BOOL"}, nil))
		if !retained.Next() {
			t.Fatal("lost captured publication", retained.Err())
		}
		var name string
		var insert bool
		if err := retained.Scan(&name, &insert); err != nil || name != "publication_rt_p" || !insert {
			t.Fatal("changed retained publication", name, insert, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("extra retained rows", retained.Err())
		}
		retained.Close()
		publicationRun(t, p, b, b.DB, publicationNamespace("publication_rt_schema"), publicationExplicit("publication_rt_p"))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		publicationControlState(t, p, b, b.DB, b.Recorder)
		publicationRun(t, p, b, held, fixture.Select(q, []string{"pubname", "pubinsert"}, []string{"NAME", "BOOL"}, [][]any{{"publication_rt_p", true}}))
		checkMetadata()
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("publication replay is not canonical")
		}
		connector, err := stuntgres.NewDriver().OpenConnector("reference/" + t.Name() + "/publication-replay")
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		db, recorder := fixture.RecordedNativePool(t, connector)
		defer db.Close()
		if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		publicationControlState(t, p, b, db, recorder)
		// Replay the actual emitted bytes through PostgreSQL's public SQL API.
		if p.Postgres != nil {
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				for _, q := range []string{"DROP PUBLICATION IF EXISTS publication_rt_p,publication_rt_schema,publication_rt_all", "DROP SCHEMA IF EXISTS publication_rt_s CASCADE", "DROP TABLE IF EXISTS publication_rt_t"} {
					if _, err := p.Postgres.ExecContext(ctx, q); err != nil {
						t.Error("replay cleanup", err)
					}
				}
			}()
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("PostgreSQL actual publication SQL replay", err)
			}
			publicationControlState(t, p, fixture.Backend{PG: true}, p.Postgres, p.PostgresRecord)
		}
		publicationRun(t, p, b, b.DB, fixture.Exec("CREATE TABLE publication_rt_keep(id integer)", 0), fixture.Exec("INSERT INTO publication_rt_keep VALUES(99)", 1))
		preserved := func() {
			publicationControlState(t, p, b, b.DB, b.Recorder)
			publicationRun(t, p, b, b.DB, fixture.Select("SELECT id FROM publication_rt_keep", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
		}
		invalid := append(bytes.Clone(dump.Bytes()), []byte("\nALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_missing;\n")...)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(invalid)); err == nil {
			t.Fatal("accepted invalid late publication replay")
		}
		preserved()
		unknownOwner := append(bytes.Clone(dump.Bytes()), []byte("\nALTER PUBLICATION publication_rt_p OWNER TO publication_rt_missing_owner;\n")...)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(unknownOwner)); err == nil {
			t.Fatal("accepted unknown publication owner in replay")
		}
		preserved()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled replacement", err)
		}
		preserved()
		publicationRun(t, p, b, b.DB, fixture.Exec("CREATE TABLE publication_rt_s.future(id integer)", 0), publicationMembers("publication_rt_schema", []any{"publication_rt_s", "future"}, []any{"publication_rt_s", "t"}), publicationIdentityError("DELETE FROM publication_rt_t WHERE false", "delete", "publication_rt_t", "filter"))
	})
}

func TestReferencePublicationSmoke(t *testing.T) {
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		publicationControlSetup(t, p, b)
		publicationControlState(t, p, b, b.DB, b.Recorder)
		publicationRun(t, p, b, b.DB, publicationIdentityError("DELETE FROM publication_rt_t WHERE false", "delete", "publication_rt_t", "filter"))
		c := publicationConn(t, p, b)
		defer c.Close()
		publicationRun(t, p, b, c, fixture.Exec("BEGIN", 0), fixture.Exec("ALTER PUBLICATION publication_rt_p SET(publish='insert')", 0), fixture.Exec("ROLLBACK", 0))
		publicationControlState(t, p, b, b.DB, b.Recorder)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			publicationControlState(t, p, b, b.DB, b.Recorder)
		}
	})
}
