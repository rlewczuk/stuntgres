package engine

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type ColumnDescriptor struct {
	Name                       string
	Usage                      sqltype.Usage
	Nullable, NullabilityKnown bool
	OriginTable                uint32
	OriginAttribute            int16
}

type StatementDescription struct {
	Parameters ParameterInfo
	Results    []ColumnDescriptor
	// Logical user identities belong to an owner/epoch, not merely a reused OID.
	catalogOwner *Engine
	catalogEpoch CatalogEpoch
	// Public signatures stay immutable. Invalidation refreshes temporal-now and
	// specialized groups atomically, preserving native logical literal identity.
	temporals        []temporalConstant
	composites       map[preparedCompositeKey]compositeOccurrence
	sequences        *sequenceAnalysis
	regtypes         *regtypeAnalysis
	analysis         *preparedAnalysis
	quotes           []quoteConstant
	quoteGeneration  uint64 // Published separately after successful quote preflight.
	planOwner        SessionToken
	planGeneration   uint64
	refreshTemporals bool // Candidate-only request; never published in a description.
}

type temporalConstant struct {
	offset       int
	scalar       int64
	typmod       int32
	typeID       sqltype.ID
	west         int32
	months, days int32
	typed        bool
	typedTypmod  int32
	value        sqltype.Value // immutable analyzed array, composite or enum literal
}

func refreshableTemporalCapture(c temporalConstant) bool {
	t := c.value.Type()
	if statementCatalogType(t) {
		return false // Logical compounds retain even their embedded temporal fields.
	}
	id := c.typeID
	if t != nil {
		for t.IsArray() {
			t = t.Element()
		}
		id = t.ID()
	}
	switch id {
	case sqltype.Date, sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval:
		return true
	}
	return false
}

// Catalog wrappers retain the existing class/namespace/revision/epoch ownership
// seam. A sqltype ID is a projection, not an authorization token.
type typeDescriptor struct {
	typ            *sqltype.Descriptor
	comment        string
	namespace      ObjectAddress
	revision       DefinitionRevision
	owner          authorization.PrincipalRef
	acl            authorization.ACLValue
	dimensions     int32
	array          sqltype.ID
	owningRelation ObjectAddress
}

func (d *typeDescriptor) Address() ObjectAddress {
	return ObjectAddress{ClassID: TypeClassID, ObjectID: OID(d.typ.ID())}
}
func (d *typeDescriptor) Revision() DefinitionRevision {
	if d.revision == 0 {
		return 1
	}
	return d.revision
}
func (d *typeDescriptor) Namespace() ObjectAddress {
	if d.namespace != (ObjectAddress{}) {
		return d.namespace
	}
	return ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}
}
func (d *typeDescriptor) Name() string   { return d.typ.Name() }
func (*typeDescriptor) Kind() ObjectKind { return TypeObject }

type typeProvider struct{}

