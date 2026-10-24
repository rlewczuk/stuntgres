package engine

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type cubeConflictStage struct {
	extension.Stage
	name  string
	owner authorization.PrincipalRef
}

func (s cubeConflictStage) Create(d catalog.Descriptor) error {
	if err := s.Stage.Create(d); err != nil {
		return err
	}
	typ, ok := d.(*catalog.Type)
	if !ok || !typ.SQLType.IsCube() {
		return nil
	}
	oid, err := s.AllocateOID()
	if err != nil {
		return err
	}
	rev, err := s.Revision()
	if err != nil {
		return err
	}
	return s.Stage.Create(&catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: typ.Namespace(), ObjectRevision: rev, ObjectName: s.name, Signature: extension.CubeComparisonSignature(typ.SQLType.ID()), Owner: s.owner})
}

func cubeComparisonProviderConflicts(t *testing.T) {
	t.Helper()
	for _, name := range []string{"cube_eq", "cube_cmp"} {
		t.Run("staged_"+name+"_conflict", func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			before := e.catalog.State
			e.mu.Lock()
			err := func() error {
				auth, err := e.authorizationLocked(op)
				if err != nil {
					return err
				}
				stage := e.BeginCatalogStage(auth.Catalog)
				defer e.DiscardCatalogStage(stage)
				ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
				stage.authorizeLocked(auth, op.Session)
				stage.extensionNamespace = ns
				oid, err := e.catalog.AllocateOIDLocked()
				if err != nil {
					return err
				}
				rev, err := e.catalog.RevisionLocked()
				if err != nil {
					return err
				}
				ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: oid}, ObjectName: "cube", ObjectRevision: rev, Version: "1.5", TargetNamespace: ns, Owner: auth.State.CurrentUser}
				if err := stage.Create(ext); err != nil {
					return err
				}
				host := &extensionHost{engine: e, auth: auth, stage: stage}
				return extension.StockCube.Stage(cubeConflictStage{Stage: host, name: name, owner: auth.State.CurrentUser}, ext, ext.Owner)
			}()
			e.mu.Unlock()
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || e.catalog.State != before {
				t.Fatal("comparison conflict published a prefix", err)
			}
		})
	}
	t.Run("comparison_signature_and_graph_corruption", func(t *testing.T) {
		e, ext, base := cubeUnitInstalled(t)
		signature := strconv.FormatUint(uint64(base.SQLType.ID()), 10)
		for _, corrupt := range []func(*catalog.State){
			func(s *catalog.State) {
				p := *s.MustProcedure(ext.Members[3])
				p.Signature = "25," + catalog.ProcedureSignature(signature)
				s.SetDescriptor(p.Address(), &p)
			},
			func(s *catalog.State) {
				p := *s.MustProcedure(ext.Members[3])
				p.Signature = catalog.ProcedureSignature(signature + ",25")
				s.SetDescriptor(p.Address(), &p)
			},
			func(s *catalog.State) {
				p := *s.MustProcedure(ext.Members[9])
				p.Implementation = extension.StockCitext
				s.SetDescriptor(p.Address(), &p)
			},
			func(s *catalog.State) {
				o, _ := s.Operator(ext.Members[10])
				n := *o
				n.Commutator = ext.Members[11].ObjectID
				s.SetDescriptor(n.Address(), &n)
			},
			func(s *catalog.State) {
				c, _ := e.catalog.Read(s).OperatorClass(ext.Members[17].ObjectID)
				n := *c
				n.Extension = ids.ObjectAddress{}
				s.SetDescriptor(n.Address(), &n)
			},
			func(s *catalog.State) {
				f, _ := e.catalog.Read(s).OperatorFamily(ext.Members[16])
				n := *f
				n.Method = 405
				s.SetDescriptor(n.Address(), &n)
			},
			func(s *catalog.State) {
				m, _ := e.catalog.Read(s).FamilySupport(ext.Members[16].ObjectID, ids.OID(base.SQLType.ID()), ids.OID(base.SQLType.ID()), 1)
				n := *m
				n.Function = ext.Members[3].ObjectID
				s.SetDescriptor(n.Address(), &n)
			},
		} {
			candidate := catalog.CloneState(e.catalog.State)
			corrupt(candidate)
			if err := extension.StockCube.ValidateExtension(e.catalog.Read(candidate), ext); err == nil {
				t.Fatal("corrupt comparison manifest admitted")
			}
		}
	})
}

