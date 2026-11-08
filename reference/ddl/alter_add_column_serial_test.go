package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// ALTER TABLE ADD COLUMN smallserial/serial/bigserial expands exactly as
// PostgreSQL 18.6 does: the column gets the integer width, an owned sequence
// named <table>_<column>_seq, a nextval DEFAULT and implicit NOT NULL, and
// existing rows are filled from that sequence. Every expectation here is the
// pinned 18.6 oracle capture, never native output.

type addColumnSerialNames func(string) string

type addColumnSerialRoute struct {
	name, suffix string
	prepared     bool
}

var addColumnSerialRoutes = []addColumnSerialRoute{
	{name: "direct"},
	{name: "prepared", suffix: "_p", prepared: true},
}

// addColumnSerialSession groups one route's notice-capable session: notices,
// prepared steps and metadata all observe the same connection.
type addColumnSerialSession struct {
	p        *fixture.Pair
	conn     *sql.Conn
	recorder *fixture.Recorder
	prepared bool
	name     addColumnSerialNames
	notices  *[]fixture.Diagnostic
}

func (s addColumnSerialSession) run(t *testing.T, step fixture.Step) {
	t.Helper()
	if s.prepared {
		step = fixture.Prepared(step)
	}
	fixture.RunStep(t, s.p.Ctx, s.conn, s.recorder, step)
}

func (s addColumnSerialSession) exec(t *testing.T, query string, affected int64) {
	t.Helper()
	s.run(t, fixture.Exec(query, affected))
}

func addColumnSerialScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

// addColumnSerialForEachRoute runs one backend body for each route; route
// suffixed names let both routes share one schema.
func addColumnSerialForEachRoute(t *testing.T, p *fixture.Pair, b fixture.Backend, body func(t *testing.T, s addColumnSerialSession)) {
	t.Helper()
	for _, route := range addColumnSerialRoutes {
		t.Run(route.name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			body(t, addColumnSerialSession{
				p: p, conn: conn, recorder: recorder, prepared: route.prepared,
				name: func(base string) string { return base + route.suffix }, notices: &notices,
			})
		})
	}
}

// addColumnSerialPosition reports the 1-based character position of token.
func addColumnSerialPosition(query, token string) string {
	return fixture.Position(len([]rune(query[:strings.Index(query, token)])) + 1)
}

func addColumnSerialAttributes(table string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT a.attname,t.typname,format_type(a.atttypid,a.atttypmod),a.attnum,a.attnotnull,a.atthasdef FROM pg_attribute a JOIN pg_type t ON t.oid=a.atttypid WHERE a.attrelid=$1::regclass AND a.attnum>0 ORDER BY a.attnum", []string{"attname", "typname", "format_type", "attnum", "attnotnull", "atthasdef"}, []string{"NAME", "NAME", "TEXT", "INT2", "BOOL", "BOOL"}, rows, table)
}

func addColumnSerialDefaults(table string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT adnum,pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid=$1::regclass ORDER BY adnum", []string{"adnum", "pg_get_expr"}, []string{"INT2", "TEXT"}, rows, table)
}

// addColumnSerialColumnDefault locates the default by column name, so a
// statement sequence that drops a column does not depend on attnum reuse.
func addColumnSerialColumnDefault(table, column string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT a.attname,pg_get_expr(d.adbin,d.adrelid) FROM pg_attribute a JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid=$1::regclass AND a.attname=$2", []string{"attname", "pg_get_expr"}, []string{"NAME", "TEXT"}, rows, table, column)
}

func addColumnSerialDefaultDependencies(table string, attribute int64, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT d.deptype,d.refobjid::regclass::text AS refname,d.refobjsubid FROM pg_depend d WHERE d.classid='pg_attrdef'::regclass AND d.objid=(SELECT oid FROM pg_attrdef WHERE adrelid=$1::regclass AND adnum=$2) AND d.refclassid='pg_class'::regclass ORDER BY deptype,refname,refobjsubid", []string{"deptype", "refname", "refobjsubid"}, []string{"CHAR", "TEXT", "INT4"}, rows, table, attribute)
}

