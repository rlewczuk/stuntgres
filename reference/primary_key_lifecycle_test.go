package reference_test

import "testing"

func TestReferencePrimaryKeyLifecycle(t *testing.T) {
	primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(extra INT,a INT,b TEXT,PRIMARY KEY(a,b))", 0))
		run(referenceExec("INSERT INTO t VALUES(0,1,'x')", 1))
		before := primaryKeyReferenceIDs(t, p, b, "t_pkey")
		for _, q := range []string{"ALTER TABLE t ALTER a DROP NOT NULL", "ALTER TABLE t DROP CONSTRAINT t_a_not_null", "ALTER TABLE t DROP CONSTRAINT t_b_not_null CASCADE"} {
			column := "a"
			if q == "ALTER TABLE t DROP CONSTRAINT t_b_not_null CASCADE" {
				column = "b"
			}
			run(referencePrepared(referenceErrorDiagnostic(q, primaryKeyReferenceProtected(column))))
			if after := primaryKeyReferenceIDs(t, p, b, "t_pkey"); after != before {
				t.Fatal("rejected NN drop changed identities", before, after)
			}
			run(referenceSelect("SELECT * FROM t", []string{"extra", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(0), int64(1), "x"}}))
		}
		run(referenceExec("ALTER TABLE t RENAME b TO label", 0))
		run(referenceExec("ALTER TABLE t RENAME TO renamed", 0))
		if after := primaryKeyReferenceIDs(t, p, b, "t_pkey"); after != before {
			t.Fatal("rename changed identities", before, after)
		}
		run(referenceSelect("SELECT pg_get_constraintdef(oid),pg_get_constraintdef(oid,true) FROM pg_constraint WHERE conname='t_pkey'", []string{"pg_get_constraintdef", "pg_get_constraintdef"}, []string{"TEXT", "TEXT"}, [][]any{{"PRIMARY KEY (a, label)", "PRIMARY KEY (a, label)"}}))
		run(referenceExec("ALTER TABLE renamed DROP extra RESTRICT", 0))
		run(referenceErrorDiagnostic("INSERT INTO renamed VALUES(1,'x')", uniqueReferenceDuplicate("renamed", "t_pkey", "Key (a, label)=(1, x) already exists.")))
		run(referenceExec("ALTER INDEX t_pkey RENAME TO current_pk", 0))
		if after := primaryKeyReferenceIDs(t, p, b, "current_pk"); after != before {
			t.Fatal("backing rename changed identities", before, after)
		}
		run(referenceErrorDiagnostic("INSERT INTO renamed VALUES(1,'x')", uniqueReferenceDuplicate("renamed", "current_pk", "Key (a, label)=(1, x) already exists.")))
		for _, suffix := range []string{"", " CASCADE"} {
			s := referenceImplicitError("DROP INDEX current_pk"+suffix, referenceDiagnostic{Code: "2BP01", Message: "cannot drop index current_pk because constraint current_pk on table renamed requires it", Hint: "You can drop constraint current_pk on table renamed instead."})
			s.errorStage = "execute"
			run(referencePrepared(s))
			if after := primaryKeyReferenceIDs(t, p, b, "current_pk"); after != before {
				t.Fatal("rejected backing drop changed identities", before, after)
			}
		}
		run(referenceExec("REINDEX INDEX current_pk", 0))
		if after := primaryKeyReferenceIDs(t, p, b, "current_pk"); after != before {
			t.Fatal("REINDEX changed logical identities", before, after)
		}
		run(referenceExec("ALTER TABLE renamed DROP label RESTRICT", 0))
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='current_pk'", []string{"relname"}, []string{"NAME"}, nil))
		run(primaryKeyReferenceCatalog("renamed", [][]any{{"t_a_not_null", "n", "NOT NULL a", nil}}))
		run(referenceExec("INSERT INTO renamed VALUES(1)", 1))
		run(referenceErrorDiagnostic("INSERT INTO renamed VALUES(NULL)", primaryKeyReferenceNull("renamed", "a", "null")))
		run(referenceExec("DROP TABLE renamed", 0))
		run(referenceSelect("SELECT conname FROM pg_constraint WHERE conname='t_a_not_null' OR conname='current_pk'", []string{"conname"}, []string{"NAME"}, nil))
	})
}

