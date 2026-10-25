package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

const inheritanceTemporaryReplacementError = "stuntgres: catalog replacement with live temporary table inheritance of persistent parents is unsupported"

func TestReferenceInheritanceTemporaryControls(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT DEFAULT 7 NOT NULL,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("INSERT INTO p VALUES(3)", 1)
		s.exec("CREATE TEMP TABLE tc() INHERITS(p)", 0)
		s.exec("INSERT INTO tc DEFAULT VALUES", 1)
		want := fixture.Select("SELECT * FROM tc", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
		s.run(want)
		fixture.TableDDLMetadata(s.t, s.p.Ctx, s.c, s.b.PG, s.prepared, "SELECT * FROM tc", []string{"a"}, []string{"INT4"}, []bool{false}, 1)
		s.fail("DROP TABLE p RESTRICT", inheritanceDropDiagnostic("p", "tc"))
		if s.b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
			s.t.Fatal(err)
		}
		held, err := s.c.QueryContext(s.p.Ctx, "SELECT * FROM tc")
		if err != nil {
			s.t.Fatal(err)
		}
		defer held.Close()
		stmt, err := s.c.PrepareContext(s.p.Ctx, "INSERT INTO tc DEFAULT VALUES")
		if err != nil {
			s.t.Fatal(err)
		}
		defer stmt.Close()
		for _, replace := range []func() error{
			func() error { return stuntgres.Reset(s.p.Ctx, s.p.Native) },
			func() error { return stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())) },
		} {
			fixture.TransactionOrdinary(s.t, replace(), inheritanceTemporaryReplacementError)
			s.run(want)
			s.fail("DROP TABLE p RESTRICT", inheritanceDropDiagnostic("p", "tc"))
			var after bytes.Buffer
			if err = stuntgres.Dump(s.p.Ctx, s.p.Native, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
				s.t.Fatal("rejected replacement changed persistent catalog", err)
			}
		}
		fixture.TableDDLColumnTypes(s.t, false, mustInheritanceColumnTypes(s.t, held), []string{"a"}, []string{"INT4"}, []bool{false})
		fixture.TransactionRowValues(s.t, held, 7)
		if _, err = stmt.ExecContext(s.p.Ctx); err != nil {
			s.t.Fatal("held prepared insert after rejection", err)
		}
		s.values("SELECT * FROM tc", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}})
		s.exec("SET search_path TO pg_temp,public", 0)
		s.fail("INSERT INTO tc VALUES(NULL)", inheritanceTemporarySQLDiagnostic(s, "23502", `null value in column "a" of relation "tc" violates not-null constraint`, "null", "a", ""))
		s.fail("INSERT INTO tc VALUES(-1)", inheritanceTemporarySQLDiagnostic(s, "23514", `new row for relation "tc" violates check constraint "ck"`, "-1", "", "ck"))
		s.exec("DROP TABLE tc", 0)
		if err = stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
			s.t.Fatal(err)
		}
		if err = stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			s.t.Fatal(err)
		}
		s.values("SELECT * FROM p", []string{"a"}, []string{"INT4"}, [][]any{{int64(3)}})
		s.exec("INSERT INTO p DEFAULT VALUES", 1)
		s.values("SELECT * FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(7)}})
	})
}

func mustInheritanceColumnTypes(t *testing.T, rows *sql.Rows) []*sql.ColumnType {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	return columns
}
func inheritanceTemporarySQLDiagnostic(s *inheritanceCase, code, message, row, column, constraint string) fixture.Diagnostic {
	s.t.Helper()
	// Resolve the actual physical owner's namespace through the existing public
	// TEMP fixture convention, independently of the error under test.
	schema := fixture.TempReferenceNamespace(s.t, &fixture.TempReferenceSession{P: s.p, C: s.c}, false)
	return fixture.Diagnostic{Code: code, Message: message, Detail: "Failing row contains (" + row + ").", Schema: schema, Table: "tc", Column: column, Constraint: constraint}
}

type inheritanceReader struct {
	reader  io.Reader
	before  func()
	started bool
}

func (r *inheritanceReader) Read(p []byte) (int, error) {
	if !r.started {
		r.started = true
		r.before()
	}
	return r.reader.Read(p)
}

func TestReferenceInheritanceTemporaryReaderFence(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT DEFAULT 7)", 0)
		s.exec("INSERT INTO p VALUES(3)", 1)
		if s.b.PG {
			s.exec("CREATE TEMP TABLE tc() INHERITS(p)", 0)
			s.exec("INSERT INTO tc DEFAULT VALUES", 1)
			s.values("SELECT * FROM tc", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
			s.t.Fatal(err)
		}
		reader := &inheritanceReader{reader: bytes.NewReader(dump.Bytes()), before: func() { s.exec("CREATE TEMP TABLE tc() INHERITS(p)", 0); s.exec("INSERT INTO tc DEFAULT VALUES", 1) }}
		fixture.TransactionOrdinary(s.t, stuntgres.Restore(s.p.Ctx, s.p.Native, reader), inheritanceTemporaryReplacementError)
		s.values("SELECT * FROM tc", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
		s.fail("DROP TABLE p RESTRICT", inheritanceDropDiagnostic("p", "tc"))
		s.exec("DROP TABLE tc", 0)
		s.values("SELECT * FROM p", []string{"a"}, []string{"INT4"}, [][]any{{int64(3)}})
	})
}