func (typeProvider) ClassID() OID { return TypeClassID }
func (typeProvider) Validate(view CatalogRead, descriptor Descriptor) error {
	t, ok := descriptor.(*typeDescriptor)
	if !ok || t.typ == nil || t.typ.ID() == 0 {
		return fmt.Errorf("stuntgres: invalid type descriptor")
	}
	ns, ok := view.Descriptor(descriptor.Namespace())
	if !ok || ns.Kind() != NamespaceObject {
		return fmt.Errorf("stuntgres: type namespace is missing")
	}
	found := view.Lookup(descriptorNameKey(descriptor))
	if len(found) != 1 || found[0] != descriptor.Address() {
		return fmt.Errorf("stuntgres: invalid type identity")
	}
	if t.owningRelation != (ObjectAddress{}) {
		relation, ok := view.state.descriptors[t.owningRelation].(*relationDescriptor)
		if !ok || relation.namespace != t.Namespace() || relation.owner != t.owner {
			return fmt.Errorf("stuntgres: invalid row type ownership")
		}
		if !t.typ.IsArray() && (relation.rowType != t.typ.ID() || relation.name != t.Name()) {
			return CatalogConflict{}
		}
		if t.typ.IsArray() && relation.rowType != t.typ.Element().ID() {
			return CatalogConflict{}
		}
	}
	if t.typ.IsUserDefined() || t.typ.IsArray() && t.typ.Element().IsUserDefined() {
		if t.revision == 0 || t.owningRelation == (ObjectAddress{}) && relationTypeNameOccupied(view, t.Namespace(), t.Name()) {
			return CatalogConflict{}
		}
	}
	if t.typ.IsComposite() {
		relation, ok := view.state.descriptors[t.owningRelation].(*relationDescriptor)
		if !ok || len(relation.columns) != t.typ.CompositeLen() {
			return fmt.Errorf("stuntgres: missing composite backing definition")
		}
		for i, column := range relation.columns {
			member := t.typ.CompositeMember(i)
			actual, exists := view.LookupType(member.Usage.Type.ID())
			if !exists || actual != member.Usage.Type || member.Attribute != relation.attributes[i] || member.Name != column.Name || member.Usage != column.Usage {
				return fmt.Errorf("stuntgres: inconsistent composite member generation")
			}
		}
	}
	if t.typ.IsDomain() {
		base := t.typ.DomainBase()
		if err := validateUsageCollation(view, base); err != nil {
			return err
		}
		actual, exists := view.LookupType(base.Type.ID())
		if !exists || actual != base.Type || t.typ.Concrete() && (!base.Valid() || t.revision == 0) || t.dimensions < 0 {
			return fmt.Errorf("stuntgres: invalid domain base generation")
		}
	}
	if t.typ.IsArray() {
		element := t.typ.Element()
		actual, exists := view.LookupType(element.ID())
		if !exists || actual != element {
			return fmt.Errorf("stuntgres: invalid array element generation")
		}
		owner, exists := view.Descriptor(ObjectAddress{ClassID: TypeClassID, ObjectID: OID(element.ID())})
		if !exists || owner.(*typeDescriptor).array != t.typ.ID() || owner.Namespace() != t.Namespace() || owner.(*typeDescriptor).owner != t.owner {
			return fmt.Errorf("stuntgres: invalid associated array identity")
		}
	}
	if t.array != 0 {
		array, exists := view.LookupType(t.array)
		if !exists || !array.IsArray() || array.Element() != t.typ {
			return fmt.Errorf("stuntgres: invalid element array association")
		}
	}
	return nil
}

func (r CatalogRead) LookupType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	d, ok := r.Descriptor(ObjectAddress{ClassID: TypeClassID, ObjectID: OID(id)})
	if !ok {
		return nil, false
	}
	t, ok := d.(*typeDescriptor)
	if !ok {
		return nil, false
	}
	return t.typ, true
}

func (r CatalogRead) LookupArray(element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	if element == nil {
		return nil, false
	}
	owner, ok := r.Descriptor(ObjectAddress{ClassID: TypeClassID, ObjectID: OID(element.ID())})
	if !ok || owner.(*typeDescriptor).typ != element {
		return nil, false
	}
	array, ok := r.LookupType(owner.(*typeDescriptor).array)
	return array, ok && array.IsArray() && array.Element() == element
}

// Called only by the existing binder against its current coherent catalog.
func (e *Engine) resolveType(raw parser.TypeName, op *Operation) (sqltype.Usage, error) {
	u, err := e.resolveAnnotationType(raw, op, false)
	if err != nil {
		return sqltype.Usage{}, err
	}
	if physical := u.Type.BaseType(); physical.IsArray() && !storedArrayElement(physical.Element()) {
		return sqltype.Usage{}, typeDiagnostic(raw, "0A000", "unsupported column type")
	}
	if u.Type.IsArray() && u.Type.Element().IsDomain() {
		auth, err := e.authorizationLocked(op)
		if err != nil {
			return sqltype.Usage{}, err
		}
		if err := auth.CanUse(ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: ObjectAddress{ClassID: TypeClassID, ObjectID: OID(u.Type.ID())}}); err != nil {
			return sqltype.Usage{}, err
		}
	}
	return u, nil
}

// Query-derived schemas can store builtin CHAR/XID without admitting new raw
// declaration owners. Only the isolated Restore caller reconstructs those
// private declarations; regclass uses ordinary public type admission.
func (e *Engine) resolveTableColumnType(raw parser.TypeName, op *Operation) (sqltype.Usage, error) {
	u, err := e.resolveType(raw, op)
	privateScalar := sqltype.ID(0)
	if err == nil && u.Type.ContainsRegproc() {
		return sqltype.Usage{}, typeDiagnostic(raw, "0A000", "unsupported column type")
	}
	if err != nil && len(raw.Modifiers) == 0 && len(raw.ArrayBounds) == 0 && (raw.Schema == nil || raw.Schema.Text == "pg_catalog") {
		if raw.Name == "char" && raw.Quoted {
			privateScalar = sqltype.Char
		} else if raw.Name == "xid" {
			privateScalar = sqltype.Xid
		}
	}
	if privateScalar == 0 {
		return u, err
	}
	if op == nil || !op.restoreTableDestination {
		return u, err
	}
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Code != "0A000" {
		return u, err
	}
	auth, authErr := e.authorizationLocked(op)
	if authErr != nil {
		return sqltype.Usage{}, authErr
	}
	bootstrap, authErr := auth.roles.Descriptor(auth.state.Authenticated)
	if authErr != nil || !bootstrap.Superuser || e.importTargets == nil {
		return sqltype.Usage{}, fmt.Errorf("stuntgres: invalid trusted restore column authority")
	}
	typ, ok := auth.catalog.LookupType(privateScalar)
	if !ok || !typ.Concrete() || typ.IsUserDefined() {
		return sqltype.Usage{}, fmt.Errorf("stuntgres: missing builtin restore column type")
	}
	return typ.Usage(), nil
}

