package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionCommentDefaults(t *testing.T) {
	extensionCommentCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		// plpgsql persists outside public-schema cleanup: restore its baseline below.
		extensionCommentValue(t, p, b, c, extensionCommentOID(t, p, c, "plpgsql"), extensionCommentSeed)
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.1'", 0))
		oid := extensionCommentOID(t, p, c, "fuzzystrmatch")
		extensionCommentValue(t, p, b, c, oid, extensionCommentDefault)
		run(fixture.Select("SELECT soundex('Anne')", []string{"soundex"}, []string{"TEXT"}, [][]any{{"A500"}}))
		for _, catalog := range []string{"PG_EXTENSION", "pg_catalog.pg_extension", ""} {
			run(fixture.Select("SELECT obj_description($1::oid,$2::name) AS comment", []string{"comment"}, []string{"TEXT"}, [][]any{{nil}}, oid, catalog))
		}
		var classCollision int64
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_class WHERE oid=$1", oid).Scan(&classCollision); err != nil {
			t.Fatal(err)
		}
		if classCollision == 0 {
			run(fixture.Select("SELECT obj_description($1::oid,'pg_class') AS comment", []string{"comment"}, []string{"TEXT"}, [][]any{{nil}}, oid))
		}
		run(fixture.Prepared(fixture.Select("SELECT obj_description(NULL::oid,'pg_extension') AS comment", []string{"comment"}, []string{"TEXT"}, [][]any{{nil}})))
	})
}

func TestReferenceExtensionCommentValues(t *testing.T) {
	extensionCommentCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
		oid := extensionCommentOID(t, p, c, "fuzzystrmatch")
		inquirySQL := "SELECT obj_description($1::oid,$2::name) AS comment"
		inquiry, err := c.PrepareContext(p.Ctx, inquirySQL)
		if err != nil {
			t.Fatal(err)
		}
		defer inquiry.Close()
		legacySQL := "SELECT obj_description($1::oid) AS comment"
		legacy, err := c.PrepareContext(p.Ctx, legacySQL)
		if err != nil {
			t.Fatal(err)
		}
		defer legacy.Close()
		var collisions int64
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_description WHERE objoid=$1 AND classoid<>3079 AND objsubid=0", oid).Scan(&collisions); err != nil {
			t.Fatal(err)
		}
		for _, v := range []struct {
			literal string
			want    any
		}{
			{"'plain ''quote'' 雪'", "plain 'quote' 雪"},
			{"E'line\\nslash\\\\雪'", "line\nslash\\雪"},
			{"$note$dollar 'quote' 雪$note$", "dollar 'quote' 雪"},
			{"U&'snow \\96EA'", "snow 雪"},
			{"NULL", nil}, {"''", nil}, {"' '", " "},
		} {
			q := "COMMENT ON EXTENSION \"fuzzystrmatch\" IS " + v.literal
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			// Prepare is quiet, and a held command executes again after replacement.
			extensionCommentValue(t, p, b, peer, oid, extensionCommentDefault)
			for _, held := range []bool{false, true, true} {
				var x fixture.Executor = c
				if held {
					x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 0))
				fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q, []string{}, []string{}, [][]any{}))
				extensionCommentValue(t, p, b, peer, oid, v.want)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: inquiry, SQL: inquirySQL}, b.Recorder, fixture.Select(inquirySQL, []string{"comment"}, []string{"TEXT"}, [][]any{{v.want}}, oid, "pg_extension"))
				if collisions == 0 {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: legacy, SQL: legacySQL}, b.Recorder, fixture.Select(legacySQL, []string{"comment"}, []string{"TEXT"}, [][]any{{v.want}}, oid))
				} else {
					extensionCommentLegacyCandidates(t, p, fixture.HeldStatement{Executor: c, Statement: legacy, SQL: legacySQL}, oid)
				}
				run(fixture.Exec("COMMENT ON EXTENSION fuzzystrmatch IS 'intervening'", 0))
			}
			stmt.Close()
			run(fixture.Exec("COMMENT ON EXTENSION fuzzystrmatch IS '"+extensionCommentDefault+"'", 0))
		}
	})
}
