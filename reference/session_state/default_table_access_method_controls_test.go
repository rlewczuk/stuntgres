package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDefaultTableAccessMethodControls(t *testing.T) {
	defaultAMCases(t, nil, func(s *physicalCase) {
		defaultAMWitness(s, "dam_control_base", "heap", false)
		defaultAMSet(s, "settings_heap", true)
		defaultAMWitness(s, "dam_control_custom", "settings_heap", true)
		defaultAMMetadata(s, true, true)
		if s.pg {
			return
		} // Native controls have no PostgreSQL API equivalent.

		rows, err := s.conn.QueryContext(s.p.Ctx, "SELECT id,note FROM dam_control_custom ORDER BY id")
		if err != nil {
			s.t.Fatal(err)
		}
		defer rows.Close()
		metadata, err := s.conn.QueryContext(s.p.Ctx, "SELECT setting FROM pg_settings WHERE name='default_table_access_method'")
		if err != nil {
			s.t.Fatal(err)
		}
		defer metadata.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
			s.t.Fatal(err)
		}
		if dump.Len() == 0 {
			s.t.Fatal("empty actual dump")
		}
		if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
			s.t.Fatal(err)
		}
		s.clientReads(defaultAMSetting, "settings_heap", true)
		s.run(fixture.Select("SELECT amname FROM pg_am WHERE amname='settings_heap'", []string{"amname"}, []string{"NAME"}, nil))
		s.run(physicalError("CREATE TABLE dam_reset_missing(id integer)", physicalDiagnostic("42704", `access method "settings_heap" does not exist`), true, false))
		s.run(defaultAMAbsent("dam_reset_missing"))
		fixture.StringCoreRetainedRows(s.t, rows, nil, []string{"id", "note"}, []string{"INT4", "TEXT"}, defaultAMKeptRows(), false)
		fixture.StringCoreRetainedRows(s.t, metadata, nil, []string{"setting"}, []string{"TEXT"}, [][]any{{"settings_heap"}}, false)
		rows.Close()
		metadata.Close()
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			s.t.Fatal(err)
		}
		defaultAMControlImported(s)
		defaultAMMetadata(s, true, true)
		defaultAMWitness(s, "dam_source_after_restore", "settings_heap", true)
		defaultAMMalformed(s, s.p.Native)

		connector, err := stuntgres.NewDriver().OpenConnector("default-am-restored")
		if err != nil {
			s.t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		recorded, recorder := fixture.RecordedNativePool(s.t, connector)
		c, err := recorded.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer c.Close()
		target := &physicalCase{s.t, s.p, c, false, recorder}
		target.clientReads(defaultAMSetting, "heap", true)
		if err := stuntgres.Restore(s.p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
			s.t.Fatal(err)
		}
		target.clientReads(defaultAMSetting, "heap", true)
		defaultAMMetadata(target, false, true)
		defaultAMControlImported(target)
		defaultAMWitness(target, "dam_destination_default", "heap", true)
		target.run(fixture.Prepared(fixture.Exec("INSERT INTO dam_control_custom VALUES(9,'restored')", 1)))
		target.run(defaultAMRows("dam_control_custom", [][]any{{int64(7), "keep"}, {int64(8), nil}, {int64(9), "restored"}}))
		defaultAMSet(target, "settings_heap", true)
		defaultAMWitness(target, "dam_destination_custom", "settings_heap", true)
		defaultAMMalformed(target, raw)
		target.run(defaultAMRows("dam_control_custom", [][]any{{int64(7), "keep"}, {int64(8), nil}, {int64(9), "restored"}}))
		target.run(fixture.Exec("RESET default_table_access_method", 0))
		target.clientReads(defaultAMSetting, "heap", true)
		defaultAMControlImported(s) // Destination operations never change source data.
	})
}

func defaultAMControlImported(s *physicalCase) {
	for _, v := range []struct{ name, method string }{{"dam_control_base", "heap"}, {"dam_control_custom", "settings_heap"}} {
		s.run(fixture.Prepared(defaultAMIdentity(v.name, v.method)))
		s.run(fixture.Prepared(defaultAMRows(v.name, defaultAMKeptRows())))
	}
}

func defaultAMMalformed(s *physicalCase, raw *sql.DB) {
	var before, after bytes.Buffer
	if err := stuntgres.Dump(s.p.Ctx, raw, &before); err != nil {
		s.t.Fatal(err)
	}
	// Reuse the control API's existing malformed-parser failure contract; this
	// is not a new SQL wire command or an invented control SQLSTATE.
	if err := stuntgres.Restore(s.p.Ctx, raw, strings.NewReader("CREATE TABLE broken(")); err == nil {
		s.t.Fatal("malformed replacement succeeded")
	}
	if err := stuntgres.Dump(s.p.Ctx, raw, &after); err != nil {
		s.t.Fatal(err)
	}
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		s.t.Fatal("malformed replacement changed committed catalog/data")
	}
	s.clientReads(defaultAMSetting, "settings_heap", true)
	defaultAMMetadata(s, true, true)
	s.run(defaultAMIdentity("dam_control_custom", "settings_heap"))
}