func addColumnSerialOwnership(sequence string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT refobjsubid,deptype FROM pg_depend WHERE classid='pg_class'::regclass AND objid=$1::regclass AND refclassid='pg_class'::regclass AND refobjsubid>0 ORDER BY refobjsubid", []string{"refobjsubid", "deptype"}, []string{"INT4", "CHAR"}, rows, sequence)
}

func addColumnSerialOptions(sequence string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT c.relname,t.typname,format_type(s.seqtypid,NULL::integer),s.seqstart,s.seqincrement,s.seqmax,s.seqmin,s.seqcache,s.seqcycle FROM pg_sequence s JOIN pg_class c ON c.oid=s.seqrelid JOIN pg_type t ON t.oid=s.seqtypid WHERE c.relname=$1", []string{"relname", "typname", "format_type", "seqstart", "seqincrement", "seqmax", "seqmin", "seqcache", "seqcycle"}, []string{"NAME", "NAME", "TEXT", "INT8", "INT8", "INT8", "INT8", "INT8", "BOOL"}, rows, sequence)
}

func addColumnSerialState(sequence string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT last_value,is_called FROM "+sequence, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, rows)
}

func addColumnSerialRelationCount(name string, count int64) fixture.Step {
	return fixture.Select("SELECT count(*) FROM pg_class WHERE relname=$1", []string{"count"}, []string{"INT8"}, [][]any{{count}}, name)
}

func addColumnSerialRelationSet(first, second string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT relname FROM pg_class WHERE relname IN ($1,$2) ORDER BY relname", []string{"relname"}, []string{"NAME"}, rows, first, second)
}

// TestReferenceAlterAddColumnSerialCore replays the recorded repro: two serial
// columns added to a populated table fill existing rows in physical order and
// publish the same attribute, default, dependency and sequence catalog objects
// as CREATE TABLE serial.
func TestReferenceAlterAddColumnSerialCore(t *testing.T) {
	addColumnSerialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnSerialForEachRoute(t, p, b, func(t *testing.T, s addColumnSerialSession) {
			table := s.name("ser_core")
			region, big := table+"_region_id_seq", table+"_big_id_seq"
			s.exec(t, "CREATE TABLE "+table+"(id INT)", 0)
			s.exec(t, "INSERT INTO "+table+" VALUES(10),(20),(30)", 3)
			s.exec(t, "ALTER TABLE "+table+" ADD COLUMN region_id serial NOT NULL", 0)
			s.exec(t, "ALTER TABLE "+table+" ADD COLUMN big_id bigserial", 0)
			s.run(t, fixture.Select("SELECT id,region_id,big_id FROM "+table+" ORDER BY id", []string{"id", "region_id", "big_id"}, []string{"INT4", "INT4", "INT8"}, [][]any{
				{int64(10), int64(1), int64(1)},
				{int64(20), int64(2), int64(2)},
				{int64(30), int64(3), int64(3)},
			}))
			s.run(t, addColumnSerialAttributes(table,
				[]any{"id", "int4", "integer", int64(1), false, false},
				[]any{"region_id", "int4", "integer", int64(2), true, true},
				[]any{"big_id", "int8", "bigint", int64(3), true, true},
			))
			s.run(t, addColumnSerialDefaults(table,
				[]any{int64(2), "nextval('" + region + "'::regclass)"},
				[]any{int64(3), "nextval('" + big + "'::regclass)"},
			))
			s.run(t, addColumnSerialDefaultDependencies(table, 2,
				[]any{"a", table, int64(2)},
				[]any{"n", region, int64(0)},
			))
			s.run(t, addColumnSerialDefaultDependencies(table, 3,
				[]any{"a", table, int64(3)},
				[]any{"n", big, int64(0)},
			))
			s.run(t, addColumnSerialOwnership(region, []any{int64(2), "a"}))
			s.run(t, addColumnSerialOwnership(big, []any{int64(3), "a"}))
			s.run(t, addColumnSerialOptions(region, []any{region, "int4", "integer", int64(1), int64(1), int64(2147483647), int64(1), int64(1), false}))
			s.run(t, addColumnSerialOptions(big, []any{big, "int8", "bigint", int64(1), int64(1), int64(9223372036854775807), int64(1), int64(1), false}))
			s.exec(t, "INSERT INTO "+table+"(id) VALUES(40)", 1)
			s.run(t, fixture.Select("SELECT id,region_id,big_id FROM "+table+" WHERE id=40", []string{"id", "region_id", "big_id"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(40), int64(4), int64(4)}}))
			fixture.TableDDLMetadata(t, p.Ctx, s.conn, b.PG, s.prepared, "SELECT * FROM "+table+" ORDER BY id", []string{"id", "region_id", "big_id"}, []string{"INT4", "INT4", "INT8"}, []bool{true, false, false}, 4)
		})
	})
}

