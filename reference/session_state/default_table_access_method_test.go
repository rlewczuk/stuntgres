package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

var defaultAMSetting = clientSetting{"default_table_access_method", "string", "heap", "heap", "settings_heap", "settings_heap", "Sets the default table access method for new tables."}

// The custom identity uses the genuine installed heap handler, not a second
// storage algorithm. Keep setup local: the registry-wide inventory needs no AM.
func defaultAMCases(t *testing.T, roles []string, body func(*physicalCase)) {
	t.Helper()
	clean := fixture.AccessMethodReferenceCleanup(t, []string{"settings_heap", "settings_denied"})
	if len(roles) != 0 {
		fixture.RoleReferenceCleanup(t, roles)
	}
	p := fixture.NewScenario(t)
	clean()
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			s := &physicalCase{t, p, c, b.PG, b.Recorder}
			s.run(fixture.Exec("CREATE ACCESS METHOD settings_heap TYPE TABLE HANDLER pg_catalog.heap_tableam_handler", 0))
			s.run(fixture.Select("SELECT oid<>0 AND oid<>2 AS distinct_method FROM pg_am WHERE amname='settings_heap'", []string{"distinct_method"}, []string{"BOOL"}, [][]any{{true}}))
			body(s)
		})
	}
}

func defaultAMRun(s *physicalCase, prepared bool, step fixture.Step) {
	step.Prepared = prepared
	s.run(step)
}

func defaultAMIdentity(name, method string) fixture.Step {
	return fixture.Select("SELECT a.amname,c.relam=a.oid AS exact FROM pg_class c JOIN pg_am a ON a.oid=c.relam WHERE c.oid=$1::regclass", []string{"amname", "exact"}, []string{"NAME", "BOOL"}, [][]any{{method, true}}, name)
}

func defaultAMRows(name string, rows [][]any) fixture.Step {
	return fixture.Select("SELECT id,note FROM "+name+" ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, rows)
}

func defaultAMKeptRows() [][]any { return [][]any{{int64(7), "keep"}, {int64(8), nil}} }

func defaultAMAbsent(name string) fixture.Step {
	return fixture.Select("SELECT relname FROM pg_class WHERE relname="+physicalLiteral(name), []string{"relname"}, []string{"NAME"}, nil)
}

func defaultAMSet(s *physicalCase, method string, prepared bool) {
	defaultAMRun(s, prepared, fixture.Exec("SET default_table_access_method="+physicalLiteral(method), 0))
}

func defaultAMWitness(s *physicalCase, name, method string, prepared bool) {
	s.clientReads(defaultAMSetting, method, prepared)
	defaultAMRun(s, prepared, fixture.Exec("CREATE TABLE "+name+"(id integer,note text)", 0))
	defaultAMRun(s, prepared, defaultAMIdentity(name, method))
	defaultAMRun(s, prepared, fixture.Exec("INSERT INTO "+name+" VALUES(7,'keep'),(8,NULL)", 2))
	defaultAMRun(s, prepared, defaultAMRows(name, defaultAMKeptRows()))
}

func defaultAMMetadata(s *physicalCase, custom, prepared bool) {
	want := []any{"default_table_access_method", "heap", nil, "Client Connection Defaults / Statement Behavior", "Sets the default table access method for new tables.", nil, "user", "string", "default", nil, nil, nil, "heap", "heap", nil, nil, false}
	if custom {
		want[1], want[8] = "settings_heap", "session"
	}
	step := fixture.Select("SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE name='default_table_access_method'", physicalColumns, physicalTypes, [][]any{want})
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		statisticsSettingsCheckMetadata(t, rows, s.pg, physicalColumns, physicalTypes)
	}
	defaultAMRun(s, prepared, step)
}

