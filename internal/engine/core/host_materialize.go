package core

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// Language events can add associations only for a new language identity or an
// admitted changed route. They never recreate retired directional procedures.
func (h DDLHost) MaterializeHostConversions() error {
	var languages []ids.ObjectAddress
	for address, descriptor := range h.CatalogStage.Descriptors() {
		language, ok := descriptor.(*catalog.Language)
		if !ok {
			continue
		}
		old, _ := h.CatalogStage.Base.Get(address).(*catalog.Language)
		if old == nil || old.Host != language.Host {
			languages = append(languages, address)
		}
	}
	if len(languages) == 0 {
		return nil
	}
	return h.E.materializeHostConversions(h.CatalogStage, false, false, languages)
}

// Only a genuinely new hstore installation creates its generation's directions.
// IF NOT EXISTS, alias creation and ordinary declaration resolution cannot
// resurrect a dropped registered SQL declaration.
func (e *Engine) MaterializeHostConversions(stage *CatalogStage) error {
	for address, descriptor := range stage.Descriptors() {
		ext, ok := descriptor.(*catalog.Extension)
		if ok && stage.Base.Get(address) == nil && ext.Installation != nil && ext.Installation.Implementation == extension.StockHstore {
			return e.materializeHostConversions(stage, true, true, nil)
		}
	}
	return nil
}
func hostMaterializationTypes(read catalog.Read, hstoreOnly bool) (map[host.Type]*sqltype.Descriptor, error) {
	types := make(map[host.Type]*sqltype.Descriptor)
	if !hstoreOnly {
		for _, id := range []sqltype.ID{sqltype.Bool, sqltype.Int4, sqltype.Int8, sqltype.Text} {
			if typ, ok := read.LookupType(id); ok {
				types[catalog.HostLogicalType(typ)] = typ
			}
		}
	}
	for _, descriptor := range read.Descriptors() {
		typ, ok := descriptor.(*catalog.Type)
		if !ok || !typ.SQLType.IsHstore() {
			continue
		}
		// Installation provenance authenticates the type independently of its
		// current extension membership. Replay temporarily detaches genuine
		// types before restoring membership, while aliases still need pairs.
		if err := (stored.TypeProvider{}).Validate(read, typ); err != nil {
			return nil, err
		}
		types[host.Hstore] = typ.SQLType
	}
	return types, nil
}

