package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

const extensionCommentDefault = "determine similarities and distance between strings"
const extensionCommentSeed = "PL/pgSQL procedural language"

func extensionCommentCase(t *testing.T, f func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			f(t, p, b, c, peer)
		})
	}
}

func extensionCommentOID(t *testing.T, p *fixture.Pair, x fixture.Executor, name string) int64 {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, "SELECT oid FROM pg_extension WHERE extname=$1", name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing extension", name, rows.Err())
	}
	var oid int64
	if err := rows.Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || rows.Next() {
		t.Fatal("invalid extension identity", oid)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return oid
}

func extensionCommentValue(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, oid int64, want any) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }
	run(fixture.Select("SELECT obj_description($1::oid,$2::name) AS comment", []string{"comment"}, []string{"TEXT"}, [][]any{{want}}, oid, "pg_extension"))
	// Legacy lookup is only unambiguous when no other commented class shares OID.
	rows, err := x.QueryContext(p.Ctx, "SELECT count(*) FROM pg_description WHERE objoid=$1 AND classoid<>3079 AND objsubid=0", oid)
	if err != nil {
		t.Fatal(err)
	}
	var collisions int64
	if !rows.Next() {
		rows.Close()
		t.Fatal("missing collision count")
	}
	if err := rows.Scan(&collisions); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		rows.Close()
		t.Fatal("invalid collision count")
	}
	rows.Close()
	if collisions == 0 {
		run(fixture.Select("SELECT obj_description($1::oid) AS comment", []string{"comment"}, []string{"TEXT"}, [][]any{{want}}, oid))
	} else {
		extensionCommentLegacyCandidates(t, p, x, oid)
	}
	expected := [][]any{}
	if want != nil {
		expected = [][]any{{oid, int64(3079), int64(0), want}}
	}
	run(fixture.Select(fmt.Sprintf("SELECT objoid::bigint AS objoid,classoid::bigint AS classoid,objsubid,description FROM pg_description WHERE objoid=%d AND classoid=3079 ORDER BY objsubid", oid), []string{"objoid", "classoid", "objsubid", "description"}, []string{"INT8", "INT8", "INT4", "TEXT"}, expected))
	run(fixture.Select("SELECT shobj_description($1::oid,'pg_extension') AS comment", []string{"comment"}, []string{"TEXT"}, [][]any{{nil}}, oid))
	run(fixture.Select(fmt.Sprintf("SELECT count(*) FROM pg_shdescription WHERE objoid=%d AND classoid=3079", oid), []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

// OIDs can collide across catalogs: require an actual candidate and stable
// repeated reads, not an invented cross-class winner.
func extensionCommentLegacyCandidates(t *testing.T, p *fixture.Pair, x fixture.Executor, oid int64) {
	t.Helper()
	candidates := map[string]bool{}
	rows, err := x.QueryContext(p.Ctx, "SELECT description FROM pg_description WHERE objoid=$1 AND objsubid=0", oid)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		candidates[text] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	var prior sql.NullString
	for i := range 2 {
		rows, err := x.QueryContext(p.Ctx, "SELECT obj_description($1::oid) AS comment", oid)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 || columns[0].Name() != "comment" || columns[0].DatabaseTypeName() != "TEXT" {
			rows.Close()
			t.Fatal("legacy metadata", columns, err)
		}
		if !rows.Next() {
			rows.Close()
			t.Fatal("missing legacy row")
		}
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if rows.Next() || rows.Err() != nil {
			rows.Close()
			t.Fatal("legacy multiplicity")
		}
		rows.Close()
		if value.Valid && !candidates[value.String] || !value.Valid && len(candidates) != 0 || i > 0 && value != prior {
			t.Fatal("legacy value not a stable catalog candidate", value, candidates)
		}
		prior = value
	}
}
