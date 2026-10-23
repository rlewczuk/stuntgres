package typedef

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func rangeOptionName(raw parser.TypeName, subtype bool) parser.ObjectName {
	name := raw.Name
	if subtype && !raw.Quoted && raw.Schema == nil {
		name = CanonicalDeclarationTypeName(name)
	}
	parts := []parser.Identifier{{Text: name, Pos: raw.Pos}}
	if raw.Schema != nil {
		parts = []parser.Identifier{*raw.Schema, {Text: name, Pos: raw.Pos}}
	}
	return parser.ObjectName{Parts: parts}
}

func rangeSemanticError(err error) error {
	if d, ok := err.(*sqlstate.Error); ok {
		n := *d
		n.Position = ""
		return &n
	}
	return err
}

func StageCreateRangeLocked(h StageHost, auth ddl.Authority, s *parser.CreateRange) error {
	ns, err := h.ExistingCreationNamespace(auth, s.Name)
	if err != nil {
		return rangeSemanticError(err)
	}
	if err = h.CheckCreateIn(auth, ns); err != nil {
		return rangeSemanticError(err)
	}
	view := h.AccessView(auth)
	if err = access.CheckPersistentCreationNamespace(view.Catalog, ns.Address); err != nil {
		return err
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	available, err := h.DisplaceAssociatedArray(ns.Address, name)
	if err != nil {
		return err
	}
	if !available || RelationTypeNameOccupied(view.Catalog, ns.Address, name) {
		return sqlError("42710", fmt.Sprintf(`type "%s" already exists`, name))
	}
	var subtype sqltype.Usage
	var className, collationName, diffName *parser.ObjectName
	multiNS := ns
	multiName := ""
	seen := make(map[string]bool, len(s.Options))
	for _, option := range s.Options {
		key := option.Name.Text
		if seen[key] {
			return expr.ErrorAt("42601", "conflicting or redundant options", option.Name.Pos)
		}
		seen[key] = true
		object := rangeOptionName(option.Value, key == "subtype")
		switch key {
		case "subtype":
			t, lookupErr := h.NamedType(auth, object)
			if lookupErr != nil {
				return rangeSemanticError(lookupErr)
			}
			subtype = t.SQLType.Usage()
			if len(option.Value.ArrayBounds) > 0 || len(option.Value.Modifiers) > 0 {
				subtype, err = h.ResolveDomainBase(option.Value)
				if err != nil {
					return rangeSemanticError(err)
				}
			}
		case "subtype_opclass":
			className = &object
		case "collation":
			collationName = &object
		case "subtype_diff":
			diffName = &object
		case "multirange_type_name":
			multiNS, err = h.ExistingCreationNamespace(auth, object)
			if err != nil {
				return rangeSemanticError(err)
			}
			if err = h.CheckCreateIn(auth, multiNS); err != nil {
				return rangeSemanticError(err)
			}
			if err = access.CheckPersistentCreationNamespace(view.Catalog, multiNS.Address); err != nil {
				return err
			}
			multiName = object.Parts[len(object.Parts)-1].Text
		default:
			return sqlError("42601", fmt.Sprintf(`type attribute "%s" not recognized`, key))
		}
	}
	if subtype.Type == nil {
		return sqlError("42601", `type attribute "subtype" is required`)
	}
	if !subtype.Type.Concrete() {
		return sqlError("42804", "range subtype cannot be "+subtype.Type.Name())
	}
	if err = view.CanUse(auth, ddl.ObjectRef(h, auth, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(subtype.Type.ID())})); err != nil {
		return rangeSemanticError(err)
	}
	class, err := h.RangeClass(auth, subtype, className)
	if err != nil {
		return rangeSemanticError(err)
	}
	if collationName != nil && subtype.Collation.ID == 0 {
		return sqlError("42809", "range collation specified but subtype does not support collation")
	}
	subtype, err = h.ResolveColumnCollation(subtype, collationName)
	if err != nil {
		return rangeSemanticError(err)
	}
	ordering, err := sqltype.BindIndexOrdering(subtype, class)
	if err != nil {
		return err
	}
	var diff uint32
	if diffName != nil {
		id := strconv.FormatUint(uint64(subtype.Type.ID()), 10)
		procedure, lookupErr := h.LookupFunction(auth, *diffName, catalog.ProcedureSignature(id+","+id))
		if lookupErr != nil {
			return rangeSemanticError(lookupErr)
		}
		spelling := catalog.ObjectNameText(*diffName) + "(" + subtype.Type.DiagnosticName() + ", " + subtype.Type.DiagnosticName() + ")"
		if procedure == nil {
			return sqlError("42883", "function "+spelling+" does not exist")
		}
		if procedure.Result != 701 {
			return sqlError("42P17", "range subtype diff function "+spelling+" must return type double precision")
		}
		if procedure.Volatility != expr.Immutable {
			return sqlError("42P17", "range subtype diff function "+spelling+" must be immutable")
		}
		if err = ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, procedure.Address()), Privileges: authorization.ExecutePrivilege}); err != nil {
			return rangeSemanticError(err)
		}
		if procedure.Builtin == nil && procedure.SQL == nil {
			return sqlError("0A000", "range subtype diff function has no registered provider")
		}
		diff = uint32(procedure.Address().ObjectID)
	}
	automatic := multiName == ""
	if automatic {
		multiName = strings.Replace(name, "range", "multirange", 1)
		if multiName == name {
			multiName = name + "_multirange"
		}
		if len(multiName) > 63 {
			end := 63
			for !utf8.RuneStart(multiName[end]) {
				end--
			}
			multiName = multiName[:end]
		}
	}
	if multiNS.Address == ns.Address && multiName == name {
		d := sqlError("23505", `duplicate key value violates unique constraint "pg_type_typname_nsp_index"`).(*sqlstate.Error)
		d.Detail = fmt.Sprintf("Key (typname, typnamespace)=(%s, %d) already exists.", name, ns.Address.ObjectID)
		d.Schema, d.Table, d.Constraint = "pg_catalog", "pg_type", "pg_type_typname_nsp_index"
		return d
	}
	available, err = h.DisplaceAssociatedArray(multiNS.Address, multiName)
	if err != nil {
		return err
	}
	if !available || RelationTypeNameOccupied(view.Catalog, multiNS.Address, multiName) {
		d := sqlError("42710", fmt.Sprintf(`type "%s" already exists`, multiName)).(*sqlstate.Error)
		if automatic {
			d.Detail = fmt.Sprintf(`Failed while creating a multirange type for type "%s".`, name)
			d.Hint = `You can manually specify a multirange type name using the "multirange_type_name" attribute.`
		}
		return d
	}
	oid, err := h.AllocateOID()
	if err != nil {
		return err
	}
	multiOID, err := h.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := h.Revision()
	if err != nil {
		return err
	}
	qualified := func(namespace ids.ObjectAddress, name string) string {
		return catalog.DependencyIdentifier(view.Catalog.Get(namespace).Name()) + "." + catalog.DependencyIdentifier(name)
	}
	boolean, _ := view.Catalog.LookupType(sqltype.Bool)
	r, m, err := sqltype.NewRangeFamily(sqltype.ID(oid), name, qualified(ns.Address, name), sqltype.ID(multiOID), multiName, qualified(multiNS.Address, multiName), sqltype.RangeDefinition{Subtype: subtype, Class: class, Ordering: ordering, Diff: diff}, boolean)
	if err != nil {
		return err
	}
	members := [2]struct {
		typ   *sqltype.Descriptor
		ns    ids.ObjectAddress
		array sqltype.ID
	}{{typ: r, ns: ns.Address}, {typ: m, ns: multiNS.Address}}
	// Reserve both real names before choosing either autogenerated array name.
	for i := range members {
		member := &members[i]
		arrayOID, err := h.AllocateOID()
		if err != nil {
			return err
		}
		member.array = sqltype.ID(arrayOID)
		acl, synthetic := h.CreationTypeACL(view.State.CurrentUser, member.ns)
		if member.typ.IsMultirange() {
			acl, synthetic = authorization.DefaultBaseline(view.State.CurrentUser, authorization.DefaultTypes, false), true
		}
		if err = h.Stage().Create(&catalog.Type{SQLType: member.typ, ObjectNamespace: member.ns, ObjectRevision: revision, Owner: view.State.CurrentUser, ACL: acl, ACLSyntheticDefault: synthetic, Array: member.array}); err != nil {
			return err
		}
	}
	for _, member := range members {
		array, err := sqltype.Array(member.array, h.AssociatedArrayName(member.ns, member.typ.Name()), member.typ)
		if err != nil {
			return err
		}
		if err = h.Stage().Create(&catalog.Type{SQLType: array, ObjectNamespace: member.ns, ObjectRevision: revision, Owner: view.State.CurrentUser, ACL: authorization.DefaultBaseline(view.State.CurrentUser, authorization.DefaultTypes, false), ACLSyntheticDefault: true}); err != nil {
			return err
		}
	}
	if err = stageRangeConstructors(h, ns.Address, r, m); err != nil {
		return err
	}
	return h.Context().Err()
}
