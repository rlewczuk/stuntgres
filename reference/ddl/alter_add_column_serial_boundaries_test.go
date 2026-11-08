package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// ALTER TABLE ADD COLUMN serial expands to a column, an owned sequence and a
// nextval default exactly as CREATE TABLE serial does. Every expectation in
// this file is the pinned PostgreSQL 18.6 oracle capture, never native output.

const addColumnSerialBoundaryAttributesSQL = "SELECT attname,attnotnull,attnum FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 ORDER BY attnum"

func addColumnSerialBoundaryScenario(t *testing.T, body func(t *testing.T, p *fixture.Pair, b fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

type addColumnSerialBoundarySession struct {
	p        *fixture.Pair
	conn     *sql.Conn
	recorder *fixture.Recorder
	prepared bool
	name     func(string) string
}

func (s addColumnSerialBoundarySession) run(t *testing.T, step fixture.Step) {
	t.Helper()
	if s.prepared {
		step = fixture.Prepared(step)
	}
	fixture.RunStep(t, s.p.Ctx, s.conn, s.recorder, step)
}

func (s addColumnSerialBoundarySession) exec(t *testing.T, query string, affected int64) {
	t.Helper()
	s.run(t, fixture.Exec(query, affected))
}

// addColumnSerialBoundaryForEachRoute runs one backend body for each route;
// route-suffixed names let both routes share one schema.
func addColumnSerialBoundaryForEachRoute(t *testing.T, p *fixture.Pair, b fixture.Backend, body func(t *testing.T, s addColumnSerialBoundarySession)) {
	t.Helper()
	for _, route := range []struct {
		name, suffix string
		prepared     bool
	}{
		{name: "direct"},
		{name: "prepared", suffix: "_p", prepared: true},
	} {
		t.Run(route.name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			body(t, addColumnSerialBoundarySession{
				p: p, conn: conn, recorder: recorder, prepared: route.prepared,
				name: func(base string) string { return base + route.suffix },
			})
		})
	}
}

// addColumnSerialBoundaryRaw captures a complete result set for before/after
// comparisons; it is used only within one backend where scan representations
// are stable.
func addColumnSerialBoundaryRaw(t *testing.T, p *fixture.Pair, c *sql.Conn, query string, args ...any) [][]any {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var bag [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		bag = append(bag, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return bag
}

func addColumnSerialBoundaryConstraints(table string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT conname,contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass ORDER BY conname", []string{"conname", "contype", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "TEXT"}, rows, table)
}

// TestReferenceAlterAddColumnSerialAtomicity proves that a failed ADD COLUMN
// serial publishes neither the column nor its sequence: the check and NOT NULL
// backfill failures leave the table exactly as it was, and an explicit
// ROLLBACK discards a successful ADD so a retry starts fresh.
func TestReferenceAlterAddColumnSerialAtomicity(t *testing.T) {
	addColumnSerialBoundaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnSerialBoundaryForEachRoute(t, p, b, func(t *testing.T, s addColumnSerialBoundarySession) {
			at := s.name("ser_atomic")
			s.exec(t, "CREATE TABLE "+at+"(id INT)", 0)
			s.exec(t, "INSERT INTO "+at+" VALUES(1),(2)", 2)
			rows := addColumnSerialBoundaryRaw(t, p, s.conn, "SELECT * FROM "+at+" ORDER BY id")
			attributes := addColumnSerialBoundaryRaw(t, p, s.conn, addColumnSerialBoundaryAttributesSQL, at)
			assertUnchanged := func() {
				t.Helper()
				if got := addColumnSerialBoundaryRaw(t, p, s.conn, "SELECT * FROM "+at+" ORDER BY id"); !reflect.DeepEqual(got, rows) {
					t.Fatalf("failed ADD changed rows: %#v -> %#v", rows, got)
				}
				if got := addColumnSerialBoundaryRaw(t, p, s.conn, addColumnSerialBoundaryAttributesSQL, at); !reflect.DeepEqual(got, attributes) {
					t.Fatalf("failed ADD changed attributes: %#v -> %#v", attributes, got)
				}
				s.run(t, fixture.Select("SELECT count(*) FROM pg_class WHERE relname LIKE $1 || '%'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, at))
			}

			violation := fixture.ControlsReferenceDiagnostic("23514", `check constraint "`+at+`_b_check" of relation "`+at+`" is violated by some row`)
			violation.Schema, violation.Table, violation.Constraint = "public", at, at+"_b_check"
			s.run(t, fixture.ImplicitError("ALTER TABLE "+at+" ADD COLUMN b serial CHECK (b > 100)", violation))
			assertUnchanged()

			nulls := fixture.ControlsReferenceDiagnostic("23502", `column "d" of relation "`+at+`" contains null values`)
			nulls.Schema, nulls.Table, nulls.Column = "public", at, "d"
			s.run(t, fixture.ImplicitError("ALTER TABLE "+at+" ADD COLUMN b serial, ADD COLUMN d int NOT NULL", nulls))
			assertUnchanged()

			s.exec(t, "BEGIN", 0)
			s.exec(t, "ALTER TABLE "+at+" ADD COLUMN b serial", 0)
			s.exec(t, "ROLLBACK", 0)
			assertUnchanged()

			s.exec(t, "ALTER TABLE "+at+" ADD COLUMN b serial", 0)
			s.run(t, fixture.Select("SELECT id,b FROM "+at+" ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}))
			s.run(t, fixture.Select("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, [][]any{{at + "_b_seq"}}, at+"_b_seq"))
		})
	})
}

// TestReferenceAlterAddColumnSerialInheritance proves the parent ADD creates
// exactly one owned sequence and both the parent and the existing child carry
// the parent sequence's nextval default; child rows and later inserts draw from
// the shared parent sequence.
func TestReferenceAlterAddColumnSerialInheritance(t *testing.T) {
	addColumnSerialBoundaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnSerialBoundaryForEachRoute(t, p, b, func(t *testing.T, s addColumnSerialBoundarySession) {
			par, chi := s.name("ser_inh_par"), s.name("ser_inh_chi")
			s.exec(t, "CREATE TABLE "+par+"(id int)", 0)
			s.exec(t, "CREATE TABLE "+chi+"() INHERITS ("+par+")", 0)
			s.exec(t, "INSERT INTO "+par+" VALUES(1)", 1)
			s.exec(t, "INSERT INTO "+chi+" VALUES(2)", 1)
			s.exec(t, "ALTER TABLE "+par+" ADD COLUMN c serial", 0)

			parentDefault := "nextval('" + par + "_c_seq'::regclass)"
			defaultOf := func(table string) fixture.Step {
				return fixture.Select("SELECT pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid=$1::regclass AND adnum=2", []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{parentDefault}}, table)
			}
			s.run(t, defaultOf(par))
			s.run(t, defaultOf(chi))

			s.run(t, fixture.Select("SELECT c.relname,d.refobjsubid FROM pg_class c JOIN pg_depend d ON d.classid='pg_class'::regclass AND d.objid=c.oid AND d.refclassid='pg_class'::regclass AND d.deptype='a' WHERE c.relkind='S' AND d.refobjid IN ($1::regclass,$2::regclass) ORDER BY c.relname", []string{"relname", "refobjsubid"}, []string{"NAME", "INT4"}, [][]any{{par + "_c_seq", int64(2)}}, par, chi))
			s.run(t, fixture.Select("SELECT d.deptype,d.refobjid::regclass::text AS refname,d.refobjsubid FROM pg_depend d WHERE d.classid='pg_class'::regclass AND d.objid=(SELECT oid FROM pg_class WHERE relname=$1) AND d.refclassid='pg_class'::regclass ORDER BY d.deptype", []string{"deptype", "refname", "refobjsubid"}, []string{"CHAR", "TEXT", "INT4"}, [][]any{{"a", par, int64(2)}}, par+"_c_seq"))
			s.run(t, fixture.Select("SELECT d.deptype,d.refclassid::regclass::text AS refclass,d.refobjid::regclass::text AS refname,d.refobjsubid FROM pg_depend d WHERE d.classid='pg_attrdef'::regclass AND d.objid=(SELECT ad.oid FROM pg_attrdef ad WHERE ad.adrelid=$1::regclass AND ad.adnum=2) ORDER BY d.deptype", []string{"deptype", "refclass", "refname", "refobjsubid"}, []string{"CHAR", "TEXT", "TEXT", "INT4"}, [][]any{
				{"a", "pg_class", chi, int64(2)},
				{"n", "pg_class", par + "_c_seq", int64(0)},
			}, chi))

			s.run(t, fixture.Select("SELECT id,c FROM ONLY "+par+" ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
			s.run(t, fixture.Select("SELECT id,c FROM "+chi+" ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(2)}}))
			s.exec(t, "INSERT INTO "+par+" DEFAULT VALUES", 1)
			s.exec(t, "INSERT INTO "+chi+" DEFAULT VALUES", 1)
			s.run(t, fixture.Select("SELECT c FROM ONLY "+par+" ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
			s.run(t, fixture.Select("SELECT c FROM "+chi+" ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(4)}}))
		})
	})
}

// TestReferenceAlterAddColumnSerialMigrations replays the verbatim migration
// forms on populated simplified tables: a dropped and re-added primary key,
// a dropped primary key replaced by a serial key plus a unique constraint, one
// combined serial/boolean/unique statement, and the UUID primary key form whose
// surrounding DROP DEFAULT still applies.
func TestReferenceAlterAddColumnSerialMigrations(t *testing.T) {
	addColumnSerialBoundaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnSerialBoundaryForEachRoute(t, p, b, func(t *testing.T, s addColumnSerialBoundarySession) {
			t.Run("job_logs", func(t *testing.T) {
				table := "provisioner_job_logs"
				s.exec(t, "DROP TABLE IF EXISTS "+table+" CASCADE", 0)
				s.exec(t, "CREATE TABLE "+table+"(id INT, payload INT)", 0)
				s.exec(t, "INSERT INTO "+table+" VALUES(5,1),(6,2)", 2)
				s.exec(t, "ALTER TABLE "+table+" DROP COLUMN id", 0)
				s.exec(t, "ALTER TABLE "+table+" ADD COLUMN id BIGSERIAL PRIMARY KEY", 0)
				s.run(t, fixture.Select("SELECT id,payload FROM "+table+" ORDER BY payload", []string{"id", "payload"}, []string{"INT8", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}))
				s.run(t, fixture.Select("SELECT count(*),count(DISTINCT id) FROM "+table, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(2)}}))
				s.run(t, addColumnSerialBoundaryConstraints(table,
					[]any{table + "_id_not_null", "n", "NOT NULL id"},
					[]any{table + "_pkey", "p", "PRIMARY KEY (id)"},
				))
			})

			t.Run("resource_metadata", func(t *testing.T) {
				table := "workspace_resource_metadata"
				s.exec(t, "DROP TABLE IF EXISTS "+table+" CASCADE", 0)
				s.exec(t, "CREATE TABLE "+table+"(workspace_resource_id INT, key TEXT, PRIMARY KEY(workspace_resource_id,key))", 0)
				s.exec(t, "INSERT INTO "+table+" VALUES(1,'a'),(2,'b')", 2)
				s.exec(t, "ALTER TABLE "+table+" DROP CONSTRAINT "+table+"_pkey", 0)
				s.exec(t, "ALTER TABLE "+table+" ADD COLUMN id BIGSERIAL PRIMARY KEY", 0)
				s.exec(t, "ALTER TABLE "+table+" ADD CONSTRAINT "+table+"_name UNIQUE(workspace_resource_id, key)", 0)
				s.run(t, fixture.Select("SELECT workspace_resource_id,key,id FROM "+table+" ORDER BY key", []string{"workspace_resource_id", "key", "id"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "a", int64(1)}, {int64(2), "b", int64(2)}}))
				s.run(t, fixture.Select("SELECT count(*),count(DISTINCT id) FROM "+table, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(2)}}))
				s.run(t, fixture.Select("SELECT conname,contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass AND contype IN ('p','u') ORDER BY conname", []string{"conname", "contype", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "TEXT"}, [][]any{
					{table + "_name", "u", "UNIQUE (workspace_resource_id, key)"},
					{table + "_pkey", "p", "PRIMARY KEY (id)"},
				}, table))
			})

			t.Run("workspace_proxies", func(t *testing.T) {
				table := "workspace_proxies"
				s.exec(t, "DROP TABLE IF EXISTS "+table+" CASCADE", 0)
				s.exec(t, "CREATE TABLE "+table+"(k INT)", 0)
				s.exec(t, "INSERT INTO "+table+" VALUES(1),(2)", 2)
				s.exec(t, "ALTER TABLE "+table+" ADD COLUMN region_id serial NOT NULL, ADD COLUMN derp_enabled boolean NOT NULL DEFAULT true, ADD CONSTRAINT "+table+"_region_id_unique UNIQUE (region_id)", 0)
				s.run(t, fixture.Select("SELECT k,region_id,derp_enabled FROM "+table+" ORDER BY k", []string{"k", "region_id", "derp_enabled"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(1), true}, {int64(2), int64(2), true}}))
				s.run(t, fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid=$1::regclass ORDER BY conname", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{
					{table + "_derp_enabled_not_null", "n"},
					{table + "_region_id_not_null", "n"},
					{table + "_region_id_unique", "u"},
				}, table))
			})

			t.Run("uuid_primary_key", func(t *testing.T) {
				table := "workspace_app_audit_sessions"
				s.exec(t, "DROP TABLE IF EXISTS "+table+" CASCADE", 0)
				s.exec(t, "CREATE TABLE "+table+"(kind TEXT)", 0)
				s.exec(t, "INSERT INTO "+table+" VALUES('a'),('b')", 2)
				s.exec(t, "ALTER TABLE "+table+" ADD COLUMN id UUID PRIMARY KEY DEFAULT gen_random_uuid()", 0)
				s.exec(t, "ALTER TABLE "+table+" ALTER COLUMN id DROP DEFAULT", 0)
				s.run(t, fixture.Select("SELECT count(*),count(DISTINCT id) FROM "+table, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(2)}}))
				s.run(t, fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid=$1::regclass AND contype='p'", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{table + "_pkey", "p"}}, table))
				s.run(t, fixture.Select("SELECT count(*) FROM pg_attrdef WHERE adrelid=$1::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, table))
			})
		})
	})
}

// TestReferenceAlterAddColumnSerialFeatureSmoke walks one stream: both repro
// ALTERs, the width aliases, an insert that advances each serial, a failing
// serial CHECK that leaves no sequence, parent/child sharing of one sequence,
// and the combined migration statement form.
func TestReferenceAlterAddColumnSerialFeatureSmoke(t *testing.T) {
	addColumnSerialBoundaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnSerialBoundaryForEachRoute(t, p, b, func(t *testing.T, s addColumnSerialBoundarySession) {
			repro := s.name("ser_smoke")
			s.exec(t, "CREATE TABLE "+repro+"(id int)", 0)
			s.exec(t, "INSERT INTO "+repro+" VALUES(10),(20),(30)", 3)
			s.exec(t, "ALTER TABLE "+repro+" ADD COLUMN region_id serial NOT NULL", 0)
			s.exec(t, "ALTER TABLE "+repro+" ADD COLUMN big_id bigserial", 0)
			s.run(t, fixture.Select("SELECT id,region_id,big_id FROM "+repro+" ORDER BY id", []string{"id", "region_id", "big_id"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(10), int64(1), int64(1)}, {int64(20), int64(2), int64(2)}, {int64(30), int64(3), int64(3)}}))
			s.run(t, fixture.Select("SELECT a.attname,a.attnum,t.typname,a.attnotnull,a.atthasdef FROM pg_attribute a JOIN pg_type t ON t.oid=a.atttypid WHERE a.attrelid=$1::regclass AND a.attnum>0 ORDER BY a.attnum", []string{"attname", "attnum", "typname", "attnotnull", "atthasdef"}, []string{"NAME", "INT2", "NAME", "BOOL", "BOOL"}, [][]any{
				{"id", int64(1), "int4", false, false},
				{"region_id", int64(2), "int4", true, true},
				{"big_id", int64(3), "int8", true, true},
			}, repro))
			s.exec(t, "INSERT INTO "+repro+"(id) VALUES(40)", 1)
			s.run(t, fixture.Select("SELECT id,region_id,big_id FROM "+repro+" ORDER BY id", []string{"id", "region_id", "big_id"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(10), int64(1), int64(1)}, {int64(20), int64(2), int64(2)}, {int64(30), int64(3), int64(3)}, {int64(40), int64(4), int64(4)}}))

			widths := s.name("ser_widths")
			s.exec(t, "CREATE TABLE "+widths+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+widths+" ADD COLUMN d serial2, ADD COLUMN e serial4, ADD COLUMN f serial8", 0)
			s.run(t, fixture.Select("SELECT a.attname,t.typname FROM pg_attribute a JOIN pg_type t ON t.oid=a.atttypid WHERE a.attrelid=$1::regclass AND a.attnum>0 ORDER BY a.attnum", []string{"attname", "typname"}, []string{"NAME", "NAME"}, [][]any{
				{"id", "int4"},
				{"d", "int2"},
				{"e", "int4"},
				{"f", "int8"},
			}, widths))
			s.run(t, fixture.Select("SELECT relname FROM pg_class WHERE relkind='S' AND relname LIKE $1 || '%' ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{{widths + "_d_seq"}, {widths + "_e_seq"}, {widths + "_f_seq"}}, widths))

			bad := s.name("ser_bad")
			s.exec(t, "CREATE TABLE "+bad+"(id INT)", 0)
			s.exec(t, "INSERT INTO "+bad+" VALUES(1),(2)", 2)
			violation := fixture.ControlsReferenceDiagnostic("23514", `check constraint "`+bad+`_b_check" of relation "`+bad+`" is violated by some row`)
			violation.Schema, violation.Table, violation.Constraint = "public", bad, bad+"_b_check"
			s.run(t, fixture.ImplicitError("ALTER TABLE "+bad+" ADD COLUMN b serial CHECK (b > 100)", violation))
			s.run(t, fixture.Select("SELECT count(*) FROM pg_class WHERE relname LIKE $1 || '%'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, bad))

			par, chi := s.name("ser_smoke_par"), s.name("ser_smoke_chi")
			s.exec(t, "CREATE TABLE "+par+"(id INT)", 0)
			s.exec(t, "CREATE TABLE "+chi+"() INHERITS ("+par+")", 0)
			s.exec(t, "INSERT INTO "+par+" VALUES(1)", 1)
			s.exec(t, "INSERT INTO "+chi+" VALUES(2)", 1)
			s.exec(t, "ALTER TABLE "+par+" ADD COLUMN c serial", 0)
			s.run(t, fixture.Select("SELECT count(*) FROM pg_attrdef d JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum WHERE a.attname='c' AND d.adrelid IN ($1::regclass,$2::regclass) AND pg_get_expr(d.adbin,d.adrelid)=$3", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, par, chi, "nextval('"+par+"_c_seq'::regclass)"))
			s.run(t, fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_depend d ON d.classid='pg_class'::regclass AND d.objid=c.oid AND d.refclassid='pg_class'::regclass AND d.deptype='a' WHERE c.relkind='S' AND d.refobjid IN ($1::regclass,$2::regclass)", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, par, chi))
			s.run(t, fixture.Select("SELECT c FROM ONLY "+par, []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}}))
			s.run(t, fixture.Select("SELECT c FROM "+chi, []string{"c"}, []string{"INT4"}, [][]any{{int64(2)}}))

			px := s.name("ser_smoke_px")
			s.exec(t, "CREATE TABLE "+px+"(k INT)", 0)
			s.exec(t, "INSERT INTO "+px+" VALUES(1),(2)", 2)
			s.exec(t, "ALTER TABLE "+px+" ADD COLUMN region_id serial NOT NULL, ADD COLUMN derp_enabled boolean NOT NULL DEFAULT true, ADD CONSTRAINT "+px+"_region_id_unique UNIQUE (region_id)", 0)
			s.run(t, fixture.Select("SELECT k,region_id,derp_enabled FROM "+px+" ORDER BY k", []string{"k", "region_id", "derp_enabled"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(1), true}, {int64(2), int64(2), true}}))
			s.run(t, fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid=$1::regclass ORDER BY conname", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{
				{px + "_derp_enabled_not_null", "n"},
				{px + "_region_id_not_null", "n"},
				{px + "_region_id_unique", "u"},
			}, px))
		})
	})
}