func storedArrayElement(typ *sqltype.Descriptor) bool {
	physical := typ.BaseType()
	if sqltype.IsNetwork(physical.ID()) {
		return false
	}
	if physical.IsArray() {
		return storedArrayElement(physical.Element())
	}
	return sqltype.StoredScalarCastType(physical.ID()) || physical.ID() == sqltype.Regclass
}

func (e *Engine) resolveAnnotationType(raw parser.TypeName, op *Operation, unknownNull bool) (sqltype.Usage, error) {
	return e.resolveAnnotationTypeMode(raw, op, unknownNull, false)
}

func (e *Engine) resolveAnnotationTypeMode(raw parser.TypeName, op *Operation, unknownNull, domainBase bool) (sqltype.Usage, error) {
	return e.resolveAnnotationTypeInput(raw, op, unknownNull, domainBase, false)
}

// String roles admit internal CHAR without admitting it as a column or a
// general cast target. Lookup, permissions and modifier checks stay type-owned.
func (e *Engine) resolveStringAnnotationType(raw parser.TypeName, op *Operation, unknownNull bool) (sqltype.Usage, error) {
	return e.resolveAnnotationTypeInput(raw, op, unknownNull, false, true)
}

func (e *Engine) resolveAnnotationTypeInput(raw parser.TypeName, op *Operation, unknownNull, domainBase, stringInput bool) (sqltype.Usage, error) {
	name := raw.Name
	if !raw.Quoted && raw.Schema == nil {
		switch name {
		case "smallint":
			name = "int2"
		case "integer":
			name = "int4"
		case "bigint":
			name = "int8"
		case "boolean":
			name = "bool"
		case "real":
			name = "float4"
		case "float", "double precision":
			name = "float8"
		case "decimal", "dec":
			name = "numeric"
		case "char", "character":
			name = "bpchar"
		case "character varying":
			name = "varchar"
		case "bit varying":
			name = "varbit"
		}
	}
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return sqltype.Usage{}, err
	}
	view := CatalogRead{owner: e, state: auth.catalog.state}
	object := parser.ObjectName{Parts: []parser.Identifier{{Text: name, Pos: raw.Pos}}}
	if raw.Schema != nil {
		object.Parts = []parser.Identifier{*raw.Schema, {Text: name, Pos: raw.Pos}}
	}
	path, err := e.lookupNamespaces(auth, object, typeLookup)
	if err != nil {
		return sqltype.Usage{}, err
	}
	var found []ObjectAddress
	for i := range path.count() {
		found = view.Lookup(NameKey{ClassID: TypeClassID, Namespace: path.at(i), Name: name})
		if len(found) != 0 {
			break
		}
	}
	if len(found) != 1 {
		if domainBase && raw.Name == "record" && (raw.Schema == nil || raw.Schema.Text == "pg_catalog") {
			return sqltype.Usage{}, typeDiagnostic(raw, "42804", `"record" is not a valid base type for a domain`)
		}
		if raw.Schema == nil && unsupportedColumnType(raw) {
			return sqltype.Usage{}, typeDiagnostic(raw, "0A000", "unsupported column type")
		}
		return sqltype.Usage{}, typeDiagnostic(raw, "42704", fmt.Sprintf(`type "%s" does not exist`, objectNameText(object)))
	}
	d, _ := view.LookupType(sqltype.ID(found[0].ObjectID))
	if domainBase && (!d.Concrete() || d.IsArray() && d.Element().IsEnum() || d.ID() == sqltype.Void || d.ContainsRegproc()) {
		return sqltype.Usage{}, typeDiagnostic(raw, "42804", fmt.Sprintf(`"%s" is not a valid base type for a domain`, d.Name()))
	}
	if unknownNull && d.ID() == sqltype.Unknown && len(raw.Modifiers) == 0 {
		return d.Usage(), nil
	}
	if !d.Concrete() || d.IsArray() && d.Element().IsEnum() || d.ID() == sqltype.Char && !stringInput || d.ID() == sqltype.Void || d.ID() == sqltype.Xid {
		return sqltype.Usage{}, typeDiagnostic(raw, "0A000", "unsupported column type")
	}
	u := d.Usage()
	if !raw.Quoted && (raw.Name == "char" || raw.Name == "character") && len(raw.Modifiers) == 0 {
		u.Typmod = 5
	}
	if !raw.Quoted && raw.Schema == nil && raw.Name == "bit" && len(raw.Modifiers) == 0 {
		u.Typmod = 1
	}
	if len(raw.Modifiers) > 0 {
		if d.IsUserDefined() || d.IsDomain() {
			return sqltype.Usage{}, typeDiagnostic(raw, "42601", fmt.Sprintf(`type modifier is not allowed for type "%s"`, objectNameText(object)))
		}
		if err := d.CheckModifierSupport(); err != nil {
			return sqltype.Usage{}, inputErrorAt(err, raw.Pos)
		}
	}
	modifiers := make([]int32, len(raw.Modifiers))
	for i, modifier := range raw.Modifiers {
		if modifier.NonSimple || modifier.UnaryPlus {
			return sqltype.Usage{}, typeDiagnostic(raw, "42601", "type modifiers must be simple constants or identifiers")
		}
		if modifier.Integer != nil {
			modifiers[i] = *modifier.Integer
			continue
		}
		integer, _ := view.LookupType(sqltype.Int4)
		ctx := context.Background()
		if op != nil {
			ctx = op.Context()
		}
		value, err := integer.Input(ctx, modifier.Raw, -1)
		if err != nil {
			return sqltype.Usage{}, inputErrorAt(err, raw.Pos)
		}
		n, err := value.Integer()
		if err != nil {
			return sqltype.Usage{}, err
		}
		modifiers[i] = int32(n)
	}
	if len(modifiers) > 0 {
		var warning *sqlstate.Error
		var err error
		u, warning, err = d.ResolveModifiers(modifiers...)
		if err != nil {
			return sqltype.Usage{}, inputErrorAt(err, raw.Pos)
		}
		if warning != nil && op != nil {
			if raw.Pos.Character > 0 {
				warning.Position = strconv.Itoa(raw.Pos.Character)
			}
			op.emitNotice(levelWarning, *warning)
		}
	}
	if len(raw.ArrayBounds) > 0 {
		array, ok := view.LookupArray(d)
		if !ok || d.IsEnum() {
			return sqltype.Usage{}, typeDiagnostic(raw, "0A000", "unsupported array element type")
		}
		u.Type = array
	}
	return u, nil
}

