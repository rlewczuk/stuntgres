package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const ginLimitName = "gin_pending_list_limit"
const ginLimitBoundary = "gin_clean_pending_list physical GIN pending-list cleanup is unsupported in embedded mode"

func (s *physicalCase) ginState(display, raw, source string, prepared bool) {
	s.t.Helper()
	for _, q := range []string{"SHOW gin_pending_list_limit", "SELECT current_setting('gin_pending_list_limit')"} {
		column := "current_setting"
		if strings.HasPrefix(q, "SHOW") {
			column = ginLimitName
		}
		step := toastText(s.pg, q, column, display)
		step.Prepared = prepared
		s.run(step)
	}
	step := fixture.Select("SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE name='gin_pending_list_limit'", physicalColumns, physicalTypes, [][]any{{
		ginLimitName, raw, "kB", "Client Connection Defaults / Statement Behavior",
		"Sets the maximum size of the pending list for GIN index.", nil, "user", "integer", source,
		"64", "2147483647", nil, "4096", "4096", nil, nil, false,
	}})
	step.Prepared = prepared
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		statisticsSettingsCheckMetadata(t, rows, s.pg, physicalColumns, physicalTypes)
	}
	s.run(step)
}

func (s *physicalCase) ginPeer() *physicalCase {
	s.t.Helper()
	pool := s.p.Memory
	if s.pg {
		pool = s.p.Postgres
	}
	c, err := pool.Conn(s.p.Ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { c.Close() })
	return &physicalCase{s.t, s.p, c, s.pg, s.recorder}
}

func ginLimitArray() string {
	var b strings.Builder
	b.WriteByte('{')
	for n := 1; n <= 10000; n++ {
		if n > 1 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(n))
	}
	b.WriteByte('}')
	return b.String()
}

func ginLimitResult(pg bool, q, typ string, rows [][]any, args ...any) fixture.Step {
	step := fixture.Select(q, []string{"cleaned"}, []string{typ}, rows, args...)
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		metadata, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(q, "SELECT cleaned FROM gin_limit_cleanup_view") {
			fixture.TableDDLColumnTypes(t, pg, metadata, []string{"cleaned"}, []string{typ}, []bool{true})
		} else {
			fixture.ScalarAssertMetadata(t, pg, metadata, 0)
		}
	}
	return step
}

func ginLimitDemand(pg bool, q, typ string, value any, args ...any) fixture.Step {
	if pg {
		return ginLimitResult(pg, q, typ, [][]any{{value}}, args...)
	}
	step := physicalError(q, physicalDiagnostic("0A000", ginLimitBoundary), false, true, args...)
	return step
}

func (s *physicalCase) ginHeld(step fixture.Step) {
	s.t.Helper()
	statement, err := s.conn.PrepareContext(s.p.Ctx, step.Query)
	if err != nil {
		s.t.Fatal("prepare without physical demand", err)
	}
	defer statement.Close()
	fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: statement, SQL: step.Query}, s.recorder, step)
}

func (s *physicalCase) ginLogical() {
	s.t.Helper()
	for _, table := range []string{"gin_limit_low", "gin_limit_high"} {
		s.run(fixture.Select("SELECT id,a::text AS a FROM "+table+" ORDER BY id", []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), ginLimitArray()}}))
		s.run(fixture.Select("SELECT id FROM "+table+" WHERE a @> '{1,10000}'::integer[] ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	}
	s.run(fixture.Select("SELECT relname,reloptions::text AS reloptions FROM pg_class WHERE relname IN ('gin_limit_low_i','gin_limit_high_i') ORDER BY relname", []string{"relname", "reloptions"}, []string{"NAME", "TEXT"}, [][]any{{"gin_limit_high_i", "{fastupdate=on}"}, {"gin_limit_low_i", "{fastupdate=on}"}}))
}

// Independent REL_18_6 ginfast.c threshold witness, not observed native counts:
// one large entry exceeds 64kB, but remains below 8MB. Never drain high twice
// expecting a positive result: every subsequent call must observe zero on PG.
func (s *physicalCase) ginInsertPair() {
	s.t.Helper()
	for _, table := range []string{"gin_limit_low", "gin_limit_high"} {
		s.run(fixture.Exec("CREATE TABLE "+table+"(id integer,a integer[])", 0))
		s.run(fixture.Exec("CREATE INDEX "+table+"_i ON "+table+" USING gin(a) WITH(fastupdate=on)", 0))
	}
	for i, value := range []string{"64kB", "8MB"} {
		s.run(fixture.Exec("SET gin_pending_list_limit="+physicalLiteral(value), 0))
		table, raw := "gin_limit_low", "64"
		if i == 1 {
			table, raw = "gin_limit_high", "8192"
		}
		s.ginState(value, raw, "session", i == 1)
		step := fixture.Exec("INSERT INTO "+table+" VALUES(1,$1::integer[])", 1, ginLimitArray())
		step.Prepared = i == 1
		s.run(step)
	}
	for i, value := range []string{"64kB", "8MB"} {
		s.run(fixture.Exec("SET gin_pending_list_limit="+physicalLiteral(value), 0))
		raw := "64"
		if i == 1 {
			raw = "8192"
		}
		s.ginState(value, raw, "session", false)
		s.ginLogical()
	}
}

func ginLimitInvalid(value string) fixture.Diagnostic {
	var message, hint string
	switch value {
	case "63", "-1":
		message = value + ` kB is outside the valid range for parameter "gin_pending_list_limit" (64 kB .. 2147483647 kB)`
	case "2147483648":
		hint = "Value exceeds integer range."
	case "1mb":
		hint = `Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`
	}
	if message == "" {
		message = `invalid value for parameter "gin_pending_list_limit": "` + value + `"`
	}
	d := physicalDiagnostic("22023", message)
	d.Hint = hint
	return d
}
