package extension_test

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Publish the actual ALTER stage, then project pg_proc from that publication.
// Retained descriptor and row backing distinguish COW publication from mutation.
func TestRoutineProviderRowProjection(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, name := range []string{"intagg", "pgrowlocks", "tablefunc", "pageinspect", "amcheck", "citext"} {
		enginetest.RoleExecute(t, e, op, "CREATE EXTENSION "+name)
	}
	base := e.CatalogRead()
	capture := func(view catalog.Read) map[ids.OID][]sqltype.Value {
		t.Helper()
		source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.ProcedureClassID})
		produce, ok := pgcatalog.ProducerFor(ids.ProcedureClassID)
		if !ok {
			t.Fatal("missing procedure producer")
		}
		rows, err := produce(pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.Roles(), Owner: &e.Catalog.Owner, Ref: e.Catalog.Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e})})
		if err != nil {
			t.Fatal(err)
		}
		result := make(map[ids.OID][]sqltype.Value, len(rows))
		for _, row := range rows {
			oid, err := row[0].OID()
			if err != nil {
				t.Fatal(err)
			}
			result[ids.OID(oid)] = row
		}
		return result
	}
	retained := capture(base)
	// Independently expected released exceptions plus ordinary/default controls.
	for oid, want := range map[ids.OID]float64{1067: 1000, 2763: 1, 2764: 10, 2947: 50, 5064: 50, 2331: 100, 6159: 250, 1397: 0} {
		row := retained[oid]
		if row == nil {
			t.Fatal("missing released estimate control", oid)
		}
		got, err := row[6].Float()
		if err != nil || got != want {
			t.Fatal("released row estimate", oid, got, want, err)
		}
	}
	scalar := *base.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1397})
	scalar.Rows = 23
	if scalar.ReturnsSet() || scalar.RowEstimate() != 0 {
		t.Fatal("estimate fabricated scalar set capability")
	}
	providers := make(map[ids.ObjectAddress]*catalog.Procedure)
	targets := make(map[ids.ObjectAddress]string)
	for _, descriptor := range base.Descriptors() {
		d, ok := descriptor.(*catalog.Procedure)
		if !ok || !d.ReturnsSet() || d.SQL != nil {
			continue
		}
		providers[d.Address()] = d
		var arguments []string
		for id := range strings.SplitSeq(string(d.Signature), ",") {
			if id == "" {
				continue
			}
			n, err := strconv.ParseUint(id, 10, 32)
			if err != nil {
				t.Fatal(err)
			}
			typ := base.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(n)})
			arguments = append(arguments, catalog.DependencyIdentifier(base.MustNamespace(typ.Namespace()).Name())+"."+catalog.DependencyIdentifier(typ.Name()))
		}
		targets[d.Address()] = catalog.DependencyIdentifier(base.MustNamespace(d.Namespace()).Name()) + "." + catalog.DependencyIdentifier(d.Name()) + "(" + strings.Join(arguments, ",") + ")"
		if d.Implementation != nil {
			got, err := retained[d.Address().ObjectID][6].Float()
			if err != nil || got != float64(d.Installed.Rows) || !d.Installed.ReturnsSet {
				t.Fatal("installed intrinsic estimate", d.Name(), got, err)
			}
			if d.Implementation == extension.StockIntagg {
				for _, change := range []func(*catalog.Procedure){
					func(p *catalog.Procedure) { p.Installed.ReturnsSet = false },
					func(p *catalog.Procedure) { p.Installed.Rows = 23 },
				} {
					bad := *d
					change(&bad)
					state := catalog.CloneState(base.State())
					state.SetDescriptor(bad.Address(), &bad)
					if _, err := extension.ProcedureSymbol(base.WithState(state), &bad); err == nil {
						t.Fatal("intagg immutable set declaration was not sealed")
					}
				}
			}
		}
	}
	if len(providers) == 0 {
		t.Fatal("no genuine set providers")
	}
	for _, estimate := range []string{"23", "31"} {
		before := e.CatalogRead()
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		stage := e.BeginCatalogStage(auth.Catalog)
		for address := range providers {
			raw := enginetest.Statement(t, "ALTER FUNCTION "+targets[address]+" ROWS "+estimate).(*parser.AlterRoutine)
			err = routine.StageAlterRoutine(schemacmds.RoutineHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, raw)
			if err != nil {
				break
			}
		}
		e.Mu.Unlock()
		if err != nil {
			t.Fatal("set-provider ALTER", err)
		}
		prepared, err := e.ValidateCatalogStage(t.Context(), stage)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: before.Epoch()}); err != nil {
			t.Fatal(err)
		}
		current := e.CatalogRead()
		projected := capture(current)
		want, _ := strconv.ParseFloat(estimate, 32)
		for address, original := range providers {
			d := current.MustProcedure(address)
			copy := *d
			copy.Rows, copy.ObjectRevision = original.Rows, original.ObjectRevision
			got, err := projected[address.ObjectID][6].Float()
			set, setErr := projected[address.ObjectID][13].Boolean()
			if err != nil || setErr != nil || got != want || !set || d == original || !reflect.DeepEqual(copy, *original) {
				t.Fatal("publication changed intrinsic identity or lost row estimate", address, got, err, setErr)
			}
			old, err := retained[address.ObjectID][6].Float()
			if err != nil || old != float64(original.RowEstimate()) || base.MustProcedure(address) != original {
				t.Fatal("publication mutated retained descriptor/projection", address, old, err)
			}
		}
		// A rejected estimate must not produce a candidate or alter either
		// the current publication or the earlier retained capture.
		for address := range providers {
			raw := enginetest.Statement(t, "ALTER FUNCTION "+targets[address]+" ROWS 0").(*parser.AlterRoutine)
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			rejected := e.BeginCatalogStage(auth.Catalog)
			err = routine.StageAlterRoutine(schemacmds.RoutineHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: rejected}}, auth, raw)
			e.Mu.Unlock()
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != "ROWS must be positive" || rejected.Candidate.DescriptorCount() != 0 || e.CatalogRead().State() != current.State() {
				t.Fatal("invalid estimate changed publication", address, err)
			}
		}
	}
}
