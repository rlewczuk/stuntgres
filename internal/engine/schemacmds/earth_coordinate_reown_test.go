package schemacmds_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestEarthCoordinateReownOrdering(t *testing.T) {
	for _, typesOnly := range []bool{false, true} {
		for _, supplied := range []bool{false, true} {
			for _, reverse := range []bool{false, true} {
				t.Run(fmt.Sprintf("typesOnly=%v/memo=%v/reverse=%v", typesOnly, supplied, reverse), func(t *testing.T) {
					ctx := context.Background()
					e, ext, earth := earthUnitInstalled(t)
					op, done := enginetest.RoleOperation(t, e, "postgres")
					owner := roleAttributeCreate(t, e, op, "earth_reown_owner", false, parser.RoleOptions{})
					done()
					original := e.CatalogRead()
					state := catalog.CloneState(original.State())
					for _, slot := range []int{2, 5} {
						old := state.MustProcedure(ext.Members[slot])
						p := *old
						if slot == 2 {
							p.ObjectName = "radius_now"
							p.Owner = owner.Ref
							p.ACL = authorization.SchemaACLOwner(p.ACL, old.Owner, owner.Ref)
						} else {
							p.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
						}
						state.UnbindName(catalog.DescriptorNameKey(old))
						state.SetDescriptor(p.Address(), &p)
						state.BindName(catalog.DescriptorNameKey(&p), p.Address())
					}
					before := e.Catalog.Read(state)
					roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, before)
					metadata, err := extension.OwnerMetadata(before, roles, ext)
					if err != nil {
						t.Fatal("live independent provider owners", err)
					}
					record, found, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
					if err != nil || !found {
						t.Fatal(err)
					}
					record = extensionOwnerRecipeOrder(record)
					if record.Members[2].Name != "radius_now" || record.Members[2].Owner != "earth_reown_owner" || record.Members[5].Schema != "pg_catalog" || record.Members[0].Owner == record.Members[2].Owner {
						t.Fatal("lost independent labels/owners", record)
					}
					floating, _ := before.LookupType(sqltype.Float8)
					columns := make([]catalog.Column, 6)
					attributes := []int32{1, 2, 3, 4, 5, 6}
					defaults := make(map[int32]catalog.ColumnDefault, 6)
					var sourceCalls [6]plan.InstalledEarthExpr
					var userRoutines [6]ids.ObjectAddress
					for i := range 6 {
						operation := sqltype.EarthOperation(i)
						p := before.MustProcedure(ext.Members[i+2])
						result, formal := floating, floating
						if operation == sqltype.EarthLL {
							result = earth.SQLType
						}
						if operation >= sqltype.EarthLatitude {
							formal = earth.SQLType
						}
						args := make([]expr.Expr, operation.Arity())
						for j := range args {
							args[j] = plan.LiteralExpr{Info: expr.Info{Usage: formal.Usage()}, Value: sqltype.Null(formal)}
						}
						kernel, err := functions.NewEarthCoordinate(expr.Info{Usage: result.Usage(), RoutineReference: p, Property: expr.Properties{Volatility: expr.Immutable}}, operation, earth.SQLType, args)
						if err != nil {
							t.Fatal(err)
						}
						call := plan.InstalledEarthExpr{EarthCoordinate: kernel, Procedure: p, Ref: before.Owner().Domain.Ref(before.Epoch(), p.Address())}
						sourceCalls[i] = call
						columns[i] = catalog.Column{Name: operation.Name(), Usage: result.Usage()}
						defaults[int32(i+1)] = catalog.ColumnDefault{OID: ids.OID(901000 + i), Expression: call, Dependencies: []ids.ObjectRef{call.Ref}}
						// A real SQL declaration-default payload forces the routine
						// preparation path to validate the same installed provider.
						payload, err := stored.NewRoutineDefaults(ctx, before, nil, []expr.Expr{call})
						if err != nil {
							t.Fatal(err)
						}
						definition := &proc.Definition{Name: fmt.Sprintf("earth_reown_default_%d", i), Result: result.Usage()}
						if err := definition.SetFormals([]proc.Formal{{Mode: "i", Name: "a", Usage: result.Usage(), HasDefault: true}}); err != nil {
							t.Fatal(err)
						}
						address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(902000 + i)}
						userRoutines[i] = address
						declaration := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: earth.Namespace(), ObjectName: definition.Name, ObjectRevision: 1, RoutineKind: catalog.FunctionRoutine, Owner: ext.Owner, Signature: catalog.ProcedureSignature(strconv.FormatUint(uint64(result.ID()), 10)), Result: ids.OID(result.ID()), SQL: definition, SQLDefaults: payload}
						state.SetDescriptor(address, declaration)
						state.BindName(catalog.DescriptorNameKey(declaration), address)
					}
					oid := ids.OID(900100)
					if reverse {
						oid = 900101
					}
					consumer := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: earth.Namespace(), ObjectName: "earth_reown_consumer", ObjectRevision: 1, RelationKind: catalog.TableRelation, Owner: ext.Owner, Columns: columns, Attributes: attributes, ColumnMeta: catalog.ColumnMetadata{HighWater: 6, Defaults: defaults}}
					state.SetDescriptor(consumer.Address(), consumer)
					state.BindName(catalog.DescriptorNameKey(consumer), consumer.Address())
					next := catalog.CloneState(state)
					base := earth.SQLType.DomainBase().Type
					fresh, err := sqltype.Cube(base.ID(), base.Name(), base.SQLName())
					if err != nil {
						t.Fatal(err)
					}
					baseAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.ID())}
					baseType := *next.MustType(baseAddress)
					baseType.SQLType = fresh
					next.SetDescriptor(baseAddress, &baseType)
					if _, err := typedef.ReownDomainTypes(next, nil, false, routine.RangeTypeRebinder(next, nil)); err != nil {
						t.Fatal(err)
					}
					if !typesOnly {
						next.SetEpoch(next.Epoch() + 1)
					}
					view := e.Catalog.Read(next)
					if err := extension.StockEarthdistance.ValidateExtension(view, ext); err == nil {
						t.Fatal("half-remapped CHECK generation was accepted")
					}
					if err := stored.ReownScalarTypes(next, nil, nil); err != nil {
						t.Fatal(err)
					}
					var remap *catalog.RoutineRemap
					if supplied {
						remap = stored.NewRoutineRemap(view, nil, nil, nil, typesOnly)
						if err := stored.ReownInstalledDomainChecks(view, next, nil, typesOnly, remap); err != nil {
							t.Fatal("provider bootstrap", err)
						}
						prepared := next.MustType(earth.Address())
						if err := extension.StockEarthdistance.ValidateExtension(view, ext); err != nil {
							t.Fatal("bootstrapped manifest", err)
						}
						if err := stored.ReownInstalledDomainChecks(view, next, nil, typesOnly, remap); err != nil || next.MustType(earth.Address()) != prepared {
							t.Fatal("bootstrap remapped destination a second time", err)
						}
						if !typesOnly {
							if err := routine.RebindRoutineDefinitions(next, remap); err != nil {
								t.Fatal("routine defaults before relation consumers", err)
							}
						}
					}
					if err := stored.ReownExpressions(view, next, nil, typesOnly, remap); err != nil {
						t.Fatal("ordered stored reownership", err)
					}
					current := next.MustType(earth.Address())
					if err := extension.StockEarthdistance.ValidateExtension(view, ext); err != nil {
						t.Fatal(err)
					}
					for i, source := range sourceCalls {
						call := next.MustRelation(consumer.Address()).ColumnMeta.Defaults[int32(i+1)].Expression.(plan.InstalledEarthExpr)
						expectedEarth := current.SQLType
						if sqltype.EarthOperation(i) < sqltype.EarthLL {
							expectedEarth = nil
						}
						if call.Plan.Earth() != expectedEarth {
							t.Fatal("hidden earth generation retained", i)
						}
						if err := stored.ValidateInstalledEarth(call, view); err != nil {
							t.Fatal(err)
						}
						// Stored scalar calls retain only FLOAT8 and the genuine
						// routine/domain identity, not the incidental Earth/Cube
						// generation. An epoch change during stored reownership
						// alone does not retire this unchanged scalar declaration.
						err := stored.ValidateInstalledEarth(source, view)
						unchangedScalar := sqltype.EarthOperation(i) < sqltype.EarthLL
						if unchangedScalar && err != nil {
							t.Fatal("unchanged scalar capture rejected", i, err)
						}
						if !unchangedScalar && err == nil {
							t.Fatal("stale nominal source generation accepted", i)
						}
						foreignLifetime := call
						foreignLifetime.Ref = (&ids.Domain{}).Ref(view.Epoch(), call.Ref.Address)
						if err := stored.ValidateInstalledEarth(foreignLifetime, view); err == nil {
							t.Fatal("foreign catalog lifetime accepted", i)
						}
						missingRoutine := call
						missingRoutine.Ref.Address.ObjectID = ^ids.OID(0)
						if err := stored.ValidateInstalledEarth(missingRoutine, view); err == nil {
							t.Fatal("missing remapped routine accepted", i)
						}
						badPrincipal := catalog.CloneState(next)
						backing := *next.MustProcedure(call.Procedure.Address())
						backing.Owner = (&authorization.Domain{}).Principal(backing.Owner.ID())
						badPrincipal.SetDescriptor(backing.Address(), &backing)
						if err := stored.ValidateInstalledEarth(call, view.WithState(badPrincipal)); err == nil {
							t.Fatal("foreign current routine principal accepted", i)
						}
						p := next.MustProcedure(userRoutines[i])
						defaultCall, ok := p.SQLDefaults.Default(0)
						if !ok {
							t.Fatal("lost SQL declaration default")
						}
						if got := defaultCall.(plan.InstalledEarthExpr).Plan.Earth(); got != expectedEarth {
							t.Fatal("routine hidden earth generation retained", i)
						}
						if err := stored.ValidateInstalledEarth(defaultCall.(plan.InstalledEarthExpr), view); err != nil {
							t.Fatal(err)
						}
					}
					if earth.SQLType.DomainBase().Type != base {
						t.Fatal("bootstrap mutated source domain")
					}
					broken := catalog.CloneState(next)
					bad := *current
					bad.DomainChecks = append([]catalog.DomainCheckConstraint(nil), current.DomainChecks...)
					bad.DomainChecks[0].Ordinal++
					broken.SetDescriptor(bad.Address(), &bad)
					if err := extension.StockEarthdistance.ValidateExtension(view.WithState(broken), ext); err == nil {
						t.Fatal("bootstrap weakened malformed-recipe rejection")
					}
				})
			}
		}
	}
}
