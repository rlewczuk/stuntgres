package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAlterConstraintControls(t *testing.T) {
	t.Run("invalid_noinherit_emitted_sql", constraintModeInvalidReplay)
	t.Run("invalid_parent_independent_child", constraintModeIndependentChildReplay)
	constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := enforcementRun(t, p, b, c)
		constraintModeGraph(run)
		run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT", 0))
		// Invalid + NO INHERIT is reached by ordinary released SQL, not by a
		// privileged pg_constraint repair. Historical NULL must survive replay.
		for _, q := range []string{"CREATE TABLE invalid_root(a INT)", "INSERT INTO invalid_root VALUES(NULL)", "ALTER TABLE invalid_root ADD CONSTRAINT invalid_nn NOT NULL a NOT VALID", "ALTER TABLE invalid_root ALTER CONSTRAINT invalid_nn NO INHERIT", "CREATE TABLE named_child(a INT CONSTRAINT own_nn NOT NULL) INHERITS(t)", "CREATE TABLE keyed(a INT CONSTRAINT keyed_nn NOT NULL,CONSTRAINT keyed_pk PRIMARY KEY(a))", "ALTER TABLE keyed ALTER CONSTRAINT keyed_nn NO INHERIT"} {
			n := int64(0)
			if strings.HasPrefix(q, "INSERT") {
				n = 1
			}
			run(fixture.Exec(q, n))
		}
		run(fixture.Exec("INSERT INTO named_child VALUES(4)", 1))
		run(fixture.Exec("INSERT INTO keyed VALUES(5)", 1))
		consumer := addConstraintHeld(t, p, b, c, "SELECT a FROM ONLY t ORDER BY a")
		check := func() {
			run(constraintModeNN("t", "nn", true, true, 0, true))
			run(constraintModeNN("child", "nn", false, true, 0, true))
			run(constraintModeNN("grandchild", "nn", false, false, 1, true))
			run(constraintModeNN("invalid_root", "invalid_nn", true, true, 0, false))
			run(constraintModeNN("named_child", "own_nn", false, true, 0, true))
			run(constraintModeNN("keyed", "keyed_nn", true, true, 0, true))
			run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
			run(fixture.Select("SELECT a FROM invalid_root", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
			run(fixture.Select("SELECT a FROM keyed", []string{"a"}, []string{"INT4"}, [][]any{{int64(5)}}))
			validateConstraintHeldRun(t, p, b, consumer, fixture.Select(consumer.SQL, []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, true, consumer.SQL, []string{"a"}, []string{"INT4"}, []bool{false}, 1)
		}
		check()
		if !b.PG {
			dump := addConstraintDump(t, p)
			old, err := c.QueryContext(p.Ctx, consumer.SQL)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			enforcementSameDump(t, p, dump)
			check()
			constraintModeDetachedRows(t, old, false, []any{int64(1)})
			// The replayed parent property controls future inheritance, not just
			// a catalog display row.
			run(fixture.Exec("CREATE TABLE after_restore() INHERITS(t)", 0))
			run(fixture.Exec("INSERT INTO after_restore VALUES(NULL)", 1))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='after_restore'::regclass", []string{"conname"}, []string{"NAME"}, nil))
			run(fixture.Exec("CREATE TABLE invalid_after() INHERITS(invalid_root)", 0))
			run(fixture.Exec("INSERT INTO invalid_after VALUES(NULL)", 1))
			run(fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
			run(fixture.Exec("INSERT INTO sentinel VALUES(91)", 1))
			before := constraintModeSnapshot(t, p, b, c)
			unchanged := func() {
				constraintModeUnchanged(t, p, b, c, before)
				run(fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
			}
			old, err = c.QueryContext(p.Ctx, consumer.SQL)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			bad := append(append([]byte(nil), before.base.dump...), []byte("ALTER TABLE public.t ADD CONSTRAINT late CHECK(missing>0);")...)
			want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)), &want)
			unchanged()
			failure := errors.New("constraint mode late reader failure")
			if err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(bytes.NewReader(before.base.dump), addConstraintFailedReader{failure})); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			unchanged()
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(before.base.dump)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			unchanged()
			constraintModeDetachedRows(t, old, false, []any{int64(1)})
		}
		run(fixture.ErrorDiagnostic("INSERT INTO invalid_root VALUES(NULL)", fixture.PrimaryKeyReferenceNull("invalid_root", "a", "null")))
		run(fixture.ErrorDiagnostic("INSERT INTO child VALUES(NULL)", fixture.PrimaryKeyReferenceNull("child", "a", "null")))
		// Ordinary native SQL is not the privileged Restore dispatcher.
		if !b.PG {
			run(fixture.ErrorDiagnostic("UPDATE pg_constraint SET connoinherit=false WHERE conrelid='t'::regclass", fixture.ControlsReferenceDiagnostic("0A000", "catalog source mutation is unsupported")))
		}
	})
}