// TestReferenceAlterAddColumnSerialWidths proves the width aliases, the
// sequence data types and bounds, ChooseRelationName numbering, quoted name
// resolution and the persistence inherited from TEMP and UNLOGGED tables.
func TestReferenceAlterAddColumnSerialWidths(t *testing.T) {
	addColumnSerialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnSerialForEachRoute(t, p, b, func(t *testing.T, s addColumnSerialSession) {
			table := s.name("ser_widths")
			s.exec(t, "CREATE TABLE "+table+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+table+" ADD COLUMN d serial2, ADD COLUMN e serial4, ADD COLUMN f serial8", 0)
			s.run(t, addColumnSerialAttributes(table,
				[]any{"id", "int4", "integer", int64(1), false, false},
				[]any{"d", "int2", "smallint", int64(2), true, true},
				[]any{"e", "int4", "integer", int64(3), true, true},
				[]any{"f", "int8", "bigint", int64(4), true, true},
			))
			s.run(t, addColumnSerialDefaults(table,
				[]any{int64(2), "nextval('" + table + "_d_seq'::regclass)"},
				[]any{int64(3), "nextval('" + table + "_e_seq'::regclass)"},
				[]any{int64(4), "nextval('" + table + "_f_seq'::regclass)"},
			))
			s.run(t, addColumnSerialOptions(table+"_d_seq", []any{table + "_d_seq", "int2", "smallint", int64(1), int64(1), int64(32767), int64(1), int64(1), false}))
			s.run(t, addColumnSerialOptions(table+"_e_seq", []any{table + "_e_seq", "int4", "integer", int64(1), int64(1), int64(2147483647), int64(1), int64(1), false}))
			s.run(t, addColumnSerialOptions(table+"_f_seq", []any{table + "_f_seq", "int8", "bigint", int64(1), int64(1), int64(9223372036854775807), int64(1), int64(1), false}))

			alias := s.name("ser_alias")
			s.exec(t, "CREATE TABLE "+alias+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+alias+" ADD COLUMN a smallserial, ADD COLUMN b serial, ADD COLUMN c bigserial", 0)
			s.run(t, addColumnSerialAttributes(alias,
				[]any{"id", "int4", "integer", int64(1), false, false},
				[]any{"a", "int2", "smallint", int64(2), true, true},
				[]any{"b", "int4", "integer", int64(3), true, true},
				[]any{"c", "int8", "bigint", int64(4), true, true},
			))
			s.run(t, addColumnSerialDefaults(alias,
				[]any{int64(2), "nextval('" + alias + "_a_seq'::regclass)"},
				[]any{int64(3), "nextval('" + alias + "_b_seq'::regclass)"},
				[]any{int64(4), "nextval('" + alias + "_c_seq'::regclass)"},
			))

			quoted := s.name("ser_quoted")
			s.exec(t, "CREATE TABLE "+quoted+"(id INT)", 0)
			s.exec(t, `ALTER TABLE `+quoted+` ADD COLUMN q "serial"`, 0)
			s.run(t, addColumnSerialAttributes(quoted,
				[]any{"id", "int4", "integer", int64(1), false, false},
				[]any{"q", "int4", "integer", int64(2), true, true},
			))
			s.run(t, addColumnSerialDefaults(quoted, []any{int64(2), "nextval('" + quoted + "_q_seq'::regclass)"}))

			upper := s.name("ser_upper")
			s.exec(t, "CREATE TABLE "+upper+"(id INT)", 0)
			query := `ALTER TABLE ` + upper + ` ADD COLUMN b "SERIAL"`
			want := fixture.ControlsReferenceDiagnostic("42704", `type "SERIAL" does not exist`)
			want.Position = addColumnSerialPosition(query, `"SERIAL"`)
			step := fixture.ImplicitError(query, want)
			step.ErrorStage = "execute"
			s.run(t, step)
			s.run(t, addColumnSerialAttributes(upper, []any{"id", "int4", "integer", int64(1), false, false}))
			s.run(t, addColumnSerialRelationSet(upper, upper+"_b_seq", []any{upper}))

			qualified := s.name("ser_qual")
			schema := s.name("s1")
			s.exec(t, "CREATE TABLE "+qualified+"(id INT)", 0)
			s.exec(t, "CREATE SCHEMA "+schema, 0)
			query = "ALTER TABLE " + qualified + " ADD COLUMN b " + schema + ".serial"
			want = fixture.ControlsReferenceDiagnostic("42704", `type "`+schema+`.serial" does not exist`)
			want.Position = addColumnSerialPosition(query, schema+".serial")
			step = fixture.ImplicitError(query, want)
			step.ErrorStage = "execute"
			s.run(t, step)
			s.run(t, addColumnSerialAttributes(qualified, []any{"id", "int4", "integer", int64(1), false, false}))
			s.run(t, addColumnSerialRelationSet(qualified, qualified+"_b_seq", []any{qualified}))

			numbered := s.name("ser_dup")
			s.exec(t, "CREATE TABLE "+numbered+"(id INT)", 0)
			s.exec(t, "CREATE SEQUENCE "+numbered+"_c_seq", 0)
			s.exec(t, "ALTER TABLE "+numbered+" ADD COLUMN c serial", 0)
			s.run(t, addColumnSerialDefaults(numbered, []any{int64(2), "nextval('" + numbered + "_c_seq1'::regclass)"}))
			s.run(t, addColumnSerialRelationSet(numbered+"_c_seq", numbered+"_c_seq1", []any{numbered + "_c_seq"}, []any{numbered + "_c_seq1"}))

			temp := s.name("ser_tmp")
			s.exec(t, "CREATE TEMP TABLE "+temp+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+temp+" ADD COLUMN c serial", 0)
			s.run(t, fixture.Select("SELECT relpersistence FROM pg_class WHERE relname=$1", []string{"relpersistence"}, []string{"CHAR"}, [][]any{{"t"}}, temp+"_c_seq"))

			unlogged := s.name("ser_unlogged")
			s.exec(t, "CREATE UNLOGGED TABLE "+unlogged+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+unlogged+" ADD COLUMN c serial", 0)
			s.run(t, fixture.Select("SELECT relpersistence FROM pg_class WHERE relname=$1", []string{"relpersistence"}, []string{"CHAR"}, [][]any{{"u"}}, unlogged+"_c_seq"))
		})
	})
}

