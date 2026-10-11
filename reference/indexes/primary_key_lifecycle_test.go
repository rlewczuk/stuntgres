package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePrimaryKeyLifecycle(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(extra INT,a INT,b TEXT,PRIMARY KEY(a,b))", 0))
		run(fixture.Exec("INSERT INTO t VALUES(0,1,'x')", 1))
		before := fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey")
		for _, q := range []string{"ALTER TABLE t ALTER a DROP NOT NULL", "ALTER TABLE t DROP CONSTRAINT t_a_not_null", "ALTER TABLE t DROP CONSTRAINT t_b_not_null CASCADE"} {
			column := "a"
			if q == "ALTER TABLE t DROP CONSTRAINT t_b_not_null CASCADE" {
				column = "b"
			}
			run(fixture.Prepared(fixture.ErrorDiagnostic(q, fixture.PrimaryKeyReferenceProtected(column))))
			if after := fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey"); after != before {
				t.Fatal("rejected NN drop changed identities", before, after)
			}
			run(fixture.Select("SELECT * FROM t", []string{"extra", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(0), int64(1), "x"}}))
		}
		run(fixture.Exec("ALTER TABLE t RENAME b TO label", 0))
		run(fixture.Exec("ALTER TABLE t RENAME TO renamed", 0))
		if after := fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey"); after != before {
			t.Fatal("rename changed identities", before, after)
		}
		run(fixture.Select("SELECT pg_get_constraintdef(oid),pg_get_constraintdef(oid,true) FROM pg_constraint WHERE conname='t_pkey'", []string{"pg_get_constraintdef", "pg_get_constraintdef"}, []string{"TEXT", "TEXT"}, [][]any{{"PRIMARY KEY (a, label)", "PRIMARY KEY (a, label)"}}))
		run(fixture.Exec("ALTER TABLE renamed DROP extra RESTRICT", 0))
		run(fixture.ErrorDiagnostic("INSERT INTO renamed VALUES(1,'x')", fixture.UniqueReferenceDuplicate("renamed", "t_pkey", "Key (a, label)=(1, x) already exists.")))
		run(fixture.Exec("ALTER INDEX t_pkey RENAME TO current_pk", 0))
		if after := fixture.PrimaryKeyReferenceIDs(t, p, b, "current_pk"); after != before {
			t.Fatal("backing rename changed identities", before, after)
		}
		run(fixture.ErrorDiagnostic("INSERT INTO renamed VALUES(1,'x')", fixture.UniqueReferenceDuplicate("renamed", "current_pk", "Key (a, label)=(1, x) already exists.")))
		for _, suffix := range []string{"", " CASCADE"} {
			s := fixture.ImplicitError("DROP INDEX current_pk"+suffix, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop index current_pk because constraint current_pk on table renamed requires it", Hint: "You can drop constraint current_pk on table renamed instead."})
			s.ErrorStage = "execute"
			run(fixture.Prepared(s))
			if after := fixture.PrimaryKeyReferenceIDs(t, p, b, "current_pk"); after != before {
				t.Fatal("rejected backing drop changed identities", before, after)
			}
		}
		run(fixture.Exec("REINDEX INDEX current_pk", 0))
		if after := fixture.PrimaryKeyReferenceIDs(t, p, b, "current_pk"); after != before {
			t.Fatal("REINDEX changed logical identities", before, after)
		}
		run(fixture.Exec("ALTER TABLE renamed DROP label RESTRICT", 0))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='current_pk'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.PrimaryKeyReferenceCatalog("renamed", [][]any{{"t_a_not_null", "n", "NOT NULL a", nil}}))
		run(fixture.Exec("INSERT INTO renamed VALUES(1)", 1))
		run(fixture.ErrorDiagnostic("INSERT INTO renamed VALUES(NULL)", fixture.PrimaryKeyReferenceNull("renamed", "a", "null")))
		run(fixture.Exec("DROP TABLE renamed", 0))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='t_a_not_null' OR conname='current_pk'", []string{"conname"}, []string{"NAME"}, nil))
	})
}

func TestReferencePrimaryKeyDropRetainsNotNull(t *testing.T) {
	for _, order := range []string{"separate", "forward", "reverse"} {
		t.Run(order, func(t *testing.T) {
			fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(a INT PRIMARY KEY)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				if order == "reverse" {
					run(fixture.ErrorDiagnostic("ALTER TABLE t ALTER a DROP NOT NULL,DROP CONSTRAINT t_pkey", fixture.PrimaryKeyReferenceProtected("a")))
					run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a)=(1) already exists.")))
					run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(NULL)", fixture.PrimaryKeyReferenceNull("t", "a", "null")))
					run(fixture.PrimaryKeyReferenceCatalog("t", [][]any{{"t_a_not_null", "n", "NOT NULL a", nil}, {"t_pkey", "p", "PRIMARY KEY (a)", nil}}))
					return
				}
				if order == "forward" {
					run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT t_pkey,ALTER a DROP NOT NULL", 0))
				} else {
					run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT t_pkey", 0))
					run(fixture.PrimaryKeyReferenceCatalog("t", [][]any{{"t_a_not_null", "n", "NOT NULL a", nil}}))
					run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
					run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(NULL)", fixture.PrimaryKeyReferenceNull("t", "a", "null")))
					run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT t_a_not_null", 0))
				}
				run(fixture.Exec("INSERT INTO t VALUES(NULL)", 1))
				rows := [][]any{{int64(1)}, {nil}}
				if order == "separate" {
					rows = [][]any{{int64(1)}, {int64(1)}, {nil}}
				}
				run(fixture.Select("SELECT a FROM t ORDER BY a NULLS LAST", []string{"a"}, []string{"INT4"}, rows))
				run(fixture.PrimaryKeyReferenceCatalog("t", nil))
				fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, true, "SELECT a FROM t", []string{"a"}, []string{"INT4"}, []bool{true}, len(rows))
			})
		})
	}
}

