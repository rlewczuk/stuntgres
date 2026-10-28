package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGParameterACLSharedControls(t *testing.T) {
	pgParameterACLSharedScenario(t, "controls", func(t *testing.T, p *fixture.Pair, a, b fixture.Backend, create func(string) fixture.Backend) {
		run := func(view fixture.Backend, s fixture.Step) { fixture.RunStep(t, p.Ctx, view.DB, view.Recorder, s) }
		owner := pgParameterACLOwner(a)
		for _, q := range []string{
			"GRANT SET ON PARAMETER lc_messages TO pacs_reader",
			"GRANT ALTER SYSTEM ON PARAMETER lc_messages TO pacs_grantor WITH GRANT OPTION",
			"GRANT SET ON PARAMETER pacs_control.public TO PUBLIC",
			"GRANT SET ON PARAMETER work_mem TO pacs_reader",
			"REVOKE ALL ON PARAMETER work_mem FROM "+owner,
			"REVOKE ALL ON PARAMETER work_mem FROM pacs_reader",
		} { run(a, fixture.Exec(q, 0)) }
		grantor, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		for _, q := range []string{"SET SESSION AUTHORIZATION pacs_grantor", "GRANT ALTER SYSTEM ON PARAMETER lc_messages TO pacs_reader", "RESET SESSION AUTHORIZATION"} { fixture.RunStep(t, p.Ctx, grantor, b.Recorder, fixture.Exec(q, 0)) }
		grantor.Close()
		acl := "{"+owner+"=sA/"+owner+",pacs_reader=s/"+owner+",pacs_grantor=A*/"+owner+",pacs_reader=A/pacs_grantor}"
		expected := [][]any{{"lc_messages", acl}, {"pacs_control.public", "{"+owner+"=sA/"+owner+",=s/"+owner+"}"}, {"work_mem", "{}"}}
		const query = "SELECT parname,paracl::text AS acl FROM pg_parameter_acl WHERE parname IN ('lc_messages','pacs_control.public','work_mem') ORDER BY parname"
		check := func(views ...fixture.Backend) {
			for _, view := range views {
				s := fixture.Select(query, []string{"parname", "acl"}, []string{"TEXT", "TEXT"}, expected)
				run(view, s); run(view, fixture.Prepared(s))
				h := fixture.ViewHold(t, p, view, query)
				fixture.RunStep(t, p.Ctx, h, view.Recorder, s)
				for _, fact := range []struct{ role, parameter, privilege string; value bool }{
					{"pacs_reader", "lc_messages", "SET", true}, {"pacs_reader", "lc_messages", "ALTER SYSTEM", true},
					{"pacs_reader", "lc_messages", "ALTER SYSTEM WITH GRANT OPTION", false}, {"pacs_grantor", "lc_messages", "ALTER SYSTEM WITH GRANT OPTION", true},
					{"pacs_reader", "pacs_control.public", "SET", true}, {"pacs_reader", "work_mem", "SET,ALTER SYSTEM", false},
				} { run(view, fixture.Select("SELECT has_parameter_privilege('"+fact.role+"','"+fact.parameter+"','"+fact.privilege+"') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{fact.value}})) }
			}
		}
		check(a, b)
		if a.PG {
			// Shared PUBLIC and explicit-empty ACLs outlive role cleanup.
			for _, q := range []string{"REVOKE ALL ON PARAMETER pacs_control.public FROM PUBLIC", "GRANT ALL ON PARAMETER work_mem TO "+owner} { run(a, fixture.Exec(q, 0)) }
			return
		}
		run(b, fixture.Exec("CREATE TABLE pacs_control_sentinel (v int)", 0))
		run(b, fixture.Exec("INSERT INTO pacs_control_sentinel VALUES (71)", 1))
		dbState := parameterSharedDatabaseState(t, p, b)
		oldOID := pgParameterACLOID(t, p, b, "lc_messages")
		old, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_parameter_acl WHERE parname='lc_messages'")
		if err != nil { t.Fatal(err) }
		defer old.Close()
		saved, err := old.ColumnTypes()
		if err != nil { t.Fatal(err) }
		// A genuinely held peer statement must recheck across replacement.
		held := fixture.ViewHold(t, p, b, query)
		private, err := a.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer private.Close()
		defer private.ExecContext(p.Ctx, "ROLLBACK")
		for _, q := range []string{"BEGIN", "GRANT SET ON PARAMETER application_name TO pacs_reader", "REVOKE SET ON PARAMETER lc_messages FROM pacs_reader"} { fixture.RunStep(t, p.Ctx, private, a.Recorder, fixture.Exec(q, 0)) }
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
		fixture.RunStep(t, p.Ctx, private, a.Recorder, fixture.Exec("ROLLBACK", 0))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		for _, view := range []fixture.Backend{a, b} { run(view, fixture.Select(query, []string{"parname", "acl"}, []string{"TEXT", "TEXT"}, nil)) }
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"parname", "acl"}, []string{"TEXT", "TEXT"}, nil))
		var retainedOID int64
		var retainedName, retainedACL string
		if !old.Next() { t.Fatal("Reset discarded retained Rows", old.Err()) }
		if err := old.Scan(&retainedOID, &retainedName, &retainedACL); err != nil || retainedOID != oldOID || retainedName != "lc_messages" || retainedACL != acl { t.Fatal(retainedOID, retainedName, retainedACL, err) }
		if old.Next() || old.Err() != nil { t.Fatal(old.Err()) }; old.Close()
		pgParameterACLMetadata(t, saved, false)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
		if got := parameterSharedDatabaseState(t, p, b); got != dbState { t.Fatal("controls changed database authority", dbState, got) }
		c := create("c")
		check(a, b, c)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"parname", "acl"}, []string{"TEXT", "TEXT"}, expected))
		for _, view := range []fixture.Backend{b, c} {
			if got := pgParameterACLOID(t, p, view, "lc_messages"); got != pgParameterACLOID(t, p, a, "lc_messages") { t.Fatal("replacement peer identity differs", got) }
		}
		run(b, fixture.Select("SELECT v FROM pacs_control_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(71)}}))
		run(a, fixture.Select("SELECT has_parameter_privilege('pacs_reader','application_name','SET') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) { t.Fatal("canonical redump", err) }
		for _, invalid := range []string{
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{missing=s/postgres}'::pg_catalog.aclitem[] WHERE parname='lc_messages'::pg_catalog.text;",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{pacs_reader=s/missing}'::pg_catalog.aclitem[] WHERE parname='lc_messages'::pg_catalog.text;",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{pacs_reader=r/postgres}'::pg_catalog.aclitem[] WHERE parname='lc_messages'::pg_catalog.text;",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{pacs_reader=z/postgres}'::pg_catalog.aclitem[] WHERE parname='lc_messages'::pg_catalog.text;",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{}'::pg_catalog.aclitem[] WHERE parname='lc_messages'::pg_catalog.text OR true;",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{}'::pg_catalog.aclitem[],parname='other' WHERE parname='lc_messages'::pg_catalog.text;",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{}'::pg_catalog.aclitem[] WHERE parname=lower('lc_messages');",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{}'::pg_catalog.aclitem[] WHERE parname=$1::pg_catalog.text;",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{}'::pg_catalog.aclitem[] WHERE parname='lc_messages'::pg_catalog.text RETURNING parname;",
		} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"\n"+invalid)); err == nil { t.Fatal("invalid repair admitted", invalid) }
			check(a, b, c)
		}
		failure := errors.New("parameter shared reader failure")
		if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: failure}); !errors.Is(err, failure) { t.Fatal(err) }
		check(a, b, c)
		ctx, cancel := context.WithCancel(p.Ctx)
		if err := stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel}); !errors.Is(err, context.Canceled) { t.Fatal(err) }
		cancel(); check(a, b, c)
		pgParameterACLSharedDestination(t, p, dump.Bytes(), expected)
		pgParameterACLSharedPending(t, p, a, b, dump.Bytes())
		check(a, b, c)
		// A gated real reader proves callbacks execute outside cluster locks.
		gate := &fixture.SequenceGatedReader{Reader: bytes.NewReader(dump.Bytes()), Started: make(chan struct{}), Release: make(chan struct{})}
		done := make(chan error, 1)
		go func() { done <- stuntgres.Restore(p.Ctx, p.Native, gate) }()
		released := false
		defer func() { if !released { close(gate.Release); <-done } }()
		select { case <-gate.Started: case <-p.Ctx.Done(): t.Fatal(p.Ctx.Err()) }
		run(b, fixture.Exec("GRANT SET ON PARAMETER pacs_during_restore.value TO pacs_reader", 0))
		close(gate.Release); released = true
		if err := <-done; err == nil { t.Fatal("stale Restore overwrote peer publication") }
		check(a, b, c)
		for _, view := range []fixture.Backend{a, b, c} { run(view, fixture.Select("SELECT has_parameter_privilege('pacs_reader','pacs_during_restore.value','SET') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}})) }
	})
}

func parameterSharedDatabaseState(t *testing.T, p *fixture.Pair, b fixture.Backend) string {
	t.Helper()
	rows, err := b.DB.QueryContext(p.Ctx, "SELECT oid,datname,datdba,datacl::text FROM pg_database ORDER BY datname")
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	var result strings.Builder
	for rows.Next() {
		var oid, owner int64
		var name string
		var acl sql.NullString
		if err := rows.Scan(&oid, &name, &owner, &acl); err != nil { t.Fatal(err) }
		// Preserve exact SQL identities, not source/destination allocator equality.
		result.WriteString(name); result.WriteString("|"); result.WriteString(acl.String)
		result.WriteString("|"); result.WriteString(strconv.FormatInt(oid, 10)); result.WriteString("|"); result.WriteString(strconv.FormatInt(owner, 10)); result.WriteString("\n")
	}
	if err := rows.Err(); err != nil { t.Fatal(err) }
	return result.String()
}
