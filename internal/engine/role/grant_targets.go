package role

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// objectGrantTargets resolves the targets of GRANT or REVOKE on objects, in
// statement order: parameters (deduplicated), named objects, routines, then
// every matching object of each ALL ... IN SCHEMA schema in OID order.
func objectGrantTargets(h Host, auth ddl.Authority, s *parser.ObjectGrant) ([]catalog.Descriptor, error) {
	view := h.AccessView(auth)
	targets := make([]catalog.Descriptor, 0, len(s.Relations)+len(s.Routines))
	for _, name := range s.Relations {
		if err := h.Context().Err(); err != nil {
			return nil, err
		}
		if s.Kind == "parameter" {
			key, err := h.CanonicalParameter(catalog.ObjectNameText(name))
			if err != nil {
				return nil, err
			}
			duplicate := false
			for _, target := range targets {
				if target.Name() == key {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			descriptor, err := h.GrantParameter(auth, key)
			if err != nil {
				return nil, err
			}
			targets = append(targets, descriptor)
			continue
		}
		ref, err := grantObject(h, auth, name, s.Kind)
		if err != nil {
			return nil, err
		}
		targets = append(targets, view.Catalog.Get(ref.Address))
	}
	for _, target := range s.Routines {
		if err := h.Context().Err(); err != nil {
			return nil, err
		}
		ref, err := grantRoutine(h, auth, target, s.Kind)
		if err != nil {
			return nil, err
		}
		targets = append(targets, view.Catalog.Get(ref.Address))
	}
	for _, name := range s.Schemas {
		if err := h.Context().Err(); err != nil {
			return nil, err
		}
		addr := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Text})
		if name.Text == "pg_temp" {
			addr = auth.TemporaryNamespace()
		}
		if addr == (ids.ObjectAddress{}) {
			return nil, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, name.Text))
		}
		ref := view.Ref(addr)
		if err := view.Check(auth, access.Requirement{Object: ref, Privileges: authorization.UsagePrivilege}); err != nil {
			return nil, err
		}
		start := len(targets)
		for _, descriptor := range view.Catalog.Descriptors() {
			if err := h.Context().Err(); err != nil {
				return nil, err
			}
			if procedure, ok := descriptor.(*catalog.Procedure); ok {
				if procedure.Namespace() == addr && routine.GrantRoutineKindMatches(s.Kind, procedure.RoutineKind) {
					targets = append(targets, descriptor)
				}
				continue
			}
			relation, ok := descriptor.(*catalog.Relation)
			matches := ok && (s.Kind == "" || s.Kind == "sequence") && relation.Namespace() == addr && !relation.VirtualView
			if matches && s.Kind == "sequence" {
				matches = relation.RelationKind == catalog.SequenceRelation
			} else if matches {
				matches = relation.RelationKind == catalog.TableRelation || relation.RelationKind == catalog.PartitionedRelation || relation.RelationKind == catalog.ViewRelation || relation.RelationKind == catalog.MaterializedViewRelation || relation.RelationKind == catalog.ForeignRelation
			}
			if matches {
				targets = append(targets, descriptor)
			}
		}
		// Catalog enumeration must not expose Go map iteration as notice order.
		sort.Slice(targets[start:], func(i, j int) bool { return targets[start+i].Address().ObjectID < targets[start+j].Address().ObjectID })
	}
	return targets, nil
}

// grantObject resolves a named GRANT target of kind: a relation, tablespace,
// schema, type or domain.
func grantObject(h Host, auth ddl.Authority, name parser.ObjectName, kind string) (ids.ObjectRef, error) {
	view := h.AccessView(auth)
	if kind == "large object" {
		text := catalog.ObjectNameText(name)
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil || n < -2147483648 || n > 4294967295 {
			return ids.ObjectRef{}, sqlError("22003", fmt.Sprintf("value %q is out of range for type oid", text))
		}
		a := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: ids.OID(uint32(n))}
		if view.Catalog.Get(a) == nil {
			return ids.ObjectRef{}, sqlError("42704", fmt.Sprintf("large object %d does not exist", a.ObjectID))
		}
		return view.Ref(a), nil
	}
	if kind == "" || kind == "sequence" {
		return h.ResolveRelation(auth, name)
	}
	if kind == "tablespace" {
		return ddl.TablespaceRef(view, catalog.ObjectNameText(name))
	}
	var address ids.ObjectAddress
	if kind == "schema" {
		if len(name.Parts) == 1 {
			address = view.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Parts[0].Text})
			if name.Parts[0].Text == "pg_temp" {
				address = auth.TemporaryNamespace()
			}
		}
		if address == (ids.ObjectAddress{}) {
			return ids.ObjectRef{}, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, catalog.ObjectNameText(name)))
		}
	} else {
		path, err := h.SearchNamespaces(auth, name, ids.TypeClassID)
		if err != nil {
			return ids.ObjectRef{}, err
		}
		for _, namespace := range path {
			found := view.Catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: name.Parts[len(name.Parts)-1].Text})
			if len(found) == 1 {
				address = found[0]
				break
			}
		}
		if address == (ids.ObjectAddress{}) {
			return ids.ObjectRef{}, sqlError("42704", fmt.Sprintf(`type "%s" does not exist`, catalog.ObjectNameText(name)))
		}
		t := view.Catalog.MustType(address)
		if kind == "domain" && !t.SQLType.IsDomain() {
			return ids.ObjectRef{}, sqlError("42809", fmt.Sprintf(`"%s" is not a domain`, catalog.ObjectNameText(name)))
		}
		if t.SQLType.IsArray() {
			diagnostic := sqlError("0LP01", "cannot set privileges of array types").(*sqlstate.Error)
			diagnostic.Hint = "Set the privileges of the element type instead."
			return ids.ObjectRef{}, diagnostic
		}
	}
	return view.Ref(address), nil
}