func TestReferenceInheritanceTemporaryCollision(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TEMP TABLE tp(a INT DEFAULT 7 NOT NULL,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TEMP TABLE tc() INHERITS(tp)", 0)
		s.exec("INSERT INTO tc DEFAULT VALUES", 1)
		s.values("SELECT * FROM tc", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
		if s.b.PG {
			return
		}
		var tempOID int64
		if err := s.c.QueryRowContext(s.p.Ctx, "SELECT 'tp'::regclass::oid").Scan(&tempOID); err != nil {
			s.t.Fatal(err)
		}
		// Two real persistent schemas match the target's table/TOAST namespace
		// allocations. Observe the collision rather than hardcoding identities.
		source := fixture.NewFixture(s.t, false, true)
		for _, q := range []string{"CREATE SCHEMA collision_padding", "CREATE SCHEMA collision_toast_padding", "CREATE TABLE p(a INT DEFAULT 9 NOT NULL,CONSTRAINT positive CHECK(a>0))", "CREATE TABLE c() INHERITS(p)", "INSERT INTO c DEFAULT VALUES"} {
			count := int64(0)
			if q == "INSERT INTO c DEFAULT VALUES" {
				count = 1
			}
			fixture.RunStep(s.t, source.Ctx, source.Memory, source.MemoryRecord, fixture.Exec(q, count))
		}
		var importedOID int64
		if err := source.Memory.QueryRowContext(source.Ctx, "SELECT 'p'::regclass::oid").Scan(&importedOID); err != nil {
			s.t.Fatal(err)
		}
		if importedOID != tempOID {
			s.t.Fatalf("fixture must exercise a real relation collision: source p=%d retained tp=%d", importedOID, tempOID)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(source.Ctx, source.Native, &dump); err != nil {
			s.t.Fatal(err)
		}
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			s.t.Fatal(err)
		}
		s.values("SELECT * FROM c", []string{"a"}, []string{"INT4"}, [][]any{{int64(9)}})
		s.edges("c", "p")
		s.edges("tc", "tp")
		var retainedOID int64
		if err := s.c.QueryRowContext(s.p.Ctx, "SELECT 'tp'::regclass::oid").Scan(&retainedOID); err != nil || retainedOID != tempOID {
			s.t.Fatal("retained TEMP identity changed", retainedOID, tempOID, err)
		}
		s.exec("INSERT INTO tc DEFAULT VALUES", 1)
		s.values("SELECT * FROM tc", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}})
		fixture.TableDDLMetadata(s.t, s.p.Ctx, s.c, false, s.prepared, "SELECT * FROM tc", []string{"a"}, []string{"INT4"}, []bool{false}, 2)
		s.exec("SET search_path TO pg_temp,public", 0)
		s.fail("INSERT INTO tc VALUES(NULL)", inheritanceTemporarySQLDiagnostic(s, "23502", `null value in column "a" of relation "tc" violates not-null constraint`, "null", "a", ""))
		s.fail("INSERT INTO tc VALUES(-1)", inheritanceTemporarySQLDiagnostic(s, "23514", `new row for relation "tc" violates check constraint "ck"`, "-1", "", "ck"))
		s.fail("DROP TABLE p RESTRICT", inheritanceDropDiagnostic("p", "c"))
		if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
			s.t.Fatal(err)
		}
		s.absent("p")
		s.absent("c")
		s.edges("tc", "tp")
		s.values("SELECT * FROM tc", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}})
		fixture.TableDDLMetadata(s.t, s.p.Ctx, s.c, false, s.prepared, "SELECT * FROM tc", []string{"a"}, []string{"INT4"}, []bool{false}, 2)
		s.exec("DROP TABLE tp CASCADE", 0)
		s.absent("tc")
	})
}