func TestReferenceDefaultTableAccessMethodValues(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			defaultAMCases(t, nil, func(s *physicalCase) {
				s.clientReads(defaultAMSetting, "heap", prepared)
				defaultAMMetadata(s, false, prepared)
				defaultAMWitness(s, "dam_base", "heap", prepared)
				defaultAMRun(s, prepared, fixture.Select("SELECT relam=2 AS builtin FROM pg_class WHERE oid='dam_base'::regclass", []string{"builtin"}, []string{"BOOL"}, [][]any{{true}}))
				defaultAMSet(s, "settings_heap", prepared)
				step := clientText("SELECT set_config('default_table_access_method','settings_heap',false)", "set_config", "settings_heap")
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { settingsTextMetadata(t, rows, s.pg, true) }
				defaultAMRun(s, prepared, step)
				s.clientReads(defaultAMSetting, "settings_heap", prepared)
				defaultAMMetadata(s, true, prepared)
				defaultAMRun(s, prepared, fixture.Exec("CREATE TABLE dam_plain(id integer,note text)", 0))
				defaultAMRun(s, prepared, fixture.Exec("INSERT INTO dam_plain VALUES(9,'new'),(10,NULL)", 2))
				for _, q := range []string{"CREATE TABLE dam_ctas AS SELECT id,note FROM dam_base", "CREATE MATERIALIZED VIEW dam_mv AS SELECT id,note FROM dam_base", "SELECT id,note INTO dam_into FROM dam_base"} {
					defaultAMRun(s, prepared, fixture.Exec(q, 2))
				}
				for _, name := range []string{"dam_plain", "dam_ctas", "dam_mv", "dam_into"} {
					defaultAMRun(s, prepared, defaultAMIdentity(name, "settings_heap"))
					rows := defaultAMKeptRows()
					if name == "dam_plain" {
						rows = [][]any{{int64(9), "new"}, {int64(10), nil}}
					}
					defaultAMRun(s, prepared, defaultAMRows(name, rows))
				}
				for i, reset := range []string{"RESET default_table_access_method", "SET default_table_access_method TO DEFAULT", "RESET ALL"} {
					defaultAMSet(s, "settings_heap", prepared)
					defaultAMRun(s, prepared, fixture.Exec(reset, 0))
					defaultAMMetadata(s, false, prepared)
					defaultAMWitness(s, fmt.Sprintf("dam_reset_%d", i), "heap", prepared)
				}
				defaultAMHeld(s)
				defaultAMRun(s, prepared, defaultAMIdentity("dam_base", "heap"))
				defaultAMRun(s, prepared, defaultAMRows("dam_base", defaultAMKeptRows()))
				for _, name := range []string{"dam_ctas", "dam_mv", "dam_into"} {
					defaultAMRun(s, prepared, defaultAMIdentity(name, "settings_heap"))
					defaultAMRun(s, prepared, defaultAMRows(name, defaultAMKeptRows()))
				}
				defaultAMRun(s, prepared, defaultAMIdentity("dam_plain", "settings_heap"))
				defaultAMRun(s, prepared, defaultAMRows("dam_plain", [][]any{{int64(9), "new"}, {int64(10), nil}}))
			})
		})
	}
}

func defaultAMHeld(s *physicalCase) {
	const create = "CREATE TABLE dam_held(id integer)"
	held, err := s.conn.PrepareContext(s.p.Ctx, create)
	if err != nil {
		s.t.Fatal(err)
	}
	defer held.Close()
	s.run(defaultAMAbsent("dam_held"))
	s.clientReads(defaultAMSetting, "heap", false)
	readers := make([]*sql.Stmt, 0, 2)
	queries := []string{"SHOW default_table_access_method", "SELECT current_setting('default_table_access_method')"}
	for _, q := range queries {
		stmt, err := s.conn.PrepareContext(s.p.Ctx, q)
		if err != nil {
			s.t.Fatal(err)
		}
		defer stmt.Close()
		readers = append(readers, stmt)
	}
	for _, method := range []string{"settings_heap", "heap"} {
		if method == "heap" {
			s.run(fixture.Exec("RESET default_table_access_method", 0))
		} else {
			defaultAMSet(s, method, false)
		}
		for i, q := range queries {
			column := "default_table_access_method"
			if i == 1 {
				column = "current_setting"
			}
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: readers[i], SQL: q}, s.recorder, clientText(q, column, method))
		}
		if method == "settings_heap" {
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: held, SQL: create}, s.recorder, fixture.Exec(create, 0))
			s.run(defaultAMIdentity("dam_held", method))
			s.run(fixture.Exec("INSERT INTO dam_held VALUES(11)", 1))
			s.run(fixture.Select("SELECT id FROM dam_held", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
		}
	}
}
