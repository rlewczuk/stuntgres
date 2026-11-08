package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Inline PRIMARY KEY on ALTER TABLE ADD COLUMN publishes the same catalog
// objects as PostgreSQL 18.6: the column becomes the table key with implicit
// NOT NULL, the generated or spelled name, and the same-column UNIQUE/PRIMARY
// KEY dedup and name transfer. Every expectation here is the pinned 18.6
// oracle capture, never native output.
type addColumnPKNames func(string) string

type addColumnPKRoute struct {
	name, suffix string
	prepared     bool
}

var addColumnPKRoutes = []addColumnPKRoute{
	{name: "direct"},
	{name: "prepared", suffix: "_p", prepared: true},
}

// addColumnPKSession groups one route's notice-capable session: notices,
// prepared steps and TableDDLMetadata all observe the same connection.
type addColumnPKSession struct {
	p        *fixture.Pair
	conn     *sql.Conn
	recorder *fixture.Recorder
	prepared bool
	name     addColumnPKNames
	notices  *[]fixture.Diagnostic
}

func (s addColumnPKSession) run(t *testing.T, step fixture.Step) {
	t.Helper()
	if s.prepared {
		step = fixture.Prepared(step)
	}
	fixture.RunStep(t, s.p.Ctx, s.conn, s.recorder, step)
}

func (s addColumnPKSession) exec(t *testing.T, query string, affected int64) {
	t.Helper()
	s.run(t, fixture.Exec(query, affected))
}

// addColumnPKForEachRoute runs one backend body for each route; route-suffixed
// names let both routes share one schema.
func addColumnPKForEachRoute(t *testing.T, p *fixture.Pair, b fixture.Backend, body func(t *testing.T, s addColumnPKSession)) {
	t.Helper()
	for _, route := range addColumnPKRoutes {
		t.Run(route.name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			body(t, addColumnPKSession{
				p: p, conn: conn, recorder: recorder, prepared: route.prepared,
				name: func(base string) string { return base + route.suffix }, notices: &notices,
			})
		})
	}
}

func addColumnPKConstraints(table string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT conname,contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass AND contype IN ('p','u') ORDER BY conname", []string{"conname", "contype", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "TEXT"}, rows, table)
}

func addColumnPKAttributeNames(table string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 ORDER BY attnum", []string{"attname"}, []string{"NAME"}, rows, table)
}