// grantRoutine resolves a GRANT routine target of kind (function, procedure
// or routine) on the function path.
func grantRoutine(h Host, auth ddl.Authority, target parser.GrantRoutineTarget, kind string) (ids.ObjectRef, error) {
	view := h.AccessView(auth)
	namespaces, err := h.SearchNamespaces(auth, target.Name, ids.ProcedureClassID)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	path := analyze.NamespaceSelection{Path: namespaces}
	var signature catalog.ProcedureSignature
	var exact *catalog.ProcedureSignature
	var inputTypes [100]ids.OID
	inputCount := 0
	if target.SignatureSpecified {
		types, err := h.ArgumentTypes(auth)
		if err != nil {
			return ids.ObjectRef{}, err
		}
		var encoded strings.Builder
		for _, argument := range target.Arguments {
			if argument.Mode == "out" {
				continue
			}
			if err := h.Context().Err(); err != nil {
				return ids.ObjectRef{}, err
			}
			inputCount++
			if inputCount > len(inputTypes) {
				noun := "functions"
				if kind == "procedure" {
					noun = "procedures"
				}
				return ids.ObjectRef{}, sqlError("54023", noun+" cannot have more than 100 arguments")
			}
			id, err := types(h.Context(), argument.Type)
			if err != nil {
				return ids.ObjectRef{}, err
			}
			if encoded.Len() != 0 {
				encoded.WriteByte(',')
			}
			var digits [20]byte
			encoded.Write(strconv.AppendUint(digits[:0], uint64(id), 10))
			inputTypes[inputCount-1] = ids.OID(id)
		}
		signature = catalog.ProcedureSignature(encoded.String())
		exact = &signature
	}
	name := target.Name.Parts[len(target.Name.Parts)-1].Text
	var selected catalog.RoutineIdentity
	var count int
	if !target.SignatureSpecified && kind == "procedure" {
		// With no argument list PostgreSQL filters by SQL kind before deciding
		// uniqueness. Native currently has no user-procedure creator/runtime.
		for _, namespace := range namespaces {
			for _, descriptor := range view.Catalog.Descriptors() {
				p, ok := descriptor.(*catalog.Procedure)
				if ok && p.Namespace() == namespace && p.Name() == name && p.RoutineKind == catalog.ProcedureRoutine {
					selected = catalog.RoutineIdentity{p.Address().ObjectID, p.Namespace().ObjectID, p.Name(), p.Signature}
					count++
				}
			}
		}
	} else {
		selected, count = regobj.SelectRoutine(view.Catalog, path, name, exact)
	}
	noun := "function"
	if kind == "procedure" {
		noun = "procedure"
	}
	display := catalog.ObjectNameText(target.Name)
	if count == 0 {
		if !target.SignatureSpecified {
			return ids.ObjectRef{}, sqlError("42883", fmt.Sprintf(`could not find a %s named "%s"`, noun, display))
		}
		display += "(" + grantRoutineArgumentDisplay(view, auth, inputTypes[:inputCount]) + ")"
		return ids.ObjectRef{}, sqlError("42883", noun+" "+display+" does not exist")
	}
	if count > 1 {
		diagnostic := sqlError("42725", fmt.Sprintf(`%s name "%s" is not unique`, kind, display)).(*sqlstate.Error)
		if !target.SignatureSpecified {
			diagnostic.Hint = "Specify the argument list to select the " + kind + " unambiguously."
		}
		return ids.ObjectRef{}, diagnostic
	}
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: selected.ID}
	p, ok := view.Catalog.Procedure(address)
	if !ok {
		// Lookup-only inventory cannot manufacture an owning ACL descriptor or
		// an execution capability.
		return ids.ObjectRef{}, sqlError("0A000", "routine privileges are unsupported for unimplemented routines in embedded mode")
	}
	if !routine.GrantRoutineKindMatches(kind, p.RoutineKind) {
		return ids.ObjectRef{}, sqlError("42809", display+"("+grantRoutineArgumentDisplay(view, auth, inputTypes[:inputCount])+") is not a "+noun)
	}
	return view.Ref(address), nil
}

// grantRoutineArgumentDisplay renders the argument types of a missing or
// mismatched GRANT routine target, qualifying user types the path hides.
func grantRoutineArgumentDisplay(view access.View, env access.Environment, inputs []ids.OID) string {
	path, _ := env.ReferencePath()
	var display strings.Builder
	for i, id := range inputs {
		if i != 0 {
			display.WriteString(", ")
		}
		typ := view.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
		if typ.SQLType.IsUserDefined() {
			display.WriteString(catalog.TypeDiagnosticNameAt(view.Catalog, typ, path))
		} else {
			display.WriteString(typ.SQLType.DiagnosticName())
		}
	}
	return display.String()
}
