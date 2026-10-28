package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgParameterACLSharedDestination(t *testing.T, p *fixture.Pair, dump []byte, expected [][]any) {
	t.Helper()
	driver := stuntgres.NewDriver()
	const key = "parameter shared destination /? opaque=true"
	aDB := fixture.CatalogControlPool(t, driver, key)
	base, err := driver.OpenConnector(key)
	if err != nil { t.Fatal(err) }
	recorded, recorder := fixture.RecordedNativePool(t, base)
	a := fixture.Backend{Name: "destination", DB: recorded, Recorder: recorder}
	run := func(b fixture.Backend, s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	for _, q := range []string{
		"CREATE ROLE pacs_reader", "CREATE ROLE pacs_grantor", "CREATE ROLE pacs_observer", "CREATE ROLE pacs_destination_keeper",
		"CREATE DATABASE pacs_destination_b", "GRANT CONNECT ON DATABASE pacs_destination_b TO pacs_destination_keeper",
		"CREATE TABLE pacs_advance_one (v int)", "CREATE TABLE pacs_advance_two (v int)",
		"GRANT SET ON PARAMETER pacs_destination.old TO pacs_reader",
	} { run(a, fixture.Exec(q, 0)) }
	peerDB, peerRecorder := fixture.RecordedNativePool(t, base.(*stuntgres.Connector).WithDatabase("pacs_destination_b"))
	b := fixture.Backend{Name: "destination peer", DB: peerDB, Recorder: peerRecorder}
	run(b, fixture.Exec("CREATE TABLE pacs_destination_sentinel (v int)", 0))
	run(b, fixture.Exec("INSERT INTO pacs_destination_sentinel VALUES (83)", 1))
	state := parameterSharedDatabaseState(t, p, b)
	const query = "SELECT parname,paracl::text AS acl FROM pg_parameter_acl WHERE parname IN ('lc_messages','pacs_control.public','work_mem') ORDER BY parname"
	held := fixture.ViewHold(t, p, b, query)
	old, err := b.DB.QueryContext(p.Ctx, "SELECT parname FROM pg_parameter_acl WHERE parname='pacs_destination.old'")
	if err != nil { t.Fatal(err) }
	defer old.Close()
	if err := stuntgres.Restore(p.Ctx, aDB, bytes.NewReader(dump)); err != nil { t.Fatal(err) }
	if got := parameterSharedDatabaseState(t, p, b); got != state { t.Fatal("fresh restore changed destination database authority", state, got) }
	for _, view := range []fixture.Backend{a, b} {
		s := fixture.Select(query, []string{"parname", "acl"}, []string{"TEXT", "TEXT"}, expected)
		run(view, s); run(view, fixture.Prepared(s))
		run(view, fixture.Select("SELECT parname FROM pg_parameter_acl WHERE parname='pacs_destination.old'", []string{"parname"}, []string{"TEXT"}, nil))
		run(view, fixture.Select("SELECT rolname FROM pg_roles WHERE rolname='pacs_destination_keeper'", []string{"rolname"}, []string{"NAME"}, [][]any{{"pacs_destination_keeper"}}))
	}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"parname", "acl"}, []string{"TEXT", "TEXT"}, expected))
	for _, name := range []string{"lc_messages", "pacs_control.public", "work_mem"} {
		identity := pgParameterACLOID(t, p, a, name)
		if got := pgParameterACLOID(t, p, b, name); got != identity { t.Fatal("destination peer identity", identity, got) }
	}
	// Old output is retained even though replacement removed this row.
	var name string
	if !old.Next() { t.Fatal("destination retained row lost", old.Err()) }
	if err := old.Scan(&name); err != nil || name != "pacs_destination.old" { t.Fatal(name, err) }
	if old.Next() || old.Err() != nil { t.Fatal(old.Err()) }; old.Close()
	run(b, fixture.Select("SELECT v FROM pacs_destination_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(83)}}))
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, aDB, &again); err != nil || !bytes.Equal(dump, again.Bytes()) { t.Fatal("fresh destination canonical redump", err) }
}