// TestReferenceAlterAddColumnSerialDefaults covers an empty table, the exact
// persisted nextval text, the dependency edges and every drop route: column
// ownership, table ownership, detaching with OWNED BY NONE and RESTRICT.
func TestReferenceAlterAddColumnSerialDefaults(t *testing.T) {
	addColumnSerialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnSerialForEachRoute(t, p, b, func(t *testing.T, s addColumnSerialSession) {
			table := s.name("ser_defaults")
			seq := table + "_c_seq"
			s.exec(t, "CREATE TABLE "+table+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+table+" ADD COLUMN c serial", 0)
			s.run(t, addColumnSerialState(seq, []any{int64(1), false}))
			s.run(t, addColumnSerialDefaults(table, []any{int64(2), "nextval('" + seq + "'::regclass)"}))
			s.run(t, addColumnSerialDefaultDependencies(table, 2,
				[]any{"a", table, int64(2)},
				[]any{"n", seq, int64(0)},
			))
			s.run(t, addColumnSerialOwnership(seq, []any{int64(2), "a"}))
			s.exec(t, "INSERT INTO "+table+"(id) VALUES(1)", 1)
			s.run(t, fixture.Select("SELECT id,c FROM "+table, []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
			s.run(t, addColumnSerialState(seq, []any{int64(1), true}))
			s.exec(t, "INSERT INTO "+table+" VALUES(2,50)", 1)
			s.run(t, addColumnSerialState(seq, []any{int64(1), true}))
			s.exec(t, "INSERT INTO "+table+"(id) VALUES(3)", 1)
			s.run(t, fixture.Select("SELECT id,c FROM "+table+" ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{
				{int64(1), int64(1)},
				{int64(2), int64(50)},
				{int64(3), int64(2)},
			}))

			dropped := s.name("ser_dropcol")
			droppedSeq := dropped + "_c_seq"
			s.exec(t, "CREATE TABLE "+dropped+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+dropped+" ADD COLUMN c serial", 0)
			s.exec(t, "ALTER TABLE "+dropped+" DROP COLUMN c", 0)
			s.run(t, addColumnSerialRelationCount(droppedSeq, 0))
			s.run(t, addColumnSerialRelationCount(dropped, 1))

			gone := s.name("ser_droptable")
			goneSeq := gone + "_c_seq"
			s.exec(t, "CREATE TABLE "+gone+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+gone+" ADD COLUMN c serial", 0)
			s.exec(t, "DROP TABLE "+gone, 0)
			s.run(t, addColumnSerialRelationSet(gone, goneSeq))

			kept := s.name("ser_keep")
			keptSeq := kept + "_c_seq"
			s.exec(t, "CREATE TABLE "+kept+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+kept+" ADD COLUMN c serial", 0)
			s.exec(t, "ALTER SEQUENCE "+keptSeq+" OWNED BY NONE", 0)
			s.exec(t, "DROP TABLE "+kept, 0)
			s.run(t, addColumnSerialRelationCount(keptSeq, 1))
			s.exec(t, "DROP SEQUENCE "+keptSeq, 0)

			restrict := s.name("ser_restrict")
			restrictSeq := restrict + "_c_seq"
			s.exec(t, "CREATE TABLE "+restrict+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+restrict+" ADD COLUMN c serial", 0)
			restricted := fixture.PgDependRestrict("sequence "+restrictSeq, "default value for column c of table "+restrict+" depends on sequence "+restrictSeq)
			s.run(t, fixture.ErrorDiagnostic("DROP SEQUENCE "+restrictSeq, restricted))
			s.run(t, addColumnSerialRelationCount(restrictSeq, 1))
			s.exec(t, "DROP TABLE "+restrict, 0)
		})
	})
}