func TestReferencePrimaryKeyDropRetainsNotNull(t *testing.T) {
	for _, order := range []string{"separate", "forward", "reverse"} {
		t.Run(order, func(t *testing.T) {
			primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
				run(referenceExec("CREATE TABLE t(a INT PRIMARY KEY)", 0))
				run(referenceExec("INSERT INTO t VALUES(1)", 1))
				if order == "reverse" {
					run(referenceErrorDiagnostic("ALTER TABLE t ALTER a DROP NOT NULL,DROP CONSTRAINT t_pkey", primaryKeyReferenceProtected("a")))
					run(referenceErrorDiagnostic("INSERT INTO t VALUES(1)", uniqueReferenceDuplicate("t", "t_pkey", "Key (a)=(1) already exists.")))
					run(referenceErrorDiagnostic("INSERT INTO t VALUES(NULL)", primaryKeyReferenceNull("t", "a", "null")))
					run(primaryKeyReferenceCatalog("t", [][]any{{"t_a_not_null", "n", "NOT NULL a", nil}, {"t_pkey", "p", "PRIMARY KEY (a)", nil}}))
					return
				}
				if order == "forward" {
					run(referenceExec("ALTER TABLE t DROP CONSTRAINT t_pkey,ALTER a DROP NOT NULL", 0))
				} else {
					run(referenceExec("ALTER TABLE t DROP CONSTRAINT t_pkey", 0))
					run(primaryKeyReferenceCatalog("t", [][]any{{"t_a_not_null", "n", "NOT NULL a", nil}}))
					run(referenceExec("INSERT INTO t VALUES(1)", 1))
					run(referenceErrorDiagnostic("INSERT INTO t VALUES(NULL)", primaryKeyReferenceNull("t", "a", "null")))
					run(referenceExec("ALTER TABLE t DROP CONSTRAINT t_a_not_null", 0))
				}
				run(referenceExec("INSERT INTO t VALUES(NULL)", 1))
				rows := [][]any{{int64(1)}, {nil}}
				if order == "separate" {
					rows = [][]any{{int64(1)}, {int64(1)}, {nil}}
				}
				run(referenceSelect("SELECT a FROM t ORDER BY a NULLS LAST", []string{"a"}, []string{"INT4"}, rows))
				run(primaryKeyReferenceCatalog("t", nil))
				referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, true, "SELECT a FROM t", []string{"a"}, []string{"INT4"}, []bool{true}, len(rows))
			})
		})
	}
}