func TestReferenceAlterAddColumnPrimaryKeyCore(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnPKForEachRoute(t, p, b, func(t *testing.T, s addColumnPKSession) {
			table := s.name("pk_core")
			pk := table + "_pkey"
			s.exec(t, "CREATE TABLE "+table+"(id INT)", 0)
			// The implicit NOT NULL means a populated table without a DEFAULT
			// cannot gain the key, so the added column is created empty first.
			s.exec(t, "ALTER TABLE "+table+" ADD COLUMN k INT NOT NULL PRIMARY KEY", 0)
			s.run(t, fixture.PrimaryKeyReferenceCatalog(table, [][]any{
				{table + "_k_not_null", "n", "NOT NULL k", nil},
				{pk, "p", "PRIMARY KEY (k)", nil},
			}))
			s.run(t, fixture.Select("SELECT attname,attnum,attnotnull,atthasdef FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attnum", "attnotnull", "atthasdef"}, []string{"NAME", "INT2", "BOOL", "BOOL"}, [][]any{
				{"id", int64(1), false, false},
				{"k", int64(2), true, false},
			}, table))
			s.run(t, fixture.Select("SELECT i.indisprimary,i.indisunique,i.indimmediate,i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE c.relname=$1", []string{"indisprimary", "indisunique", "indimmediate", "indisvalid"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}, pk))
			s.run(t, fixture.Select("SELECT relkind FROM pg_class WHERE relname=$1", []string{"relkind"}, []string{"CHAR"}, [][]any{{"i"}}, pk))
			s.run(t, fixture.Select("SELECT indexdef FROM pg_indexes WHERE tablename=$1", []string{"indexdef"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX " + pk + " ON public." + table + " USING btree (k)"}}, table))
			s.exec(t, "INSERT INTO "+table+" VALUES(1,10),(2,20)", 2)
			s.run(t, fixture.Select("SELECT id,k FROM "+table+" ORDER BY id", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
			fixture.TableDDLMetadata(t, p.Ctx, s.conn, b.PG, s.prepared, "SELECT * FROM "+table+" ORDER BY id", []string{"id", "k"}, []string{"INT4", "INT4"}, []bool{true, false}, 2)
			s.run(t, fixture.ErrorDiagnostic("INSERT INTO "+table+" VALUES(3,10)", fixture.UniqueReferenceDuplicate(table, pk, "Key (k)=(10) already exists.")))
			s.run(t, fixture.ErrorDiagnostic("INSERT INTO "+table+" VALUES(4,NULL)", fixture.PrimaryKeyReferenceNull(table, "k", "4, null")))
			s.run(t, fixture.Select("SELECT id,k FROM "+table+" ORDER BY id", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))

			named := s.name("named")
			namedPK := s.name("my_pk")
			s.exec(t, "CREATE TABLE "+named+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+named+" ADD COLUMN k INT CONSTRAINT "+namedPK+" PRIMARY KEY", 0)
			s.run(t, fixture.PrimaryKeyReferenceCatalog(named, [][]any{
				{namedPK, "p", "PRIMARY KEY (k)", nil},
				{named + "_k_not_null", "n", "NOT NULL k", nil},
			}))
			s.exec(t, "INSERT INTO "+named+" VALUES(1,5)", 1)
			s.run(t, fixture.ErrorDiagnostic("INSERT INTO "+named+" VALUES(2,5)", fixture.UniqueReferenceDuplicate(named, namedPK, "Key (k)=(5) already exists.")))

			deferred := s.name("defer")
			s.exec(t, "CREATE TABLE "+deferred+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+deferred+" ADD COLUMN k INT PRIMARY KEY DEFERRABLE INITIALLY DEFERRED", 0)
			s.run(t, fixture.Select("SELECT conname,contype,condeferrable,condeferred FROM pg_constraint WHERE conrelid=$1::regclass ORDER BY conname", []string{"conname", "contype", "condeferrable", "condeferred"}, []string{"NAME", "CHAR", "BOOL", "BOOL"}, [][]any{
				{deferred + "_k_not_null", "n", false, false},
				{deferred + "_pkey", "p", true, true},
			}, deferred))
			s.run(t, fixture.Select("SELECT i.indisprimary,i.indisunique,i.indimmediate,i.indisvalid FROM pg_index i JOIN pg_constraint c ON c.conindid=i.indexrelid WHERE c.conname=$1", []string{"indisprimary", "indisunique", "indimmediate", "indisvalid"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false, true}}, deferred+"_pkey"))

			pkBoth := s.name("pk_both")
			s.exec(t, "CREATE TABLE "+pkBoth+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+pkBoth+" ADD COLUMN k INT PRIMARY KEY NOT NULL", 0)
			s.run(t, fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid=$1::regclass AND contype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, pkBoth))
			pkBothNamed := s.name("pk_bothnamed")
			s.exec(t, "CREATE TABLE "+pkBothNamed+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+pkBothNamed+" ADD COLUMN k INT CONSTRAINT "+s.name("pk")+" PRIMARY KEY CONSTRAINT "+s.name("nn")+" NOT NULL", 0)
			s.run(t, fixture.PrimaryKeyReferenceCatalog(pkBothNamed, [][]any{
				{s.name("nn"), "n", "NOT NULL k", nil},
				{s.name("pk"), "p", "PRIMARY KEY (k)", nil},
			}))

			clauses := s.name("clauses")
			s.exec(t, "CREATE TABLE "+clauses+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+clauses+" ADD COLUMN k INT PRIMARY KEY DEFAULT 1 CHECK(k>0)", 0)
			s.run(t, addColumnPKDefinitions(clauses,
				[]any{clauses + "_k_check", "c", "CHECK ((k > 0))"},
				[]any{clauses + "_k_not_null", "n", "NOT NULL k"},
				[]any{clauses + "_pkey", "p", "PRIMARY KEY (k)"},
			))
			s.run(t, fixture.Select("SELECT count(*) FROM pg_attrdef WHERE adrelid=$1::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, clauses))
			s.exec(t, "INSERT INTO "+clauses+"(id) VALUES(99)", 1)
			s.run(t, fixture.Select("SELECT k FROM "+clauses+" WHERE id=99", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))

			uuid := s.name("uuid")
			s.exec(t, "CREATE TABLE "+uuid+"(id INT)", 0)
			s.exec(t, "INSERT INTO "+uuid+" VALUES(1),(2)", 2)
			s.exec(t, "ALTER TABLE "+uuid+" ADD COLUMN k2 UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid()", 0)
			s.run(t, fixture.PrimaryKeyReferenceCatalog(uuid, [][]any{
				{uuid + "_k2_not_null", "n", "NOT NULL k2", nil},
				{uuid + "_pkey", "p", "PRIMARY KEY (k2)", nil},
			}))
			s.run(t, fixture.Select("SELECT count(*),count(DISTINCT k2) FROM "+uuid, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(2)}}))
			s.run(t, fixture.Select("SELECT count(*) FROM "+uuid+" WHERE k2 IS NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			s.exec(t, "ALTER TABLE "+uuid+" ALTER COLUMN k2 DROP DEFAULT", 0)
			s.run(t, fixture.Select("SELECT count(*) FROM pg_attrdef WHERE adrelid=$1::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, uuid))
			s.exec(t, "INSERT INTO "+uuid+"(id,k2) VALUES(3,'11111111-1111-1111-1111-111111111111')", 1)
		})
	})
}

func TestReferenceAlterAddColumnPrimaryKeyDedup(t *testing.T) {
	type dedupCase struct {
		name, tail string
	}
	cases := []dedupCase{
		{name: "unique_first", tail: "UNIQUE PRIMARY KEY"},
		{name: "transfer", tail: "CONSTRAINT u UNIQUE PRIMARY KEY"},
		{name: "explicit_pk", tail: "UNIQUE CONSTRAINT p PRIMARY KEY"},
		{name: "both_explicit", tail: "CONSTRAINT u UNIQUE CONSTRAINT p PRIMARY KEY"},
		{name: "nnd_after", tail: "UNIQUE NULLS NOT DISTINCT PRIMARY KEY"},
		{name: "nnd_before", tail: "PRIMARY KEY UNIQUE NULLS NOT DISTINCT"},
		{name: "unique_deferrable", tail: "UNIQUE DEFERRABLE PRIMARY KEY"},
		{name: "pk_deferrable", tail: "UNIQUE PRIMARY KEY DEFERRABLE"},
		{name: "unique_deferred", tail: "UNIQUE INITIALLY DEFERRED PRIMARY KEY"},
	}
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnPKForEachRoute(t, p, b, func(t *testing.T, s addColumnPKSession) {
			for n, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					table := s.name("dd" + string(rune('1'+n)))
					s.exec(t, "CREATE TABLE "+table+"(id INT)", 0)
					t.Cleanup(func() {
						if _, err := s.conn.ExecContext(p.Ctx, "DROP TABLE IF EXISTS "+table); err != nil {
							t.Error(err)
						}
					})
					s.exec(t, "ALTER TABLE "+table+" ADD COLUMN k INT "+tc.tail, 0)
					switch n {
					case 0:
						s.run(t, addColumnPKConstraints(table, []any{table + "_pkey", "p", "PRIMARY KEY (k)"}))
					case 1:
						s.run(t, addColumnPKConstraints(table, []any{"u", "p", "PRIMARY KEY (k)"}))
					case 2, 3:
						s.run(t, addColumnPKConstraints(table, []any{"p", "p", "PRIMARY KEY (k)"}))
					case 4, 5:
						s.run(t, addColumnPKConstraints(table,
							[]any{table + "_k_key", "u", "UNIQUE NULLS NOT DISTINCT (k)"},
							[]any{table + "_pkey", "p", "PRIMARY KEY (k)"},
						))
					case 6:
						s.run(t, fixture.Select("SELECT conname,contype,condeferrable,condeferred FROM pg_constraint WHERE conrelid=$1::regclass AND contype IN ('p','u') ORDER BY conname", []string{"conname", "contype", "condeferrable", "condeferred"}, []string{"NAME", "CHAR", "BOOL", "BOOL"}, [][]any{
							{table + "_k_key", "u", true, false},
							{table + "_pkey", "p", false, false},
						}, table))
					case 7:
						s.run(t, fixture.Select("SELECT conname,contype,condeferrable,condeferred FROM pg_constraint WHERE conrelid=$1::regclass AND contype IN ('p','u') ORDER BY conname", []string{"conname", "contype", "condeferrable", "condeferred"}, []string{"NAME", "CHAR", "BOOL", "BOOL"}, [][]any{
							{table + "_k_key", "u", false, false},
							{table + "_pkey", "p", true, false},
						}, table))
					case 8:
						s.run(t, fixture.Select("SELECT conname,contype,condeferrable,condeferred FROM pg_constraint WHERE conrelid=$1::regclass AND contype IN ('p','u') ORDER BY conname", []string{"conname", "contype", "condeferrable", "condeferred"}, []string{"NAME", "CHAR", "BOOL", "BOOL"}, [][]any{
							{table + "_k_key", "u", true, true},
							{table + "_pkey", "p", false, false},
						}, table))
					}
					kept := []string{table + "_pkey"}
					duplicate := table + "_pkey"
					switch n {
					case 1:
						kept, duplicate = []string{"u"}, "u"
					case 2, 3:
						kept, duplicate = []string{"p"}, "p"
					case 4, 5, 6, 7, 8:
						kept = []string{table + "_k_key", table + "_pkey"}
					}
					if n == 7 {
						// The deferrable primary key is not checked at insert time,
						// so the still-immediate UNIQUE index reports the duplicate.
						duplicate = table + "_k_key"
					}
					indexes := make([][]any, len(kept))
					quoted := make([]string, len(kept))
					for i, name := range kept {
						indexes[i] = []any{name}
						quoted[i] = "'" + name + "'"
					}
					s.run(t, fixture.Select("SELECT relname FROM pg_class WHERE relname IN ("+strings.Join(quoted, ",")+") ORDER BY relname", []string{"relname"}, []string{"NAME"}, indexes))
					for _, name := range kept {
						s.run(t, fixture.Select("SELECT relkind FROM pg_class WHERE relname=$1", []string{"relkind"}, []string{"CHAR"}, [][]any{{"i"}}, name))
					}
					s.exec(t, "INSERT INTO "+table+" VALUES(1,1),(2,2)", 2)
					// When both indexes survive the immediate one reports the
					// duplicate: the primary key except in the deferrable-PK case.
					s.run(t, fixture.ErrorDiagnostic("INSERT INTO "+table+" VALUES(3,1)", fixture.UniqueReferenceDuplicate(table, duplicate, "Key (k)=(1) already exists.")))
				})
			}
		})
	})
}

