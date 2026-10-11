package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

var inetDomainSetup = []fixture.Step{
	fixture.Exec("CREATE DOMAIN host_domain AS INET", 0),
	fixture.Exec("CREATE DOMAIN host_chain AS host_domain", 0),
	fixture.Exec("CREATE DOMAIN network_domain AS CIDR", 0),
	fixture.Exec("CREATE DOMAIN network_chain AS network_domain", 0),
	fixture.Exec("CREATE TABLE domain_saved(id INTEGER,h host_chain,c network_chain)", 0),
	fixture.Exec("INSERT INTO domain_saved VALUES(1,'192.168.0.129/25','10'),(2,'::1','::1'),(3,NULL,NULL)", 3),
	fixture.Exec("CREATE VIEW domain_view AS SELECT id,h::inet AS h,c::cidr AS c FROM domain_saved", 0),
}

func inetDomainAssertions(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	expected := [][]any{{int64(1), "192.168.0.129/25", "10.0.0.0/8"}, {int64(2), "::1", "::1/128"}, {int64(3), nil, nil}}
	for _, q := range []string{"SELECT id,h::inet AS h,c::cidr AS c FROM domain_saved ORDER BY id", "SELECT id,h,c FROM domain_view ORDER BY id"} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(q, []string{"id", "h", "c"}, []string{"INT4", "INET", "CIDR"}, expected))
	}
	if !b.PG {
		// PostgreSQL implements arrays; the native scalar-only exclusions must survive
		// domain chains, catalog replacement and a fresh Driver destination.
		for _, name := range []string{"host_domain", "host_chain", "network_domain", "network_chain"} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ImplicitError("CREATE TABLE excluded(v "+name+"[])", fixture.Diagnostic{Code: "0A000", Message: "unsupported column type", Position: "25"}))
		}
	}
}

func TestReferenceINETDomainLifecycleBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	backends := p.Backends()
	native := fixture.Backend{Name: "memory", DB: p.Memory, Recorder: p.MemoryRecord}
	for _, s := range inetDomainSetup {
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s)
	}
	inetDomainAssertions(t, p, native)
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
	inetDomainAssertions(t, p, native)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if redump.String() != dump.String() {
		t.Fatal("network domain redump changed", redump.String(), dump.String())
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(redump.Bytes())); err != nil {
		t.Fatal(err)
	}
	inetDomainAssertions(t, p, native)
	connector, err := stuntgres.NewDriver().OpenConnector("inet-domain-fresh-destination")
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
	inetDomainAssertions(t, p, fixture.Backend{Name: "fresh", DB: recorded, Recorder: recorder})
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range backends {
		inetDomainAssertions(t, p, b)
	}
}
