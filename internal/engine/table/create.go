package table

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// NewRelation allocates a table descriptor: its OID, then its revision. The
// table identity owner is shared by declared and query-derived schemas and
// composite types. The caller transfers a private column slice; source
// definitions are not mutated.
func NewRelation(a ddl.Allocator, ns ids.ObjectAddress, name string, owner authorization.PrincipalRef, columns []catalog.Column, index map[string]int) (*catalog.Relation, error) {
	oid, err := a.AllocateOID()
	if err != nil {
		return nil, err
	}
	revision, err := a.Revision()
	if err != nil {
		return nil, err
	}
	if index == nil {
		index = make(map[string]int, len(columns))
		for i, column := range columns {
			index[column.Name] = i
		}
	}
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: ns, ObjectName: name, ObjectRevision: revision, Owner: owner, Columns: columns, Index: index, Attributes: make([]int32, len(columns))}
	d.ColumnMeta.HighWater = int32(len(columns))
	for i := range columns {
		d.Attributes[i] = int32(i + 1)
		d.Columns[i].OriginTable = uint32(oid)
		d.Columns[i].OriginAttribute = int16(i + 1)
	}
	return d, nil
}

// AssignDefinitionOIDs gives every NOT NULL, DEFAULT, generation, CHECK,
// UNIQUE and FOREIGN KEY definition of d without an OID one, in that order.
func AssignDefinitionOIDs(a ddl.Allocator, d *catalog.Relation) error {
	for i := range d.Constraints.NotNull {
		if d.Constraints.NotNull[i].OID == 0 {
			id, err := a.AllocateOID()
			if err != nil {
				return err
			}
			d.Constraints.NotNull[i].OID = id
		}
	}
	for _, attr := range d.Attributes {
		def, ok := d.ColumnMeta.Defaults[attr]
		if ok && def.OID == 0 {
			id, err := a.AllocateOID()
			if err != nil {
				return err
			}
			def.OID = id
			d.ColumnMeta.Defaults[attr] = def
		}
	}
	for _, attr := range d.Attributes {
		definition, exists := d.ColumnMeta.Generations[attr]
		if exists && definition.OID == 0 {
			id, err := a.AllocateOID()
			if err != nil {
				return err
			}
			definition.OID = id
			d.ColumnMeta.Generations[attr] = definition
		}
	}
	for i := range d.Constraints.Check {
		if d.Constraints.Check[i].OID == 0 {
			id, err := a.AllocateOID()
			if err != nil {
				return err
			}
			d.Constraints.Check[i].OID = id
		}
	}
	for i := range d.Constraints.Unique {
		if d.Constraints.Unique[i].OID == 0 {
			id, err := a.AllocateOID()
			if err != nil {
				return err
			}
			d.Constraints.Unique[i].OID = id
		}
	}
	for i := range d.Constraints.ForeignKeys {
		if d.Constraints.ForeignKeys[i].OID == 0 {
			id, err := a.AllocateOID()
			if err != nil {
				return err
			}
			d.Constraints.ForeignKeys[i].OID = id
		}
		if err := AssignForeignKeyTriggerOIDs(a, &d.Constraints.ForeignKeys[i]); err != nil {
			return err
		}
	}
	return nil
}