func TestReferenceAlterAddColumnPrimaryKeySkip(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnPKForEachRoute(t, p, b, func(t *testing.T, s addColumnPKSession) {
			table := s.name("skip1")
			notice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42701", Message: `column "b" of relation "` + table + `" already exists, skipping`}
			assertNotices := func() {
				t.Helper()
				if len(*s.notices) != 1 || (*s.notices)[0] != notice {
					t.Fatalf("notices %#v, want one %#v", *s.notices, notice)
				}
				*s.notices = nil
			}
			s.exec(t, "CREATE TABLE "+table+"(id INT,b INT)", 0)
			s.exec(t, "INSERT INTO "+table+" VALUES(1,5)", 1)
			s.exec(t, "ALTER TABLE "+table+" ADD COLUMN IF NOT EXISTS b INT NOT NULL PRIMARY KEY", 0)
			assertNotices()
			s.run(t, fixture.Select("SELECT attname,attnotnull FROM pg_attribute WHERE attrelid=$1::regclass AND attname='b'", []string{"attname", "attnotnull"}, []string{"NAME", "BOOL"}, [][]any{{"b", false}}, table))
			s.run(t, fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid=$1::regclass", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, nil, table))
			s.run(t, fixture.Select("SELECT * FROM "+table, []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(5)}}))
			s.exec(t, "ALTER TABLE "+table+" ADD COLUMN IF NOT EXISTS b nonexistent_type PRIMARY KEY", 0)
			assertNotices()
			s.exec(t, "ALTER TABLE "+table+" ADD COLUMN k INT PRIMARY KEY DEFAULT 7", 0)
			s.run(t, fixture.PrimaryKeyReferenceCatalog(table, [][]any{
				{table + "_k_not_null", "n", "NOT NULL k", nil},
				{table + "_pkey", "p", "PRIMARY KEY (k)", nil},
			}))
			s.run(t, fixture.Select("SELECT id,b,k FROM "+table, []string{"id", "b", "k"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(5), int64(7)}}))
		})
	})
}

