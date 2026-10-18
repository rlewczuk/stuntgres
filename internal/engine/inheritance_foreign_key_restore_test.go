package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"maps"
	"reflect"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestInheritanceForeignKeyForwardReplay(t *testing.T) {
	for _, key := range []string{"UNIQUE", "PRIMARY KEY"} {
		t.Run(key, func(t *testing.T) {
			e := New()
			for _, sql := range []string{
				"CREATE TABLE support(k INT UNIQUE)",
				"INSERT INTO support VALUES(1)",
				"CREATE TABLE p(a INT,CONSTRAINT ordinary_fk FOREIGN KEY(a) REFERENCES support(k))",
				"CREATE TABLE initial_child() INHERITS(p)",
				"ALTER TABLE ONLY p ALTER a SET NOT NULL",
				"DROP TABLE initial_child",
				"CREATE TABLE c(k INT " + key + ") INHERITS(p)",
				"INSERT INTO c(a,k) VALUES(NULL,1)",
				"ALTER TABLE p ADD CONSTRAINT forward_fk FOREIGN KEY(a) REFERENCES c(k) ON UPDATE CASCADE ON DELETE RESTRICT",
				"ALTER TABLE p ADD CONSTRAINT second_forward_fk FOREIGN KEY(a) REFERENCES c(k)",
				"INSERT INTO p VALUES(1)",
			} {
				run(t, e, sql)
			}
			for range 2 {
				var dump bytes.Buffer
				if err := e.Dump(context.Background(), &dump); err != nil {
					t.Fatal(err)
				}
				foreignPosition := strings.Index(dump.String(), `ALTER TABLE ONLY "public"."p" ADD CONSTRAINT "forward_fk"`)
				for _, rows := range []string{`INSERT INTO "public"."p"`, `INSERT INTO "public"."c"`} {
					position := strings.Index(dump.String(), rows)
					if position < 0 || foreignPosition <= position {
						t.Fatal("valid ancestor FK preceded an actual physical row stream", dump.String())
					}
				}
				statements, err := parser.Parse(dump.String())
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, statement := range statements {
					alter, ok := statement.(*parser.AlterTable)
					if !ok || len(alter.Actions) != 1 || alter.Actions[0].ForeignKey == nil || alter.Actions[0].ForeignKey.Name.Text != "forward_fk" {
						continue
					}
					foreign := alter.Actions[0].ForeignKey
					if !alter.Only || foreign.NotValid || (len(foreign.Referenced) == 0) != (key == "PRIMARY KEY") || foreign.UpdateAction != parser.ForeignKeyCascade || foreign.DeleteAction != parser.ForeignKeyRestrict {
						t.Fatal("captured valid key/actions were not genuinely declared", foreign)
					}
					found = true
				}
				if !found {
					t.Fatal("missing genuine validated FK declaration", dump.String())
				}
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err, dump.String())
				}
				if got := tuples(run(t, e, "SELECT a FROM ONLY p")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}}) {
					t.Fatal(got)
				}
				if got := tuples(run(t, e, "SELECT a,k FROM ONLY c")); !reflect.DeepEqual(got, [][]driver.Value{{nil, int64(1)}}) {
					t.Fatal(got)
				}
				view := e.CatalogRead()
				parent := view.state.descriptors[roleTableRef(t, e, "p").Address].(*relationDescriptor)
				child := view.state.descriptors[roleTableRef(t, e, "c").Address].(*relationDescriptor)
				var forward *foreignKeyConstraint
				for i := range parent.foreignKeys {
					if parent.foreignKeys[i].Name == "forward_fk" {
						forward = &parent.foreignKeys[i]
					}
				}
				if len(parent.foreignKeys) != 3 || forward == nil || forward.NotValid || forward.Target != child.address || len(parent.notNullConstraints) != 1 || !parent.notNullConstraints[0].NoInherit || !child.columns[child.index["a"]].Nullable {
					t.Fatal("roundtrip changed actual physical constraint facts", parent.foreignKeys, parent.notNullConstraints, child.columns)
				}
				_, err = testExecute(e, context.Background(), statement(t, "INSERT INTO p VALUES(2)"), nil)
				state(t, err, "23503")
			}
		})
	}
}