func TestReferencePrimaryKeySchemaMove(t *testing.T) {
	fixture.UniqueReferenceSchemaCleanup(t, "pk_moved")
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(a INT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		before := fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey")
		run(fixture.Exec("CREATE SCHEMA pk_moved", 0))
		run(fixture.Exec("CREATE TABLE pk_moved.t_pkey(v INT)", 0))
		run(fixture.ImplicitError("ALTER TABLE t SET SCHEMA pk_moved", fixture.Diagnostic{Code: "42P07", Message: `relation "t_pkey" already exists in schema "pk_moved"`}))
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a)=(1) already exists.")))
		run(fixture.Exec("DROP TABLE pk_moved.t_pkey", 0))
		if after := fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey"); after != before {
			t.Fatal("failed schema move changed identities", before, after)
		}
		run(fixture.Exec("ALTER TABLE t SET SCHEMA pk_moved", 0))
		if after := fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey"); after != before {
			t.Fatal("schema move changed identities", before, after)
		}
		duplicate := fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a)=(1) already exists.")
		duplicate.Schema = "pk_moved"
		run(fixture.ErrorDiagnostic("INSERT INTO pk_moved.t VALUES(1)", duplicate))
		null := fixture.PrimaryKeyReferenceNull("t", "a", "null")
		null.Schema = "pk_moved"
		run(fixture.ErrorDiagnostic("INSERT INTO pk_moved.t VALUES(NULL)", null))
		run(fixture.Select("SELECT a FROM pk_moved.t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Exec("DROP SCHEMA pk_moved CASCADE", 0))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='t' OR relname='t_pkey'", []string{"relname"}, []string{"NAME"}, nil))
	})
}

func TestReferencePrimaryKeyIdentityGuardPrecedence(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(a INT GENERATED BY DEFAULT AS IDENTITY CONSTRAINT nn NOT NULL,PRIMARY KEY(a))", 0))
				run(fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
				before := fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey")
				var nn, sequence int64
				if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conname='nn' AND contype='n'").Scan(&nn); err != nil || nn <= 0 {
					t.Fatal(nn, err)
				}
				if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='t_a_seq' AND relkind='S'").Scan(&sequence); err != nil || sequence <= 0 {
					t.Fatal(sequence, err)
				}
				identity := fixture.ControlsReferenceDiagnostic("42601", `column "a" of relation "t" is an identity column`)
				for _, c := range []struct {
					query      string
					diagnostic fixture.Diagnostic
				}{
					{"ALTER TABLE t ALTER COLUMN a DROP NOT NULL", identity},
					{"ALTER TABLE t DROP CONSTRAINT nn", fixture.PrimaryKeyReferenceProtected("a")},
				} {
					failure := fixture.ErrorDiagnostic(c.query, c.diagnostic)
					failure.Prepared, failure.ErrorStage = prepared, "execute"
					run(failure)
					if after := fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey"); after != before {
						t.Fatal("failed identity/PK NN drop changed group", before, after)
					}
					run(fixture.Select("SELECT oid::bigint FROM pg_constraint WHERE conname='nn' AND contype='n'", []string{"oid"}, []string{"INT8"}, [][]any{{nn}}))
					run(fixture.Select("SELECT oid::bigint FROM pg_class WHERE relname='t_a_seq' AND relkind='S'", []string{"oid"}, []string{"INT8"}, [][]any{{sequence}}))
					run(fixture.Select("SELECT pg_get_serial_sequence('t','a')", []string{"pg_get_serial_sequence"}, []string{"TEXT"}, [][]any{{"public.t_a_seq"}}))
					run(fixture.Select("SELECT last_value,is_called FROM t_a_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), true}}))
					run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
					fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT a FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 1)
				}
				run(fixture.PrimaryKeyReferenceCatalog("t", [][]any{{"nn", "n", "NOT NULL a", nil}, {"t_pkey", "p", "PRIMARY KEY (a)", nil}}))
				healthy := fixture.Exec("INSERT INTO t DEFAULT VALUES", 1)
				healthy.Prepared = prepared
				run(healthy)
				run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				run(fixture.Select("SELECT last_value,is_called FROM t_a_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
			})
		})
	}
}