func TestReferencePrimaryKeySchemaMove(t *testing.T) {
	uniqueReferenceSchemaCleanup(t, "pk_moved")
	primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(a INT PRIMARY KEY)", 0))
		run(referenceExec("INSERT INTO t VALUES(1)", 1))
		before := primaryKeyReferenceIDs(t, p, b, "t_pkey")
		run(referenceExec("CREATE SCHEMA pk_moved", 0))
		run(referenceExec("CREATE TABLE pk_moved.t_pkey(v INT)", 0))
		run(referenceImplicitError("ALTER TABLE t SET SCHEMA pk_moved", referenceDiagnostic{Code: "42P07", Message: `relation "t_pkey" already exists in schema "pk_moved"`}))
		run(referenceErrorDiagnostic("INSERT INTO t VALUES(1)", uniqueReferenceDuplicate("t", "t_pkey", "Key (a)=(1) already exists.")))
		run(referenceExec("DROP TABLE pk_moved.t_pkey", 0))
		if after := primaryKeyReferenceIDs(t, p, b, "t_pkey"); after != before {
			t.Fatal("failed schema move changed identities", before, after)
		}
		run(referenceExec("ALTER TABLE t SET SCHEMA pk_moved", 0))
		if after := primaryKeyReferenceIDs(t, p, b, "t_pkey"); after != before {
			t.Fatal("schema move changed identities", before, after)
		}
		duplicate := uniqueReferenceDuplicate("t", "t_pkey", "Key (a)=(1) already exists.")
		duplicate.Schema = "pk_moved"
		run(referenceErrorDiagnostic("INSERT INTO pk_moved.t VALUES(1)", duplicate))
		null := primaryKeyReferenceNull("t", "a", "null")
		null.Schema = "pk_moved"
		run(referenceErrorDiagnostic("INSERT INTO pk_moved.t VALUES(NULL)", null))
		run(referenceSelect("SELECT a FROM pk_moved.t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(referenceExec("DROP SCHEMA pk_moved CASCADE", 0))
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='t' OR relname='t_pkey'", []string{"relname"}, []string{"NAME"}, nil))
	})
}

func TestReferencePrimaryKeyIdentityGuardPrecedence(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
				run(referenceExec("CREATE TABLE t(a INT GENERATED BY DEFAULT AS IDENTITY CONSTRAINT nn NOT NULL,PRIMARY KEY(a))", 0))
				run(referenceExec("INSERT INTO t DEFAULT VALUES", 1))
				before := primaryKeyReferenceIDs(t, p, b, "t_pkey")
				var nn, sequence int64
				if err := b.db.QueryRowContext(p.ctx, "SELECT oid FROM pg_constraint WHERE conname='nn' AND contype='n'").Scan(&nn); err != nil || nn <= 0 {
					t.Fatal(nn, err)
				}
				if err := b.db.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='t_a_seq' AND relkind='S'").Scan(&sequence); err != nil || sequence <= 0 {
					t.Fatal(sequence, err)
				}
				identity := controlsReferenceDiagnostic("42601", `column "a" of relation "t" is an identity column`)
				for _, c := range []struct {
					query      string
					diagnostic referenceDiagnostic
				}{
					{"ALTER TABLE t ALTER COLUMN a DROP NOT NULL", identity},
					{"ALTER TABLE t DROP CONSTRAINT nn", primaryKeyReferenceProtected("a")},
				} {
					failure := referenceErrorDiagnostic(c.query, c.diagnostic)
					failure.prepared, failure.errorStage = prepared, "execute"
					run(failure)
					if after := primaryKeyReferenceIDs(t, p, b, "t_pkey"); after != before {
						t.Fatal("failed identity/PK NN drop changed group", before, after)
					}
					run(referenceSelect("SELECT oid::bigint FROM pg_constraint WHERE conname='nn' AND contype='n'", []string{"oid"}, []string{"INT8"}, [][]any{{nn}}))
					run(referenceSelect("SELECT oid::bigint FROM pg_class WHERE relname='t_a_seq' AND relkind='S'", []string{"oid"}, []string{"INT8"}, [][]any{{sequence}}))
					run(referenceSelect("SELECT pg_get_serial_sequence('t','a')", []string{"pg_get_serial_sequence"}, []string{"TEXT"}, [][]any{{"public.t_a_seq"}}))
					run(referenceSelect("SELECT last_value,is_called FROM t_a_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), true}}))
					run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
					referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, prepared, "SELECT a FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 1)
				}
				run(primaryKeyReferenceCatalog("t", [][]any{{"nn", "n", "NOT NULL a", nil}, {"t_pkey", "p", "PRIMARY KEY (a)", nil}}))
				healthy := referenceExec("INSERT INTO t DEFAULT VALUES", 1)
				healthy.prepared = prepared
				run(healthy)
				run(referenceSelect("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				run(referenceSelect("SELECT last_value,is_called FROM t_a_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
			})
		})
	}
}