func TestReferenceInheritanceRetainedExclusionDefinitionCollision(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TEMP TABLE retained_collision_parent(v INT)", 0)
		s.exec("CREATE TEMP TABLE retained_collision_child(CONSTRAINT retained_collision_ex EXCLUDE USING btree(v WITH =)) INHERITS(retained_collision_parent)", 0)
		s.exec("INSERT INTO retained_collision_child VALUES(9)", 1)
		var exclusionOID, tempParentOID, tempChildOID, tempIndexOID int64
		for query, destination := range map[string]*int64{
			"SELECT oid FROM pg_constraint WHERE conname='retained_collision_ex'": &exclusionOID,
			"SELECT 'retained_collision_parent'::regclass::oid":                   &tempParentOID,
			"SELECT 'retained_collision_child'::regclass::oid":                    &tempChildOID,
			"SELECT 'retained_collision_ex'::regclass::oid":                       &tempIndexOID,
		} {
			if err := s.c.QueryRowContext(s.p.Ctx, query).Scan(destination); err != nil {
				s.t.Fatal(err)
			}
		}
		setup := []fixture.Step{
			fixture.Exec("CREATE TABLE collision_parent(a INT DEFAULT 7 NOT NULL,CONSTRAINT collision_positive CHECK(a>0))", 0),
			fixture.Exec("CREATE TABLE collision_child(CONSTRAINT collision_ex EXCLUDE USING btree(a WITH =)) INHERITS(collision_parent)", 0),
			fixture.Exec("INSERT INTO collision_child DEFAULT VALUES", 1),
		}
		for _, step := range setup {
			s.run(step)
		}
		assertSQL := func() {
			s.values("SELECT * FROM collision_child ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
			s.edges("collision_child", "collision_parent")
			s.fail("INSERT INTO collision_child DEFAULT VALUES", fixture.ExclusionReferenceConflict("collision_child", "collision_ex", "a", "7", "7"))
			s.fail("INSERT INTO collision_child VALUES(-1)", inheritanceCheckDiagnostic("collision_child", "collision_positive", "-1"))
			s.values("SELECT * FROM retained_collision_child", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}})
			s.edges("retained_collision_child", "retained_collision_parent")
			s.run(fixture.Error("INSERT INTO retained_collision_child VALUES(9)", "23P01"))
		}
		assertSQL()
		if !s.b.PG {
			source := fixture.NewFixture(s.t, false, true)
			// One sequence consumes one actual identity. Bound the resource count
			// and observe allocator progress; never pin a bootstrap/constraint OID.
			ready := false
			for i := range 64 {
				name := fmt.Sprintf("exclusion_collision_padding_%d", i)
				fixture.RunStep(s.t, source.Ctx, source.Memory, source.MemoryRecord, fixture.Exec("CREATE SEQUENCE "+name, 0))
				var oid int64
				if err := source.Memory.QueryRowContext(source.Ctx, "SELECT '"+name+"'::regclass::oid").Scan(&oid); err != nil {
					s.t.Fatal(err)
				}
				if oid >= exclusionOID {
					s.t.Fatalf("padding passed retained exclusion identity: sequence=%d exclusion=%d", oid, exclusionOID)
				}
				if oid+1 == exclusionOID {
					ready = true
					break
				}
			}
			if !ready {
				s.t.Fatal("bounded allocation did not reach retained exclusion identity")
			}
			for _, step := range setup {
				fixture.RunStep(s.t, source.Ctx, source.Memory, source.MemoryRecord, step)
			}
			var importedOID int64
			if err := source.Memory.QueryRowContext(source.Ctx, "SELECT 'collision_parent'::regclass::oid").Scan(&importedOID); err != nil {
				s.t.Fatal(err)
			}
			if importedOID != exclusionOID {
				s.t.Fatalf("actual imported descriptor must collide with retained EXCLUDE definition: imported=%d exclusion=%d", importedOID, exclusionOID)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(source.Ctx, source.Native, &dump); err != nil {
				s.t.Fatal(err)
			}
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
			assertSQL()
			var mappedOID int64
			if err := s.c.QueryRowContext(s.p.Ctx, "SELECT 'collision_parent'::regclass::oid").Scan(&mappedOID); err != nil || mappedOID == exclusionOID {
				s.t.Fatal("imported descriptor did not yield to retained exclusion", mappedOID, exclusionOID, err)
			}
			for query, expected := range map[string]int64{
				"SELECT oid FROM pg_constraint WHERE conname='retained_collision_ex'": exclusionOID,
				"SELECT 'retained_collision_parent'::regclass::oid":                   tempParentOID,
				"SELECT 'retained_collision_child'::regclass::oid":                    tempChildOID,
				"SELECT 'retained_collision_ex'::regclass::oid":                       tempIndexOID,
			} {
				var actual int64
				if err := s.c.QueryRowContext(s.p.Ctx, query).Scan(&actual); err != nil || actual != expected {
					s.t.Fatal("retained TEMP identity changed", query, actual, expected, err)
				}
			}
		}
		// A replacement that installs duplicate definition identities can still
		// serve old rows. Require usable ordinary catalog creation and real DML.
		s.exec("CREATE TABLE collision_health(v INT DEFAULT 42 NOT NULL CHECK(v>0))", 0)
		s.exec("INSERT INTO collision_health DEFAULT VALUES", 1)
		s.values("SELECT * FROM collision_health", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		s.exec("INSERT INTO collision_child VALUES(8)", 1)
		s.values("SELECT * FROM collision_child ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}})
		s.exec("INSERT INTO retained_collision_child VALUES(10)", 1)
		s.values("SELECT * FROM retained_collision_child ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(10)}})
	})
}
