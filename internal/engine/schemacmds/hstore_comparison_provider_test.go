package schemacmds_test

import (
	"errors"
	"strconv"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/sqlstate"
)


type hstoreConflictStage struct {
	extension.Stage
	name  string
	owner authorization.PrincipalRef
}

func (s hstoreConflictStage) Create(d catalog.Descriptor) error {
	if err := s.Stage.Create(d); err != nil {
		return err
	}
	typ, ok := d.(*catalog.Type)
	if !ok || !typ.SQLType.IsHstore() {
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
	return s.Stage.Create(&catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: typ.Namespace(), ObjectRevision: rev, ObjectName: s.name, Signature: hstoreUnitComparisonSignature(typ.SQLType.ID()), Owner: s.owner})
}

func TestHstoreComparisonProvider(t *testing.T) {
	t.Helper()
	for _, name := range []string{"hstore_eq", "hstore_cmp"} {
		t.Run("staged_"+name+"_conflict", func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			before := e.Catalog.State
			e.Mu.Lock()
			err := func() error {
				auth, err := e.AuthorizationLocked(op)
				if err != nil {
					return err
				}
				stage := e.BeginCatalogStage(auth.Catalog)
				defer e.DiscardCatalogStage(stage)
				ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
				stage.AuthorizeLocked(auth, op.Session)
				oid, err := e.Catalog.AllocateOIDLocked()
				if err != nil {
					return err
				}
				rev, err := e.Catalog.RevisionLocked()
				if err != nil {
					return err
				}
				ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: oid}, ObjectName: "hstore", ObjectRevision: rev, Version: "1.8", TargetNamespace: ns, Owner: auth.State.CurrentUser}
				if err := stage.Create(ext); err != nil {
					return err
				}
				host := &schemacmds.ExtensionHost{Engine: e, Auth: auth, Stage: stage}
				return extension.StockHstore.Stage(hstoreConflictStage{Stage: host, name: name, owner: auth.State.CurrentUser}, ext, ext.Owner)
			}()
			e.Mu.Unlock()
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || e.Catalog.State != before {
				t.Fatal("comparison conflict published a prefix", err)
			}
		})
	}
	t.Run("comparison_signature_and_graph_corruption", func(t *testing.T) {
		e, ext, base := hstoreUnitInstalled(t)
		signature := strconv.FormatUint(uint64(base.SQLType.ID()), 10)
		for _, corrupt := range []func(*catalog.State){
			func(s *catalog.State) {
				p := *s.MustProcedure(ext.Members[extension.HstoreComparisonProcedureStart])
				p.Signature = "25," + catalog.ProcedureSignature(signature)
				s.SetDescriptor(p.Address(), &p)
			},
			func(s *catalog.State) {
				p := *s.MustProcedure(ext.Members[extension.HstoreComparisonProcedureStart])
				p.Signature = catalog.ProcedureSignature(signature + ",25")
				s.SetDescriptor(p.Address(), &p)
			},
			func(s *catalog.State) {
				p := *s.MustProcedure(ext.Members[extension.HstoreComparisonProcedureStart+6])
				p.Implementation = extension.StockCitext
				s.SetDescriptor(p.Address(), &p)
			},
			func(s *catalog.State) {
				o, _ := s.Operator(ext.Members[extension.HstoreComparisonOperatorStart])
				n := *o
				n.Commutator = ext.Members[extension.HstoreComparisonOperatorStart+1].ObjectID
				s.SetDescriptor(n.Address(), &n)
			},
			func(s *catalog.State) {
				c, _ := e.Catalog.Read(s).OperatorClass(ext.Members[extension.HstoreBtreeClassSlot].ObjectID)
				n := *c
				n.Extension = ids.ObjectAddress{}
				s.SetDescriptor(n.Address(), &n)
			},
			func(s *catalog.State) {
				f, _ := e.Catalog.Read(s).OperatorFamily(ext.Members[extension.HstoreBtreeFamilySlot])
				n := *f
				n.Method = 405
				s.SetDescriptor(n.Address(), &n)
			},
			func(s *catalog.State) {
				m, _ := e.Catalog.Read(s).FamilySupport(ext.Members[extension.HstoreBtreeFamilySlot].ObjectID, ids.OID(base.SQLType.ID()), ids.OID(base.SQLType.ID()), 1)
				n := *m
				n.Function = ext.Members[extension.HstoreComparisonProcedureStart].ObjectID
				s.SetDescriptor(n.Address(), &n)
			},
		} {
			candidate := catalog.CloneState(e.Catalog.State)
			corrupt(candidate)
			if err := extension.StockHstore.ValidateExtension(e.Catalog.Read(candidate), ext); err == nil {
				t.Fatal("corrupt comparison manifest admitted")
			}
		}
	})
}