func inheritedForeignKeyImportFixture(t *testing.T) (*Engine, *Operation, map[ids.ObjectAddress]ids.ObjectRef) {
	t.Helper()
	source := New()
	ownershipUnitExec(t, source, "CREATE ROLE fk_receipt_owner")
	e := source.restoreStage()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	created := make(map[ids.ObjectAddress]ids.ObjectRef)
	for _, sql := range []string{"CREATE TABLE public.p(a INT)", "CREATE TABLE public.c(k INT UNIQUE) INHERITS(public.p)", "CREATE TABLE public.other(k INT UNIQUE)"} {
		ref := roleExecute(t, e, op, sql).created
		if ref == (ids.ObjectRef{}) {
			t.Fatal("fixture did not capture a genuine creation")
		}
		created[ref.Address] = ref
	}
	roleExecute(t, e, op, "INSERT INTO public.p VALUES(1)")
	roleExecute(t, e, op, "INSERT INTO public.c(a,k) VALUES(NULL,1)")
	return e, op, created
}

func TestRestoreInheritedForeignKeyBoundary(t *testing.T) {
	const canonical = "ALTER TABLE ONLY public.p ADD CONSTRAINT forward_fk FOREIGN KEY(a) REFERENCES public.c(k)"
	for _, trial := range []string{
		strings.Replace(canonical, "ONLY ", "", 1),
		strings.Replace(canonical, "ONLY public.p", "ONLY p", 1),
		strings.Replace(canonical, "REFERENCES public.c", "REFERENCES public.other", 1),
	} {
		t.Run(trial, func(t *testing.T) {
			e, op, created := inheritedForeignKeyImportFixture(t)
			before := e.state
			raw := statement(t, trial).(*parser.AlterTable)
			_, admitted, err := e.captureInheritedForeignKey(op, raw, created)
			state(t, err, "0A000")
			if admitted || e.state != before {
				t.Fatal("unpaired public/general valid FK import gained authority", admitted, err)
			}
		})
	}
	// IF EXISTS is not admitted by the public FK grammar. Start with the
	// canonical AST so this private rejection reaches the import recognizer.
	t.Run("if_exists", func(t *testing.T) {
		e, op, created := inheritedForeignKeyImportFixture(t)
		raw := statement(t, canonical).(*parser.AlterTable)
		raw.IfExists = true
		before := e.state
		_, admitted, err := e.captureInheritedForeignKey(op, raw, created)
		state(t, err, "0A000")
		if admitted || e.state != before {
			t.Fatal("IF EXISTS gained import authority", admitted, err)
		}
	})
	e, op, created := inheritedForeignKeyImportFixture(t)
	raw := statement(t, canonical).(*parser.AlterTable)
	for _, missing := range []string{"p", "c"} {
		bad := maps.Clone(created)
		delete(bad, roleTableRef(t, e, missing).Address)
		if _, admitted, err := e.captureInheritedForeignKey(op, raw, bad); admitted || err == nil {
			t.Fatal("nonfresh endpoint gained import authority", missing, admitted, err)
		}
	}
	ordinary := New()
	ordinaryOp, ordinaryDone := roleOperation(t, ordinary, "postgres")
	defer ordinaryDone()
	if _, admitted, err := ordinary.captureInheritedForeignKey(ordinaryOp, raw, created); admitted || err == nil {
		t.Fatal("ordinary engine gained private import authority", admitted, err)
	}
	if _, admitted, err := e.captureInheritedForeignKey(ordinaryOp, raw, created); admitted || err == nil {
		t.Fatal("foreign operation gained private import authority", admitted, err)
	}
}