// materializeHostConversions is pure descriptor work in the existing candidate.
// The caller owns Engine.Mu (or exclusive prepublication construction).
func (e *Engine) materializeHostConversions(stage *CatalogStage, createDirections, hstoreOnly bool, languages []ids.ObjectAddress) error {
	if len(e.Catalog.HostRuntimes) == 0 {
		return nil
	}
	read := e.StageRead(stage)
	selected := func(address ids.ObjectAddress) bool {
		if languages == nil {
			return true
		}
		for _, a := range languages {
			if a == address {
				return true
			}
		}
		return false
	}
	for _, descriptor := range read.Descriptors() {
		pair, ok := descriptor.(*catalog.Transform)
		if !ok || !selected(pair.Language) {
			continue
		}
		language, ok := read.Get(pair.Language).(*catalog.Language)
		if ok && language.Host == pair.Runtime {
			continue
		}
		if err := stage.RemoveObjectLocked(e.Catalog.Domain.Ref(stage.Base.Epoch(), pair.Address()), false); err != nil {
			return err
		}
	}
	read = e.StageRead(stage)
	types, err := hostMaterializationTypes(read, hstoreOnly)
	if err != nil {
		return err
	}
	owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	for _, runtime := range e.Catalog.HostRuntimes {
		var handler ids.ObjectAddress
		for _, descriptor := range read.Descriptors() {
			if p, ok := descriptor.(*catalog.Procedure); ok && p.HostHandler == runtime {
				handler = p.Address()
				break
			}
		}
		if handler.ObjectID == 0 {
			continue
		}
		for _, definition := range runtime.Definition().Conversions {
			typ := types[definition.Type]
			if typ == nil {
				continue
			}
			token := runtime.Conversion(definition.ID)
			var from, to ids.ObjectAddress
			for _, direction := range []struct {
				name          host.Name
				present, from bool
			}{{definition.FromName, definition.FromSQL != nil, true}, {definition.ToName, definition.ToSQL != nil, false}} {
				if !direction.present {
					continue
				}
				var existing *catalog.Procedure
				for _, descriptor := range read.Descriptors() {
					if p, ok := descriptor.(*catalog.Procedure); ok && p.HostConversion != nil && p.HostConversion.Token == token && p.HostConversion.Type == typ && p.HostConversion.From == direction.from {
						existing = p
						break
					}
				}
				if existing == nil && createDirections {
					ns := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: direction.name.Schema})
					if ns.ObjectID == 0 {
						oid, err := e.Catalog.AllocateOIDLocked()
						if err != nil {
							return err
						}
						ns = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: oid}
						revision, err := e.Catalog.RevisionLocked()
						if err != nil {
							return err
						}
						if err = stage.Create(&catalog.Namespace{ObjectAddress: ns, ObjectRevision: revision, ObjectName: direction.name.Schema, Owner: owner}); err != nil {
							return err
						}
						read = e.StageRead(stage)
					}
					key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ns, Name: direction.name.Name, Signature: "2281"}
					if read.NameAddress(key).ObjectID != 0 {
						return fmt.Errorf("stuntgres: registered host conversion name is occupied")
					}
					oid, err := e.Catalog.AllocateOIDLocked()
					if err != nil {
						return err
					}
					revision, err := e.Catalog.RevisionLocked()
					if err != nil {
						return err
					}
					result := ids.OID(typ.ID())
					if direction.from {
						result = 2281
					}
					existing = &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectRevision: revision, ObjectNamespace: ns, ObjectName: direction.name.Name, Signature: "2281", RoutineKind: catalog.FunctionRoutine, Result: result, Owner: owner, Volatility: expr.Volatile, Parallel: 'u', Cost: 100, Display: catalog.RoutineDisplay{Language: catalog.GoRoutineLanguage, Source: definition.ID}, HostConversion: &catalog.HostConversion{Token: token, Type: typ, Handler: handler, From: direction.from}}
					if err = stage.Create(existing); err != nil {
						return err
					}
					read = e.StageRead(stage)
				}
				if existing != nil {
					if direction.from {
						from = existing.Address()
					} else {
						to = existing.Address()
					}
				}
			}
			if !definition.Bootstrap || from.ObjectID == 0 && to.ObjectID == 0 {
				continue
			}
			for _, descriptor := range read.Descriptors() {
				language, ok := descriptor.(*catalog.Language)
				if !ok || language.Host != runtime || !selected(language.Address()) {
					continue
				}
				key := ids.NameKey{ClassID: ids.TransformClassID, Name: fmt.Sprintf("%d,%d", typ.ID(), language.Address().ObjectID)}
				if old := read.Get(read.NameAddress(key)); old != nil {
					pair, ok := old.(*catalog.Transform)
					if !ok || pair.Runtime != runtime || pair.Conversion != token || pair.SQLType != typ {
						return fmt.Errorf("stuntgres: incompatible existing host conversion pair")
					}
					continue
				}
				oid, err := e.Catalog.AllocateOIDLocked()
				if err != nil {
					return err
				}
				revision, err := e.Catalog.RevisionLocked()
				if err != nil {
					return err
				}
				pair := &catalog.Transform{ObjectAddress: ids.ObjectAddress{ClassID: ids.TransformClassID, ObjectID: oid}, ObjectRevision: revision, Type: ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}, SQLType: typ, Language: language.Address(), From: from, To: to, Runtime: runtime, Conversion: token}
				if err = stage.Create(pair); err != nil {
					return err
				}
				read = e.StageRead(stage)
			}
		}
	}
	return nil
}
