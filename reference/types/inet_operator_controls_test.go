package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

var inetOperatorStoredSetup = []fixture.Step{
	fixture.Exec("CREATE TABLE operator_saved(id INTEGER,h INET DEFAULT (inet '192.168.1.255/24' + 1),n INET GENERATED ALWAYS AS (~ h) STORED,a INET GENERATED ALWAYS AS (h & inet '0.0.0.255/25') STORED,o INET GENERATED ALWAYS AS (h | inet '0.0.0.255/25') STORED,d BIGINT GENERATED ALWAYS AS (h - inet '192.168.2.0') STORED)", 0),
	fixture.Exec("INSERT INTO operator_saved(id) VALUES(1)", 1),
	fixture.Exec("CREATE VIEW operator_view AS SELECT id,1::bigint + h AS next,h - 1::bigint AS prev,d FROM operator_saved", 0),
}

func inetOperatorStoredAssertions(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Select("SELECT id,h,n,a,o,d FROM operator_saved ORDER BY id", []string{"id", "h", "n", "a", "o", "d"}, []string{"INT4", "INET", "INET", "INET", "INET", "INT8"}, [][]any{{int64(1), "192.168.2.0/24", "63.87.253.255/24", "0.0.0.0/25", "192.168.2.255/25", int64(0)}}),
		fixture.Select("SELECT id,next,prev,d FROM operator_view ORDER BY id", []string{"id", "next", "prev", "d"}, []string{"INT4", "INET", "INET", "INT8"}, [][]any{{int64(1), "192.168.2.1/24", "192.168.1.255/24", int64(0)}}),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
	}
	// Execute the stored expressions again after each reconstruction, not only
	// read the previously materialized generated values.
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO operator_saved(id) VALUES(2)", 1))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT h,n,a,o,d FROM operator_saved WHERE id=2", []string{"h", "n", "a", "o", "d"}, []string{"INET", "INET", "INET", "INET", "INT8"}, [][]any{{"192.168.2.0/24", "63.87.253.255/24", "0.0.0.0/25", "192.168.2.255/25", int64(0)}}))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM operator_saved WHERE id=2", 1))
}

func TestReferenceINETOperatorStoredRecipes(t *testing.T) {
	p := fixture.NewScenario(t)
	backends := p.Backends()
	for _, b := range backends {
		for _, s := range inetOperatorStoredSetup {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		inetOperatorStoredAssertions(t, p, b)
	}
	// Native controls have no PostgreSQL API equivalent.
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
	native := fixture.Backend{Name: "restored", DB: p.Memory, Recorder: p.MemoryRecord}
	inetOperatorStoredAssertions(t, p, native)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if redump.String() != dump.String() {
		t.Fatal("operator recipes changed on redump", redump.String(), dump.String())
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(redump.Bytes())); err != nil {
		t.Fatal(err)
	}
	inetOperatorStoredAssertions(t, p, native)
	connector, err := stuntgres.NewDriver().OpenConnector("inet-operator-fresh")
	if err != nil {
		t.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	defer func() {
		if err := fresh.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	recorded, recorder := fixture.InsertSelectRecordedPool(t, connector)
	inetOperatorStoredAssertions(t, p, fixture.Backend{Name: "fresh", DB: recorded, Recorder: recorder})
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW operator_view; DROP TABLE operator_saved"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
		for _, b := range backends {
			if b.PG {
				inetOperatorStoredAssertions(t, p, b)
			}
		}
	}
}