// The exact post-data SQL is executed by PostgreSQL and by native Restore's
// isolated admission. Ordinary native declaration syntax is intentionally not
// widened: only Restore receives the per-parse constructor permission.
func constraintModeInvalidReplay(t *testing.T) {
	constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := enforcementRun(t, p, b, c)
		declarations := []string{"CREATE TABLE public.replay_parent(a INT)", "CREATE TABLE public.replay_child() INHERITS(public.replay_parent)", "CREATE TABLE public.replay_grandchild() INHERITS(public.replay_child)", "INSERT INTO public.replay_parent VALUES(NULL)", "INSERT INTO public.replay_child VALUES(NULL)", "INSERT INTO public.replay_grandchild VALUES(NULL)"}
		const attachment = "ALTER TABLE public.replay_parent ADD CONSTRAINT replay_nn NOT NULL a NOT VALID NO INHERIT"
		if b.PG {
			for _, q := range declarations {
				n := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					n = 1
				}
				run(fixture.Exec(q, n))
			}
			run(fixture.Exec(attachment, 0))
		} else {
			script := strings.Join(append(declarations, attachment), ";\n") + ";\n"
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
				t.Fatal(err)
			}
		}
		check := func(observe func(fixture.Step)) {
			observe(constraintModeNN("replay_parent", "replay_nn", true, true, 0, false))
			observe(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='replay_child'::regclass", []string{"conname"}, []string{"NAME"}, nil))
			observe(fixture.Select("SELECT a FROM ONLY replay_parent", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
			observe(fixture.Select("SELECT a FROM ONLY replay_child", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
			observe(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='replay_grandchild'::regclass", []string{"conname"}, []string{"NAME"}, nil))
			observe(fixture.Select("SELECT a FROM ONLY replay_grandchild", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
		}
		check(run)
		if !b.PG {
			dump := addConstraintDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			enforcementSameDump(t, p, dump)
			check(run)
			constraintModeDumpSQLReplay(t, p, dump, check)
		}
		run(fixture.ErrorDiagnostic("INSERT INTO replay_parent VALUES(NULL)", fixture.PrimaryKeyReferenceNull("replay_parent", "a", "null")))
		run(fixture.Exec("INSERT INTO replay_child VALUES(NULL)", 1))
		run(fixture.Exec("INSERT INTO replay_grandchild VALUES(NULL)", 1))
	})
}

func constraintModeDumpSQLReplay(t *testing.T, p *fixture.Pair, dump []byte, check func(func(fixture.Step))) {
	t.Helper()
	if p.Postgres == nil {
		return
	}
	const reset = "DROP SCHEMA public CASCADE; CREATE SCHEMA public"
	defer func() {
		if _, err := p.Postgres.ExecContext(p.Ctx, reset); err != nil {
			t.Error("cleaning actual SQL replay", err)
		}
	}()
	if _, err := p.Postgres.ExecContext(p.Ctx, reset); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
		t.Fatal("actual emitted invalid NO INHERIT dump SQL", err)
	}
	check(func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) })
}

// An independent child NO INHERIT fact must never be temporarily merged by
// replay of its invalid parent. This complements the absent-child-fact case.
func constraintModeIndependentChildReplay(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := enforcementRun(t, p, b, c)
				change := func(q string) {
					if held {
						h := addConstraintHeld(t, p, b, c, q)
						validateConstraintHeldRun(t, p, b, h, fixture.Exec(q, 0))
					} else {
						run(fixture.Exec(q, 0))
					}
				}
				run(fixture.Exec("CREATE TABLE independent_parent(a INT)", 0))
				run(fixture.Exec("INSERT INTO independent_parent VALUES(NULL)", 1))
				run(fixture.Exec("ALTER TABLE independent_parent ADD CONSTRAINT parent_nn NOT NULL a NOT VALID", 0))
				parentID := constraintModeOID(t, p, c, "independent_parent", "parent_nn")
				change("ALTER TABLE independent_parent ALTER CONSTRAINT parent_nn NO INHERIT")
				constraintModeSameOID(t, p, c, "independent_parent", "parent_nn", parentID)
				run(fixture.Exec("CREATE TABLE independent_child(a INT CONSTRAINT own_nn NOT NULL) INHERITS(independent_parent)", 0))
				childID := constraintModeOID(t, p, c, "independent_child", "own_nn")
				change("ALTER TABLE independent_child ALTER CONSTRAINT own_nn NO INHERIT")
				constraintModeSameOID(t, p, c, "independent_child", "own_nn", childID)
				if parentID == childID {
					t.Fatal("independent child shared parent NN identity")
				}
				run(fixture.Exec("INSERT INTO independent_child VALUES(4)", 1))
				run(fixture.Exec("CREATE TABLE independent_grandchild() INHERITS(independent_child)", 0))
				run(fixture.Exec("INSERT INTO independent_grandchild VALUES(NULL)", 1))
				facts := func(observe func(fixture.Step)) {
					observe(constraintModeNN("independent_parent", "parent_nn", true, true, 0, false))
					observe(constraintModeNN("independent_child", "own_nn", true, true, 0, true))
					observe(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='independent_grandchild'::regclass", []string{"conname"}, []string{"NAME"}, nil))
					observe(fixture.Select("SELECT count(*) AS n FROM pg_constraint WHERE contype='n' AND conrelid IN ('independent_parent'::regclass,'independent_child'::regclass,'independent_grandchild'::regclass)", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
					observe(fixture.Select("SELECT (SELECT oid FROM pg_constraint WHERE conrelid='independent_parent'::regclass AND conname='parent_nn')<>(SELECT oid FROM pg_constraint WHERE conrelid='independent_child'::regclass AND conname='own_nn') AS distinct_nn", []string{"distinct_nn"}, []string{"BOOL"}, [][]any{{true}}))
					observe(fixture.Select("SELECT conname,conkey::text AS constraint_keys,conparentid::bigint AS parent_constraint FROM pg_constraint WHERE conrelid IN ('independent_parent'::regclass,'independent_child'::regclass) AND contype='n' ORDER BY conname", []string{"conname", "constraint_keys", "parent_constraint"}, []string{"NAME", "TEXT", "INT8"}, [][]any{{"own_nn", "{1}", int64(0)}, {"parent_nn", "{1}", int64(0)}}))
					for _, name := range []string{"independent_parent", "independent_child"} {
						observe(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid=$1::regclass AND attnum=1", []string{"attname"}, []string{"NAME"}, [][]any{{"a"}}, name))
					}
					observe(fixture.Select("SELECT a FROM ONLY independent_parent", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
					observe(fixture.Select("SELECT a FROM ONLY independent_child", []string{"a"}, []string{"INT4"}, [][]any{{int64(4)}}))
					observe(fixture.Select("SELECT a FROM ONLY independent_grandchild", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
					observe(fixture.Select("SELECT c.relname AS child,p.relname AS parent FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid JOIN pg_class p ON p.oid=i.inhparent WHERE c.relname IN ('independent_child','independent_grandchild') ORDER BY c.relname", []string{"child", "parent"}, []string{"NAME", "NAME"}, [][]any{{"independent_child", "independent_parent"}, {"independent_grandchild", "independent_child"}}))
				}
				// Capture this reconstruction's real identities, never allocator
				// numbers from the source before Restore. Both adapter routes use
				// this same callback for positive IDs and matching-mode stability.
				identities := func(executor fixture.Executor) [2]int64 {
					var ids [2]int64
					for i, selection := range []struct{ table, name string }{{"independent_parent", "parent_nn"}, {"independent_child", "own_nn"}} {
						func() {
							rows, err := executor.QueryContext(p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conrelid=$1::regclass AND conname=$2", selection.table, selection.name)
							if err != nil {
								t.Fatal(err)
							}
							defer rows.Close()
							if !rows.Next() {
								t.Fatal("missing reconstructed NN", selection, rows.Err())
							}
							if err := rows.Scan(&ids[i]); err != nil {
								t.Fatal(err)
							}
							if ids[i] <= 0 {
								t.Fatal("nonpositive reconstructed NN identity", selection, ids[i])
							}
							if rows.Next() || rows.Err() != nil {
								t.Fatal("reconstructed NN identity tail", selection, rows.Err())
							}
						}()
					}
					if ids[0] == ids[1] {
						t.Fatal("reconstructed parent and child share NN identity", ids)
					}
					return ids
				}
				check := func(observe func(fixture.Step), executor fixture.Executor) {
					before := identities(executor)
					facts(observe)
					for _, q := range []string{"ALTER TABLE independent_parent ALTER CONSTRAINT parent_nn NO INHERIT", "ALTER TABLE independent_child ALTER CONSTRAINT own_nn NO INHERIT"} {
						step := fixture.Exec(q, 0)
						if held {
							step = fixture.Prepared(step)
						}
						observe(step)
					}
					if after := identities(executor); after != before {
						t.Fatal("matching reconstructed modes replaced NN identities", before, after)
					}
					facts(observe)
				}
				future := func(observe func(fixture.Step)) {
					observe(fixture.ErrorDiagnostic("INSERT INTO independent_parent VALUES(NULL)", fixture.PrimaryKeyReferenceNull("independent_parent", "a", "null")))
					observe(fixture.ErrorDiagnostic("INSERT INTO independent_child VALUES(NULL)", fixture.PrimaryKeyReferenceNull("independent_child", "a", "null")))
					observe(fixture.Exec("CREATE TABLE independent_parent_fresh() INHERITS(independent_parent)", 0))
					observe(fixture.Exec("CREATE TABLE independent_child_fresh() INHERITS(independent_child)", 0))
					for _, name := range []string{"independent_parent_fresh", "independent_child_fresh"} {
						observe(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid=$1::regclass", []string{"conname"}, []string{"NAME"}, nil, name))
						observe(fixture.Exec("INSERT INTO "+name+" VALUES(NULL)", 1))
						observe(fixture.Select("SELECT a FROM ONLY "+name, []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
					}
					observe(fixture.Exec("INSERT INTO independent_grandchild VALUES(NULL)", 1))
					observe(fixture.Select("SELECT a FROM ONLY independent_parent", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
					observe(fixture.Select("SELECT a FROM ONLY independent_child", []string{"a"}, []string{"INT4"}, [][]any{{int64(4)}}))
					observe(fixture.Select("SELECT a FROM ONLY independent_grandchild", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {nil}}))
				}
				check(run, c)
				if !b.PG {
					dump := addConstraintDump(t, p)
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
						t.Fatal(err)
					}
					enforcementSameDump(t, p, dump)
					check(run, c)
					constraintModeDumpSQLReplay(t, p, dump, func(observe func(fixture.Step)) { check(observe, p.Postgres); future(observe) })
				}
				future(run)
			})
		})
	}
}