// StageCreateTable stages CREATE TABLE s (no PARTITION clause) as a table
// of namespace ns owned by owner: the descriptor, its row type, inheritance,
// NOT NULL names, identity and serial sequences, LIKE, defaults,
// generations, CHECKs, constraint OIDs, UNIQUE and EXCLUDE indexes and
// FOREIGN KEYs, in that order. foreignTargets, when given, are the FOREIGN
// KEY parents analysis resolved.
// kind is the final consumer kind; its owner completes kind-specific payloads
// after this shared staging pass. Only physical table creation selects an AM.
func StageCreateTable(h Host, auth ddl.Authority, s *parser.CreateTable, ns ids.ObjectAddress, owner authorization.PrincipalRef, input sqltype.InputEnvironment, foreignTargets []ids.ObjectRef, kind catalog.RelationKind) error {
	auth = h.Recapture(auth, h.StageBase())
	if namespace, ok := h.StageBase().Namespace(ns); ok {
		auth = h.StagedTemporary(auth, namespace)
	}
	if h.StageChanged() {
		auth = h.Recapture(auth, h.StageRead())
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: name}
	_, baseExists := h.StageBase().LookupName(key)
	_, stagedExists := h.NameBinding(key)
	if baseExists || stagedExists {
		if s.IfNotExists {
			h.EmitNotice(session.LevelNotice, session.ExistingRelation(name))
			return nil
		}
		return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
	}
	available, err := h.DisplaceAssociatedArray(ns, name)
	if err != nil {
		return err
	}
	if !available {
		diagnostic := sqlError("42710", fmt.Sprintf(`type "%s" already exists`, name)).(*sqlstate.Error)
		diagnostic.Hint = "A relation has an associated type of the same name, so you must use a name that doesn't conflict with any existing type."
		return diagnostic
	}
	if h.StageChanged() {
		auth = h.Recapture(auth, h.StageRead())
	}
	b, err := h.DescribeCreateTable(auth, s, input)
	if err != nil {
		return err
	}
	input.EnumMemberSafe = b.Resolvers.EnumMemberSafe
	view := h.AccessView(auth)
	for _, column := range b.Create.Columns {
		ref := view.Ref(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(column.Usage.Type.ID())})
		if b.Create.OfType == nil {
			if err := view.CanUse(auth, ref); err != nil {
				return err
			}
		}
		h.AddDomainBase(view.Catalog.Get(ref.Address))
	}
	d, err := NewRelation(h, ns, name, owner, b.Create.Columns, b.Create.ColumnIndex)
	if err != nil {
		return err
	}
	d.Persistence = s.Persistence
	if kind == catalog.TableRelation {
		d.TableMethod, err = ResolveTableMethod(view.Catalog, h.DefaultTableAccessMethod())
		if err != nil { return err }
	}
	if b.Create.OfType != nil {
		d.OfType = b.Create.OfType.Address()
		h.AddDomainBase(b.Create.OfType)
	}
	d.Security.ACL = h.CreationACL(owner, ns, authorization.DefaultTables)
	if err := h.StageRowType(d); err != nil {
		return err
	}
	d.OnCommit = s.OnCommit
	d.Constraints.NotNull = b.Create.NotNull
	if err := StageInheritance(h, auth, d, b); err != nil {
		return err
	}
	if err := NameCreateNotNull(d, view.Catalog); err != nil {
		return err
	}
	if err := StageCreateIdentities(h, auth, d, s); err != nil {
		return err
	}
	if err := StageCreateSerials(h, d, b.Create.Declarations); err != nil {
		return err
	}
	if err := StageLikeColumns(h, auth, d, b.Create.Likes); err != nil {
		return err
	}
	// All owned sequences and their defaults share the private CREATE candidate.
	auth = h.Recapture(auth, h.StageRead())
	view = h.AccessView(auth)
	// Preserve operation safety, but resolve diagnostic names lazily against
	// the staged catalog that owns these defaults, generations and CHECKs.
	input.TypeDiagnosticName = nil
	for slot, declaration := range b.Create.Declarations {
		if declaration.DefaultExpr != nil {
			delete(d.ColumnMeta.Defaults, d.Attributes[slot])
		}
	}
	defaults, err := h.BindColumnDefaults(auth, d.Columns, b.Create.Declarations, input, view.Catalog.MustNamespace(d.Namespace()).Temporary)
	if err != nil {
		return err
	}
	if d.ColumnMeta.Defaults == nil {
		d.ColumnMeta.Defaults = defaults
	} else {
		for attribute, definition := range defaults {
			d.ColumnMeta.Defaults[attribute] = definition
		}
	}
	if err := h.BindGenerations(auth, d, b.Create.Declarations, input); err != nil {
		return err
	}
	if err := BindChecks(h, auth, d, CreateCheckSyntax(s), input, false, nil); err != nil {
		return err
	}
	if err := StageLikeChecks(h, auth, d, b.Create.Likes); err != nil {
		return err
	}
	if err := stored.RefreshGenerationChecks(d, view.Catalog); err != nil {
		return err
	}
	if err := AssignDefinitionOIDs(h, d); err != nil {
		return err
	}
	// PostgreSQL resolves raw column modifiers again while building the
	// relation descriptor, without a parser state/source position.
	for _, element := range s.Elements {
		if column := element.Column; column != nil && len(column.TypeName.Modifiers) > 0 {
			i := d.Index[column.Name]
			// Temporal modifiers emit a second descriptor-build warning.
			if id := d.Columns[i].Usage.Type.ID(); id != sqltype.Interval && id != sqltype.Timestamp && id != sqltype.Time && id != sqltype.Timestamptz && id != sqltype.Timetz {
				continue
			}
			modifiers := make([]int32, len(column.TypeName.Modifiers))
			valid := true
			for j, modifier := range column.TypeName.Modifiers {
				if modifier.Integer == nil {
					valid = false
					break
				}
				modifiers[j] = *modifier.Integer
			}
			if !valid {
				continue
			}
			usage, warning, err := d.Columns[i].Usage.Type.ResolveModifiers(modifiers...)
			if err != nil {
				return err
			}
			d.Columns[i].Usage = usage
			if warning != nil {
				h.EmitNotice(session.LevelWarning, *warning)
			}
		}
	}
	for _, declaration := range b.Create.Uniques {
		if declaration.Source != nil {
			h.TableStage().CaptureLikeSource(declaration.Source)
		}
	}
	if err := index.StageCreateUniques(h, d, b.Create.Uniques); err != nil {
		return index.DefaultClassDiagnostic(h, auth, err)
	}
	if err := index.StageCreateExclusions(h, auth, input, d, s); err != nil {
		return err
	}
	if err := StageLikeIndexes(h, auth, d, b.Create.Likes); err != nil {
		return err
	}
	return StageCreateForeignKeys(h, auth, d, s, foreignTargets)
}

// CreateCheckSyntax collects the table and column CHECK constraints of
// syntax in declaration order.
func CreateCheckSyntax(syntax *parser.CreateTable) []*parser.CheckConstraint {
	var raw []*parser.CheckConstraint
	for _, element := range syntax.Elements {
		if element.Check != nil {
			raw = append(raw, element.Check)
		}
		if element.Column != nil {
			for _, c := range element.Column.Constraints {
				if c.Check != nil {
					raw = append(raw, c.Check)
				}
			}
		}
	}
	return raw
}