func TestReferenceAlterAddColumnPrimaryKeyMigrations(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnPKForEachRoute(t, p, b, func(t *testing.T, s addColumnPKSession) {
			files := s.name("files")
			s.exec(t, "CREATE TABLE "+files+"(hash TEXT NOT NULL,created_by INT NOT NULL,PRIMARY KEY(hash))", 0)
			s.exec(t, "INSERT INTO "+files+" VALUES('a',1),('b',2)", 2)
			s.exec(t, "ALTER TABLE "+files+" DROP CONSTRAINT "+files+"_pkey", 0)
			s.exec(t, "ALTER TABLE "+files+" ADD COLUMN id uuid NOT NULL PRIMARY KEY DEFAULT gen_random_uuid()", 0)
			s.exec(t, "ALTER TABLE "+files+" ADD UNIQUE(hash,created_by)", 0)
			s.run(t, addColumnPKConstraints(files,
				[]any{files + "_hash_created_by_key", "u", "UNIQUE (hash, created_by)"},
				[]any{files + "_pkey", "p", "PRIMARY KEY (id)"},
			))
			s.run(t, fixture.Select("SELECT count(*),count(DISTINCT id) FROM "+files, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(2)}}))
			s.run(t, fixture.Select("SELECT count(*) FROM "+files+" WHERE id IS NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			s.exec(t, "INSERT INTO "+files+"(hash,created_by) VALUES('c',3)", 1)
			s.run(t, fixture.Select("SELECT count(*),count(DISTINCT id) FROM "+files, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}}))

			audit := s.name("workspace_app_audit_sessions")
			s.exec(t, "CREATE TABLE "+audit+"(kind TEXT)", 0)
			s.exec(t, "INSERT INTO "+audit+" VALUES('a'),('b')", 2)
			s.exec(t, "ALTER TABLE "+audit+" ADD COLUMN id UUID PRIMARY KEY DEFAULT gen_random_uuid()", 0)
			s.exec(t, "ALTER TABLE "+audit+" ALTER COLUMN id DROP DEFAULT", 0)
			s.run(t, fixture.Select("SELECT count(*),count(DISTINCT id) FROM "+audit, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(2)}}))
			s.run(t, fixture.Select("SELECT count(*) FROM "+audit+" WHERE id IS NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			s.run(t, fixture.Select("SELECT count(*) FROM pg_attrdef WHERE adrelid=$1::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, audit))
			s.run(t, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid=$1::regclass AND contype='p'", []string{"conname"}, []string{"NAME"}, [][]any{{audit + "_pkey"}}, audit))
			s.exec(t, "INSERT INTO "+audit+"(kind,id) VALUES('c','11111111-1111-1111-1111-111111111111')", 1)
			s.run(t, fixture.Select("SELECT count(*),count(DISTINCT id) FROM "+audit, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}}))
		})
	})
}