func TestRestoreInheritedForeignKeyObservations(t *testing.T) {
	for _, mutation := range []string{
		"ALTER TABLE public.c ALTER k TYPE BIGINT",
		"ALTER TABLE public.p OWNER TO fk_receipt_owner",
		"ALTER INDEX public.c_k_key RENAME TO changed_key",
		"CREATE UNIQUE INDEX extra_key ON public.c(k)",
	} {
		t.Run(mutation, func(t *testing.T) {
			e, op, created := inheritedForeignKeyImportFixture(t)
			raw := statement(t, "ALTER TABLE ONLY public.p ADD CONSTRAINT forward_fk FOREIGN KEY(a) REFERENCES public.c(k)").(*parser.AlterTable)
			pending, admitted, err := e.captureInheritedForeignKey(op, raw, created)
			if err != nil || !admitted {
				t.Fatal("genuine source/target/provider preparation rejected", admitted, err)
			}
			roleExecute(t, e, op, mutation)
			before := e.state
			if err := e.bindRestoredForeignKeys(context.Background(), op, []restoredForeignKeys{pending}); !errors.Is(err, ErrCatalogReplaced) || e.state != before {
				t.Fatal("stale source/key/provider receipt rebound or published", err)
			}
		})
	}
	t.Run("canceled_after_loaded_rows", func(t *testing.T) {
		e, op, created := inheritedForeignKeyImportFixture(t)
		raw := statement(t, "ALTER TABLE ONLY public.p ADD CONSTRAINT forward_fk FOREIGN KEY(a) REFERENCES public.c(k)").(*parser.AlterTable)
		pending, admitted, err := e.captureInheritedForeignKey(op, raw, created)
		if err != nil || !admitted {
			t.Fatal(admitted, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		before := e.state
		if err := e.bindRestoredForeignKeys(ctx, op, []restoredForeignKeys{pending}); !errors.Is(err, context.Canceled) || e.state != before {
			t.Fatal("canceled post-data validation published staged metadata", err)
		}
	})
}

func TestRestoreInheritedForeignKeyAtomicity(t *testing.T) {
	e := New()
	ownershipUnitExec(t, e, "CREATE ROLE fk_receipt_owner")
	run(t, e, "CREATE TABLE sentinel(a INT)")
	run(t, e, "INSERT INTO sentinel VALUES(91)")
	before, oid, revision := e.state, e.nextOID, e.nextRevision
	sentinel := roleTableRef(t, e, "sentinel")
	storage := e.storage[sentinel.Address]
	const declarations = "CREATE TABLE public.support(k INT UNIQUE);CREATE TABLE public.p(a INT,CONSTRAINT ordinary_fk FOREIGN KEY(a) REFERENCES public.support(k));CREATE TABLE public.c(k INT UNIQUE) INHERITS(public.p);INSERT INTO public.support VALUES(2);INSERT INTO public.p VALUES(2);INSERT INTO public.c(a,k) VALUES(NULL,1);"
	const foreign = "ALTER TABLE ONLY public.p ADD CONSTRAINT forward_fk FOREIGN KEY(a) REFERENCES public.c(k);"
	err := e.Restore(context.Background(), strings.NewReader(declarations+foreign))
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23503" || diagnostic.Message != `insert or update on table "p" violates foreign key constraint "forward_fk"` || diagnostic.Detail != `Key (a)=(2) is not present in table "c".` || diagnostic.Table != "p" || diagnostic.Constraint != "forward_fk" {
		t.Fatal("late validated FK lost the native English violation", err, diagnostic)
	}
	state(t, e.Restore(context.Background(), strings.NewReader(declarations+foreign+foreign)), "42710")
	state(t, e.Restore(context.Background(), strings.NewReader(declarations+strings.Replace(foreign, "public.c(k)", "public.c(a)", 1))), "42830")
	for _, script := range []string{
		declarations + "SET SESSION AUTHORIZATION fk_receipt_owner;" + foreign,
		"CREATE SCHEMA owned AUTHORIZATION fk_receipt_owner;SET SESSION AUTHORIZATION fk_receipt_owner;CREATE TABLE owned.p(a INT);SET SESSION AUTHORIZATION postgres;CREATE TABLE public.c(k INT UNIQUE) INHERITS(owned.p);SET SESSION AUTHORIZATION fk_receipt_owner;ALTER TABLE ONLY owned.p ADD CONSTRAINT forward_fk FOREIGN KEY(a) REFERENCES public.c(k);",
	} {
		state(t, e.Restore(context.Background(), strings.NewReader(script)), "42501")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Restore(ctx, strings.NewReader(declarations+foreign)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.state != before || e.nextOID != oid || e.nextRevision != revision || e.storage[sentinel.Address] != storage || !reflect.DeepEqual(tuples(run(t, e, "SELECT a FROM sentinel")), [][]driver.Value{{int64(91)}}) {
		t.Fatal("failed/canceled isolated FK replay changed the published target")
	}
}