func cubeComparisonProviderRemap(t *testing.T) {
	t.Helper()
	e, ext, base := cubeUnitInstalled(t)
	target := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 990001}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{base.Address(): target, ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 990002}}
	for _, address := range ext.Members[3:10] {
		original := e.CatalogRead().MustProcedure(address)
		mapped := extension.RemapDescriptor(original, mapping).(*catalog.Procedure)
		if mapped.Signature != "990001,990001" || mapped.Extension != mapping[ext.Address()] || original.Signature != extension.CubeComparisonSignature(base.SQLType.ID()) {
			t.Fatal("both-argument remap/source immutability", mapped, original)
		}
	}
	metadata, err := extension.OwnerMetadata(e.CatalogRead(), e.roles.read(&e.catalog.Owner, e.CatalogRead(), e.roles.published), ext)
	if err != nil || metadata == "" {
		t.Fatal("portable owners", err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimPrefix(metadata, "-- "))
	if err != nil || !found {
		t.Fatal("portable owner decoding", err)
	}
	kinds := map[string]int{}
	for _, member := range record.Members {
		kinds[member.Kind]++
	}
	for kind, want := range map[string]int{"type": 1, "procedure": 27, "operator": 14, "operator_class": 1, "operator_family": 1} {
		if kinds[kind] != want {
			t.Fatal("complete symbolic owner kinds", kind, kinds)
		}
	}
	for _, corrupt := range []func(*extension.OwnerRecord){
		func(r *extension.OwnerRecord) { r.Members = r.Members[:17] },
		func(r *extension.OwnerRecord) { r.Members[2] = r.Members[1] },
		func(r *extension.OwnerRecord) { r.Members[1].Kind = "alien" },
		func(r *extension.OwnerRecord) { r.Members[3].Signature = "cube,text" },
		func(r *extension.OwnerRecord) { r.Members[17].Signature = "hash" },
	} {
		bad := record
		bad.Members = append([]extension.MemberOwner(nil), record.Members...)
		corrupt(&bad)
		candidate := catalog.CloneState(e.catalog.State)
		roles := e.roles.read(&e.catalog.Owner, e.CatalogRead(), e.roles.published)
		if _, err := extension.ImportOwners(context.Background(), e.CatalogRead().WithState(candidate), roles, []extension.OwnerRecord{bad}); err == nil {
			t.Fatal("corrupt owner record admitted", bad)
		}
		if err := extension.StockCube.ValidateExtension(e.CatalogRead(), ext); err != nil {
			t.Fatal("failed import damaged source", err)
		}
	}
	for _, address := range ext.Members[10:] {
		if catalog.ExtensionMembership(e.CatalogRead().Get(address)) != ext.Address() {
			t.Fatal("nonprocedure ownership", address)
		}
	}
}

func TestCubeIndexSupport(t *testing.T) {
	t.Run("transport_retains_catalog_owner", cubeIndexTransportAuthority)
	e, ext, base := cubeUnitInstalled(t)
	view := e.CatalogRead()
	class, ok := view.DefaultIndexClass(403, base.SQLType)
	if !ok {
		t.Fatal("missing dynamic default")
	}
	p, err := routine.BindIndexOrdering(view, base.SQLType.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	support := p.Support()
	if support.Provider != sqltype.CubeIndexSupport || support.Routine != uint32(ext.Members[9].ObjectID) || support.Left != uint32(base.SQLType.ID()) || support.Right != support.Left {
		t.Fatal("dynamic support capture", support)
	}
	a, err := base.SQLType.Input(context.Background(), "(2)", -1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := base.SQLType.Input(context.Background(), "(1,100)", -1)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := p.Compare(context.Background(), a, b, sqltype.InputEnvironment{}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	captured, err := routine.BindClassSupport(view, class)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []sqltype.IndexSupport{
		{Provider: sqltype.CubeIndexSupport},
		{Provider: sqltype.IndexSupportProvider(99), Family: support.Family, Member: support.Member, Routine: support.Routine, Left: support.Left, Right: support.Right},
	} {
		if _, err := sqltype.BindIndexOrdering(base.SQLType.Usage(), captured.WithSupport(1, bad)); err == nil {
			t.Fatal("invalid capture admitted", bad)
		}
	}
	for _, mutate := range []func(*sqltype.IndexSupport){
		func(s *sqltype.IndexSupport) { s.Family++ },
		func(s *sqltype.IndexSupport) { s.Left++ },
		func(s *sqltype.IndexSupport) { s.Right++ },
		func(s *sqltype.IndexSupport) { s.Member = 0 },
		func(s *sqltype.IndexSupport) { s.Routine = 0 },
		func(s *sqltype.IndexSupport) { s.Revision = 0 },
		func(s *sqltype.IndexSupport) { s.FamilyRevision = 0 },
		func(s *sqltype.IndexSupport) { s.RoutineRevision = 0 },
	} {
		bad := support
		mutate(&bad)
		if _, err := sqltype.BindIndexOrdering(base.SQLType.Usage(), captured.WithSupport(1, bad)); err == nil {
			t.Fatal("invalid cube support authority", bad)
		}
	}
	revised := support
	revised.Revision++
	revised.FamilyRevision++
	revised.RoutineRevision++
	same, err := sqltype.BindIndexOrdering(base.SQLType.Usage(), captured.WithSupport(1, revised))
	if err != nil || !p.SameOperation(same) {
		t.Fatal("metadata revision changed comparison operation", err)
	}
	for _, mutate := range []func(*catalog.OperatorFamilySupport){
		func(s *catalog.OperatorFamilySupport) { s.Function = ext.Members[3].ObjectID },
		func(s *catalog.OperatorFamilySupport) { s.Left++ },
		func(s *catalog.OperatorFamilySupport) { s.Right++ },
		func(s *catalog.OperatorFamilySupport) { s.Family++ },
		func(s *catalog.OperatorFamilySupport) { s.OwningClass = 0 },
	} {
		candidate := catalog.CloneState(e.catalog.State)
		member, _ := view.FamilySupport(ids.OID(support.Family), ids.OID(support.Left), ids.OID(support.Right), 1)
		bad := *member
		mutate(&bad)
		candidate.SetDescriptor(bad.Address(), &bad)
		if _, err := routine.BindIndexOrdering(e.catalog.Read(candidate), base.SQLType.Usage(), class); err == nil {
			t.Fatal("corrupt actual support member admitted", bad)
		}
	}
	for _, slot := range []int{0, 1} {
		candidate := catalog.CloneState(e.catalog.State)
		proc := *candidate.MustProcedure(ext.Members[9])
		if slot == 0 {
			proc.Signature = "25," + extension.CubeSignature(base.SQLType.ID())
		} else {
			proc.Signature = extension.CubeSignature(base.SQLType.ID()) + ",25"
		}
		candidate.SetDescriptor(proc.Address(), &proc)
		if _, err := routine.BindIndexOrdering(e.catalog.Read(candidate), base.SQLType.Usage(), class); err == nil {
			t.Fatal("wrong support formal admitted", slot)
		}
	}
}