// TestReferenceAlterAddColumnSerialMutations covers statements that must not
// disturb the sequence: IF NOT EXISTS skipping, drop and re-add, rename,
// RESTART and two independent serials in one statement.
func TestReferenceAlterAddColumnSerialMutations(t *testing.T) {
	addColumnSerialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnSerialForEachRoute(t, p, b, func(t *testing.T, s addColumnSerialSession) {
			table := s.name("c2")
			notice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42701", Message: `column "b" of relation "` + table + `" already exists, skipping`}
			assertNotice := func() {
				t.Helper()
				if len(*s.notices) != 1 || (*s.notices)[0] != notice {
					t.Fatalf("notices %#v, want one %#v", *s.notices, notice)
				}
				*s.notices = nil
			}
			s.exec(t, "CREATE TABLE "+table+"(id INT, b INT)", 0)
			s.exec(t, "INSERT INTO "+table+" VALUES(1,5)", 1)
			s.exec(t, "ALTER TABLE "+table+" ADD COLUMN IF NOT EXISTS b serial", 0)
			assertNotice()
			s.run(t, addColumnSerialRelationSet(table, table+"_b_seq", []any{table}))
			s.run(t, addColumnSerialAttributes(table,
				[]any{"id", "int4", "integer", int64(1), false, false},
				[]any{"b", "int4", "integer", int64(2), false, false},
			))
			s.run(t, fixture.Select("SELECT id,b FROM "+table, []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(5)}}))

			reuse := s.name("ser_reuse")
			reuseSeq := reuse + "_c_seq"
			s.exec(t, "CREATE TABLE "+reuse+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+reuse+" ADD COLUMN c serial", 0)
			s.run(t, addColumnSerialDefaults(reuse, []any{int64(2), "nextval('" + reuseSeq + "'::regclass)"}))
			s.exec(t, "ALTER TABLE "+reuse+" DROP COLUMN c", 0)
			s.run(t, addColumnSerialRelationCount(reuseSeq, 0))
			s.exec(t, "ALTER TABLE "+reuse+" ADD COLUMN c serial", 0)
			s.run(t, addColumnSerialColumnDefault(reuse, "c", []any{"c", "nextval('" + reuseSeq + "'::regclass)"}))
			s.run(t, addColumnSerialRelationCount(reuseSeq, 1))

			renamed := s.name("ser_renamed")
			renamedSeq := renamed + "_c_seq"
			s.exec(t, "CREATE TABLE "+renamed+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+renamed+" ADD COLUMN c serial", 0)
			s.exec(t, "ALTER TABLE "+renamed+" RENAME COLUMN c TO d", 0)
			s.run(t, fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid=$1::regclass AND attnum=2", []string{"attname"}, []string{"NAME"}, [][]any{{"d"}}, renamed))
			s.run(t, addColumnSerialDefaults(renamed, []any{int64(2), "nextval('" + renamedSeq + "'::regclass)"}))
			s.run(t, addColumnSerialRelationCount(renamedSeq, 1))

			restart := s.name("ser_restart")
			restartSeq := restart + "_c_seq"
			s.exec(t, "CREATE TABLE "+restart+"(id INT)", 0)
			s.exec(t, "INSERT INTO "+restart+" VALUES(1),(2)", 2)
			s.exec(t, "ALTER TABLE "+restart+" ADD COLUMN c serial", 0)
			s.run(t, fixture.Select("SELECT id,c FROM "+restart+" ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}))
			s.exec(t, "ALTER SEQUENCE "+restartSeq+" RESTART WITH 10", 0)
			s.run(t, addColumnSerialState(restartSeq, []any{int64(10), false}))
			s.run(t, fixture.Select("SELECT id,c FROM "+restart+" ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}))
			s.exec(t, "INSERT INTO "+restart+"(id) VALUES(3)", 1)
			s.run(t, fixture.Select("SELECT id,c FROM "+restart+" ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{
				{int64(1), int64(1)},
				{int64(2), int64(2)},
				{int64(3), int64(10)},
			}))
			s.run(t, addColumnSerialState(restartSeq, []any{int64(10), true}))

			two := s.name("ser_two")
			s.exec(t, "CREATE TABLE "+two+"(id INT)", 0)
			s.exec(t, "INSERT INTO "+two+" VALUES(1),(2)", 2)
			s.exec(t, "ALTER TABLE "+two+" ADD COLUMN a serial, ADD COLUMN b serial", 0)
			s.run(t, fixture.Select("SELECT id,a,b FROM "+two+" ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{
				{int64(1), int64(1), int64(1)},
				{int64(2), int64(2), int64(2)},
			}))
			s.run(t, addColumnSerialState(two+"_a_seq", []any{int64(2), true}))
			s.run(t, addColumnSerialState(two+"_b_seq", []any{int64(2), true}))
		})
	})
}
