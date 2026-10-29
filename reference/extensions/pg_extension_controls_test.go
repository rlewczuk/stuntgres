package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func pgExtensionSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	s := fixture.Select("SELECT * FROM pgext_control_sentinel", []string{"id", "words"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}})
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(s))
}

func pgExtensionControlsScenario(t *testing.T) {
	t.Helper()
	p := fixture.NewScenario(t)
	native := p.Backends()[0]
	fixture.RunStep(t, p.Ctx, native.DB, native.Recorder, fixture.Exec("CREATE TABLE pgext_control_sentinel(id INT,words TEXT)", 0))
	fixture.RunStep(t, p.Ctx, native.DB, native.Recorder, fixture.Exec("INSERT INTO pgext_control_sentinel VALUES(7,'kept')", 1))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "CREATE TABLE") || !strings.Contains(dump.String(), "INSERT INTO") {
		t.Fatal("missing actual user dump", dump.String())
	}
	for _, forbidden := range []string{"pg_extension", "CREATE EXTENSION", "LOAD "} {
		if strings.Contains(dump.String(), forbidden) {
			t.Fatal("serialized extension mechanism", dump.String())
		}
	}
	backends := p.Backends()
	for i := len(backends) - 1; i >= 0; i-- {
		b := backends[i]
		t.Run(b.Name, func(t *testing.T) {
			if b.PG {
				oid := pgExtensionInstalled(t, p, b)
				conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				if _, err := conn.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("PG rejected actual native dump", err)
				}
				pgExtensionSentinel(t, p, b, conn)
				if after := pgExtensionInstalled(t, p, b); after != oid {
					t.Fatal("dump replay changed installed identity", oid, after)
				}
				pgExtensionClass(t, p, b)
				return
			}
			oid := pgExtensionInstalled(t, p, b)
			retainedOID := oid
			old, err := b.DB.QueryContext(p.Ctx, pgExtensionProbe, "plpgsql")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			pgExtensionMetadata(t, old, false)
			types, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			stmt, err := b.DB.PrepareContext(p.Ctx, pgExtensionProbe)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgExtensionProbe}
			check := func() {
				pgExtensionSentinel(t, p, b, b.DB)
				if after := pgExtensionInstalled(t, p, b); after != oid {
					t.Fatal("failed control changed installed identity", oid, after)
				}
				pgExtensionClass(t, p, b)
				pgExtensionHeld(t, p, b, held, oid)
			}
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"CREATE SEQUENCE pgext_control_invalid CACHE 0;"))
			want := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
			fixture.AssertDiagnostic(t, err, &want)
			check()
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
				t.Fatal("Restore cancellation", err)
			}
			if err := stuntgres.Reset(ctx, p.Native); !errors.Is(err, context.Canceled) {
				t.Fatal("Reset cancellation", err)
			}
			check()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			oid = pgExtensionInstalled(t, p, b)
			pgExtensionClass(t, p, b)
			pgExtensionHeld(t, p, b, held, oid)
			s := fixture.ImplicitError("SELECT * FROM pgext_control_sentinel", fixture.Diagnostic{Code: "42P01", Message: "relation \"pgext_control_sentinel\" does not exist", Position: "15"})
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			oid = pgExtensionInstalled(t, p, b)
			check()
			if !old.Next() {
				t.Fatal("missing retained installed row", old.Err())
			}
			var oldOID, owner, namespace int64
			var name, version string
			var relocatable bool
			var config, condition any
			if err := old.Scan(&oldOID, &name, &owner, &namespace, &relocatable, &version, &config, &condition); err != nil {
				t.Fatal(err)
			}
			if oldOID != retainedOID || name != "plpgsql" || owner != 10 || namespace != 11 || relocatable || version != "1.0" || config != nil || condition != nil {
				t.Fatal("retained installed row mutated", oldOID, name, owner, namespace, relocatable, version, config, condition)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal("extra retained installed row", old.Err())
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			pgExtensionRetainedMetadata(t, types, false)
			pgExtensionCanceled(t, p, b)
		})
	}
}

func TestReferencePGExtensionControls(t *testing.T) { pgExtensionControlsScenario(t) }
