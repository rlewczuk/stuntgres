package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExtensionCommentSeedReplay(t *testing.T) {
	for _, delta := range []string{"owner", "member"} {
		for _, state := range []struct {
			name, literal string
			want          any
		}{{"default", "'" + extensionCommentSeed + "'", extensionCommentSeed}, {"custom", "'seed 雪 ''quote'''", "seed 雪 'quote'"}, {"removed", "NULL", nil}} {
			t.Run(delta+"/"+state.name, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"comment_seed_owner"})
				extensionCommentCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE ROLE comment_seed_owner SUPERUSER", 0))
					defer func() {
						for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS plpgsql CASCADE", "DROP TABLE IF EXISTS comment_seed_member", "CREATE EXTENSION plpgsql"} {
							if _, err := c.ExecContext(p.Ctx, q); err != nil {
								t.Error("seed cleanup", q, err)
							}
						}
					}()
					if delta == "owner" {
						run(fixture.Exec("DROP EXTENSION plpgsql", 0))
						run(fixture.Exec("SET SESSION AUTHORIZATION comment_seed_owner", 0))
						run(fixture.Exec("CREATE EXTENSION plpgsql", 0))
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					} else {
						run(fixture.Exec("CREATE TABLE comment_seed_member(v INT)", 0))
						run(fixture.Exec("INSERT INTO comment_seed_member VALUES(23)", 1))
						run(fixture.Exec("ALTER EXTENSION plpgsql ADD TABLE comment_seed_member", 0))
					}
					run(fixture.Exec("COMMENT ON EXTENSION plpgsql IS "+state.literal, 0))
					extensionCommentValue(t, p, b, c, extensionCommentOID(t, p, c, "plpgsql"), state.want)
					run(fixture.Exec("CREATE PROCEDURE public.comment_seed_witness(INOUT n integer) LANGUAGE plpgsql AS 'BEGIN n:=n+1; END'", 0))
					witness := fixture.Select("CALL public.comment_seed_witness(4)", []string{"n"}, []string{"INT4"}, [][]any{{int64(5)}})
					run(witness)
					if b.PG {
						return
					}
					text := fixture.ExtensionDump(t, p.Ctx, p.Native)
					connector, err := stuntgres.NewDriver().OpenConnector("comment-seed-" + delta + "-" + state.name)
					if err != nil {
						t.Fatal(err)
					}
					raw := sql.OpenDB(connector)
					defer raw.Close()
					if _, err := raw.ExecContext(p.Ctx, "CREATE ROLE comment_seed_owner NOSUPERUSER"); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(text)); err != nil {
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
					extensionCommentValue(t, p, destination, target, extensionCommentOID(t, p, target, "plpgsql"), state.want)
					wantOwner := "postgres"
					if delta == "owner" {
						wantOwner = "comment_seed_owner"
					}
					fixture.RunStep(t, p.Ctx, target, recorder, fixture.Select("SELECT rolname FROM pg_roles WHERE oid=(SELECT extowner FROM pg_extension WHERE extname='plpgsql')", []string{"rolname"}, []string{"NAME"}, [][]any{{wantOwner}}))
					fixture.RunStep(t, p.Ctx, target, recorder, witness)
					if again := fixture.ExtensionDump(t, p.Ctx, raw); again != text {
						t.Fatal("independent seeded destination redump changed")
					}
					if delta == "member" {
						fixture.RunStep(t, p.Ctx, target, recorder, fixture.Select("SELECT v FROM comment_seed_member", []string{"v"}, []string{"INT4"}, [][]any{{int64(23)}}))
						fixture.RunStep(t, p.Ctx, target, recorder, fixture.Exec("ALTER EXTENSION plpgsql DROP TABLE comment_seed_member", 0))
					}
				})
			})
		}
	}
}
