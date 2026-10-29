package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExtensionCommentControls(t *testing.T) {
	for _, state := range []struct {
		name, literal string
		want          any
	}{
		{"default", "'" + extensionCommentDefault + "'", extensionCommentDefault},
		{"custom", "E'custom 雪 ''quote''\\nslash\\\\'", "custom 雪 'quote'\nslash\\"},
		{"removed", "NULL", nil},
	} {
		t.Run(state.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"comment_ext_import_owner"})
			extensionCommentCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE USER comment_ext_import_owner SUPERUSER", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION comment_ext_import_owner", 0))
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				run(fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.1'", 0))
				run(fixture.Exec("COMMENT ON EXTENSION fuzzystrmatch IS "+state.literal, 0))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				seedLiteral, seedWant := "'"+extensionCommentSeed+"'", any(extensionCommentSeed)
				if state.name != "default" {
					seedLiteral, seedWant = state.literal, state.want
				}
				run(fixture.Exec("COMMENT ON EXTENSION plpgsql IS "+seedLiteral, 0))
				// plpgsql is database-scoped and survives public-schema fixture cleanup.
				defer c.ExecContext(p.Ctx, "COMMENT ON EXTENSION plpgsql IS '"+extensionCommentSeed+"'")
				oid := extensionCommentOID(t, p, c, "fuzzystrmatch")
				extensionCommentValue(t, p, b, c, oid, state.want)
				extensionCommentValue(t, p, b, c, extensionCommentOID(t, p, c, "plpgsql"), seedWant)
				if b.PG {
					return
				} // Native controls have no PostgreSQL equivalent.
				dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
				if p.Postgres != nil {
					pg, err := p.Postgres.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					func() {
						defer pg.Close()
						defer func() {
							for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS fuzzystrmatch CASCADE", "COMMENT ON EXTENSION plpgsql IS '" + extensionCommentSeed + "'", "DROP OWNED BY comment_ext_import_owner", "DROP ROLE comment_ext_import_owner"} {
								if _, err := pg.ExecContext(p.Ctx, q); err != nil {
									t.Error("oracle replay cleanup", q, err)
								}
							}
						}()
						if _, err := pg.ExecContext(p.Ctx, "CREATE ROLE comment_ext_import_owner SUPERUSER"); err != nil {
							t.Fatal(err)
						}
						if _, err := pg.ExecContext(p.Ctx, dump); err != nil {
							t.Fatal("actual emitted SQL oracle replay", err)
						}
						oracle := fixture.Backend{Name: "postgres", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
						extensionCommentValue(t, p, oracle, pg, extensionCommentOID(t, p, pg, "fuzzystrmatch"), state.want)
						extensionCommentValue(t, p, oracle, pg, extensionCommentOID(t, p, pg, "plpgsql"), seedWant)
						fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, fixture.Select("SELECT soundex('Anne')", []string{"soundex"}, []string{"TEXT"}, [][]any{{"A500"}}))
					}()
				}
				if again := fixture.ExtensionDump(t, p.Ctx, p.Native); again != dump {
					t.Fatal("nondeterministic dump")
				}
				connector, err := stuntgres.NewDriver().OpenConnector("extension-comment-" + state.name)
				if err != nil {
					t.Fatal(err)
				}
				raw := sql.OpenDB(connector)
				defer raw.Close()
				if _, err := raw.ExecContext(p.Ctx, "CREATE USER comment_ext_import_owner NOSUPERUSER"); err != nil {
					t.Fatal(err)
				}
				recorded, recorder := fixture.RecordedNativePool(t, connector)
				defer recorded.Close()
				target, err := recorded.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer target.Close()
				destination := b
				destination.Recorder = recorder
				restore := func(text string) {
					if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(text)); err != nil {
						t.Fatal(err)
					}
				}
				restore(dump)
				targetOID := extensionCommentOID(t, p, target, "fuzzystrmatch")
				check := func(want any) {
					extensionCommentValue(t, p, destination, target, targetOID, want)
					fixture.RunStep(t, p.Ctx, target, recorder, fixture.Select("SELECT soundex('Anne')", []string{"soundex"}, []string{"TEXT"}, [][]any{{"A500"}}))
				}
				check(state.want)
				extensionCommentValue(t, p, destination, target, extensionCommentOID(t, p, target, "plpgsql"), seedWant)
				fixture.RunStep(t, p.Ctx, target, recorder, fixture.Select("SELECT rolname FROM pg_roles WHERE oid=(SELECT extowner FROM pg_extension WHERE extname='fuzzystrmatch')", []string{"rolname"}, []string{"NAME"}, [][]any{{"comment_ext_import_owner"}}))
				if again := fixture.ExtensionDump(t, p.Ctx, raw); again != dump {
					t.Fatal("independent destination redump changed")
				}
				// Dump must use the committed root, not the caller's private comment.
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("COMMENT ON EXTENSION fuzzystrmatch IS 'private leak'", 0))
				privateDump := fixture.ExtensionDump(t, p.Ctx, p.Native)
				restore(privateDump)
				targetOID = extensionCommentOID(t, p, target, "fuzzystrmatch")
				check(state.want)
				run(fixture.Exec("ROLLBACK", 0))
				fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("CREATE TABLE comment_ext_sentinel(v INT)", 0))
				fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("INSERT INTO comment_ext_sentinel VALUES(77)", 1))
				fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("COMMENT ON EXTENSION fuzzystrmatch IS 'destination old'", 0))
				before := fixture.ExtensionDump(t, p.Ctx, raw)
				unchanged := func() {
					if current := extensionCommentOID(t, p, target, "fuzzystrmatch"); current != targetOID {
						t.Fatal("failed restore changed identity")
					}
					check("destination old")
					fixture.RunStep(t, p.Ctx, target, recorder, fixture.Select("SELECT v FROM comment_ext_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
					if after := fixture.ExtensionDump(t, p.Ctx, raw); after != before {
						t.Fatal("failed restore changed catalog/data/ownership/comments")
					}
				}
				if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(dump+"\nCOMMENT ON EXTENSION missing_comment_ext IS 'late failure';\n")); err == nil {
					t.Fatal("late invalid restore succeeded")
				}
				unchanged()
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				if err := stuntgres.Restore(ctx, raw, &fixture.CatalogControlCancelReader{Cancel: cancel}); !errors.Is(err, context.Canceled) {
					t.Fatal("canceled restore", err)
				}
				unchanged()
				// Retained rows remain on their old snapshot across real replacement.
				rows, err := target.QueryContext(p.Ctx, "SELECT obj_description($1::oid,'pg_extension') AS comment", targetOID)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				columns, err := rows.ColumnTypes()
				if err != nil || len(columns) != 1 || columns[0].Name() != "comment" || columns[0].DatabaseTypeName() != "TEXT" {
					t.Fatal("retained comment metadata", columns, err)
				}
				restore(dump)
				columns, err = rows.ColumnTypes()
				if err != nil || len(columns) != 1 || columns[0].Name() != "comment" || columns[0].DatabaseTypeName() != "TEXT" {
					t.Fatal("replacement changed retained metadata", columns, err)
				}
				if !rows.Next() {
					t.Fatal("retained comment lost", rows.Err())
				}
				var retained string
				if err := rows.Scan(&retained); err != nil || retained != "destination old" {
					t.Fatal("retained comment", retained, err)
				}
				rows.Close()
				targetOID = extensionCommentOID(t, p, target, "fuzzystrmatch")
				check(state.want)
				if err := stuntgres.Reset(p.Ctx, raw); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, target, recorder, fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='fuzzystrmatch'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				extensionCommentValue(t, p, destination, target, extensionCommentOID(t, p, target, "plpgsql"), extensionCommentSeed)
			})
		})
	}
}