func typeDiagnostic(raw parser.TypeName, code, message string) error {
	if raw.Pos.Character > 0 {
		return insertError(code, message, raw.Pos)
	}
	return sqlError(code, message)
}

// Recognized PostgreSQL types outside the concrete storage authority are a
// capability boundary, not nonexistent names. Classification belongs to binding
// so IF NOT EXISTS can skip it, never to the raw syntax parser.
func unsupportedColumnType(raw parser.TypeName) bool {
	switch raw.Name {
	case "smallserial", "serial2", "serial", "serial4", "bigserial", "serial8":
		// CREATE recognizes these utility pseudo names even when quoted.
		return true
	case "char",
		"timetz",
		"timestamptz", "xml",
		"name", "macaddr", "macaddr8", "point", "line",
		"lseg", "box", "path", "polygon", "circle", "tsvector", "tsquery", "record":
		return true
	}
	if !raw.Quoted {
		switch raw.Name {
		case "double", "precision":
			return true
		}
	}
	return false
}

// Existing row-bearing relations reserve their reciprocal type identity.
func relationTypeNameOccupied(view CatalogRead, namespace ObjectAddress, name string) bool {
	for _, address := range view.Lookup(NameKey{ClassID: RelationClassID, Namespace: namespace, Name: name}) {
		if relation, ok := view.state.descriptors[address].(*relationDescriptor); ok && (relation.relationKind == tableRelation || relation.relationKind == partitionedRelation || relation.relationKind == viewRelation || relation.relationKind == compositeRelation) {
			return true
		}
	}
	return false
}
