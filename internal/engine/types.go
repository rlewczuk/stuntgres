package engine

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type StatementDescription struct {
	Parameters ParameterInfo
	Results    []catalog.Column
	// Logical user identities belong to an owner/epoch, not merely a reused OID.
	catalogOwner *catalog.Owner
	catalogEpoch ids.CatalogEpoch
	// Public signatures stay immutable. Invalidation refreshes temporal-now and
	// specialized groups atomically, preserving native logical literal identity.
	temporals        []temporalConstant
	composites       map[preparedCompositeKey]compositeOccurrence
	sequences        *sequenceAnalysis
	regtypes         *regtypeAnalysis
	analysis         *preparedAnalysis
	quotes           []quoteConstant
	quoteGeneration  uint64 // Published separately after successful quote preflight.
	planOwner        ids.SessionToken
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
	value        sqltype.Value // immutable analyzed compound or enum literal
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
	case sqltype.Date, sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval, sqltype.DateRange, sqltype.TimestampRange, sqltype.TimestamptzRange, sqltype.Regrole:
		return true
	}
	return false
}

type typeProvider struct{}

func (typeProvider) ClassID() ids.OID { return ids.TypeClassID }
func (typeProvider) Validate(view catalog.Read, descriptor catalog.Descriptor) error {
	t, ok := descriptor.(*catalog.Type)
	if !ok || t.SQLType == nil || t.SQLType.ID() == 0 {
		return fmt.Errorf("stuntgres: invalid type descriptor")
	}
	if t.ACLSyntheticDefault && !authorization.MatchesDefaultBaseline(t.ACL, t.Owner, authorization.DefaultTypes, false) {
		return fmt.Errorf("stuntgres: invalid synthetic type ACL")
	}
	ns, ok := view.Descriptor(descriptor.Namespace())
	if !ok || ns.Kind() != ids.NamespaceObject {
		return fmt.Errorf("stuntgres: type namespace is missing")
	}
	found := view.Lookup(catalog.DescriptorNameKey(descriptor))
	if len(found) != 1 || found[0] != descriptor.Address() {
		return fmt.Errorf("stuntgres: invalid type identity")
	}
	if t.Extension != (ids.ObjectAddress{}) {
		parent, ok := view.Extension(t.Extension)
		if !ok || parent.Name() != "intarray" || parent.TargetNamespace != t.Namespace() || !t.SQLType.IsQueryInt() {
			return fmt.Errorf("stuntgres: invalid installed type membership")
		}
		if err := stockIntarray.ValidateExtension(view, parent); err != nil {
			return err
		}
	} else if t.SQLType.IsQueryInt() {
		return fmt.Errorf("stuntgres: query_int type has no installation")
	}
	if t.OwningRelation != (ids.ObjectAddress{}) {
		relation, ok := view.Relation(t.OwningRelation)
		if !ok || relation.Namespace() != t.Namespace() || relation.Owner != t.Owner {
			return fmt.Errorf("stuntgres: invalid row type ownership")
		}
		if !t.SQLType.IsArray() && (relation.RowType != t.SQLType.ID() || relation.Name() != t.Name()) {
			return catalog.Conflict{}
		}
		if t.SQLType.IsArray() && relation.RowType != t.SQLType.Element().ID() {
			return catalog.Conflict{}
		}
	}
	if t.SQLType.IsUserDefined() || t.SQLType.IsArray() && t.SQLType.Element().IsUserDefined() {
		if t.ObjectRevision == 0 || t.OwningRelation == (ids.ObjectAddress{}) && relationTypeNameOccupied(view, t.Namespace(), t.Name()) {
			return catalog.Conflict{}
		}
	}
	if t.SQLType.IsComposite() {
		relation, ok := view.Relation(t.OwningRelation)
		if !ok || len(relation.Columns) != t.SQLType.CompositeLen() {
			return fmt.Errorf("stuntgres: missing composite backing definition")
		}
		for i, column := range relation.Columns {
			member := t.SQLType.CompositeMember(i)
			actual, exists := view.LookupType(member.Usage.Type.ID())
			if !exists || actual != member.Usage.Type || member.Attribute != relation.Attributes[i] || member.Name != column.Name || member.Usage != column.Usage {
				return fmt.Errorf("stuntgres: inconsistent composite member generation")
			}
		}
	}
	if t.SQLType.IsDomain() {
		base := t.SQLType.DomainBase()
		if err := validateUsageCollation(view, base); err != nil {
			return err
		}
		actual, exists := view.LookupType(base.Type.ID())
		if !exists || actual != base.Type || t.SQLType.Concrete() && (!base.Valid() || t.ObjectRevision == 0) || t.Dimensions < 0 {
			return fmt.Errorf("stuntgres: invalid domain base generation")
		}
		if err := validateDomainCheckDefinitions(view, t); err != nil {
			return err
		}
	}
	if t.SQLType.IsArray() {
		element := t.SQLType.Element()
		actual, exists := view.LookupType(element.ID())
		if !exists || actual != element {
			return fmt.Errorf("stuntgres: invalid array element generation")
		}
		owner, exists := view.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(element.ID())})
		if !exists || owner.(*catalog.Type).Array != t.SQLType.ID() || owner.Namespace() != t.Namespace() || owner.(*catalog.Type).Owner != t.Owner {
			return fmt.Errorf("stuntgres: invalid associated array identity")
		}
	}
	if t.Array != 0 {
		array, exists := view.LookupType(t.Array)
		if !exists || !array.IsArray() || array.Element() != t.SQLType {
			return fmt.Errorf("stuntgres: invalid element array association")
		}
	}
	return nil
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
		if err := auth.CanUse(e.catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())})); err != nil {
			return sqltype.Usage{}, err
		}
	}
	return u, nil
}

// Query-derived schemas can store builtin CHAR without admitting raw declarations.
// Only the isolated Restore caller reconstructs those private declarations;
// transaction identifiers and regclass use ordinary public type admission.
func (e *Engine) resolveTableColumnType(raw parser.TypeName, op *Operation) (sqltype.Usage, error) {
	u, err := e.resolveType(raw, op)
	privateScalar := sqltype.ID(0)
	if err != nil && len(raw.Modifiers) == 0 && len(raw.ArrayBounds) == 0 && (raw.Schema == nil || raw.Schema.Text == "pg_catalog") {
		if raw.Name == "char" && raw.Quoted {
			privateScalar = sqltype.Char
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
	bootstrap, authErr := auth.Roles.Descriptor(auth.State.Authenticated)
	if authErr != nil || !bootstrap.Superuser || e.importTargets == nil {
		return sqltype.Usage{}, fmt.Errorf("stuntgres: invalid trusted restore column authority")
	}
	typ, ok := auth.Catalog.LookupType(privateScalar)
	if !ok || !typ.Concrete() || typ.IsUserDefined() {
		return sqltype.Usage{}, fmt.Errorf("stuntgres: missing builtin restore column type")
	}
	return typ.Usage(), nil
}

func storedArrayElement(typ *sqltype.Descriptor) bool {
	if !typ.Concrete() {
		return false
	}
	physical := typ.BaseType()
	if physical.ID() == sqltype.Tid || sqltype.IsNetwork(physical.ID()) || sqltype.IsTransactionIdentifier(physical.ID()) {
		return false
	}
	if physical.IsArray() {
		return storedArrayElement(physical.Element())
	}
	if physical.IsComposite() {
		return physical.Concrete()
	}
	return sqltype.StoredScalarCastType(physical.ID()) || physical.ID() == sqltype.Regclass
}

func (e *Engine) resolveAnnotationType(raw parser.TypeName, op *Operation, unknownNull bool) (sqltype.Usage, error) {
	return e.resolveAnnotationTypeMode(raw, op, unknownNull, false)
}

func (e *Engine) resolveAnnotationTypeMode(raw parser.TypeName, op *Operation, unknownNull, domainBase bool) (sqltype.Usage, error) {
	return e.resolveAnnotationTypeInput(raw, op, unknownNull, domainBase, false, false, false)
}

// String roles admit internal CHAR without admitting it as a column or a
// general cast target. Lookup, permissions and modifier checks stay type-owned.
func (e *Engine) resolveStringAnnotationType(raw parser.TypeName, op *Operation, unknownNull bool) (sqltype.Usage, error) {
	return e.resolveAnnotationTypeInput(raw, op, unknownNull, false, true, false, false)
}

func canonicalDeclarationTypeName(name string) string {
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
	return name
}

func (e *Engine) resolveAnnotationTypeInput(raw parser.TypeName, op *Operation, unknownNull, domainBase, stringInput, jsonReturning, thresholdArrayInput bool) (sqltype.Usage, error) {
	name := raw.Name
	if !raw.Quoted && raw.Schema == nil {
		name = canonicalDeclarationTypeName(name)
	}
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return sqltype.Usage{}, err
	}
	object := parser.ObjectName{Parts: []parser.Identifier{{Text: name, Pos: raw.Pos}}}
	if raw.Schema != nil {
		object.Parts = []parser.Identifier{*raw.Schema, {Text: name, Pos: raw.Pos}}
	}
	path, err := e.lookupNamespaces(auth, object, typeLookup)
	if err != nil {
		return sqltype.Usage{}, err
	}
	var found ids.ObjectAddress
	for i := range path.count() {
		found = auth.definitionLookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.at(i), Name: name})
		if found != (ids.ObjectAddress{}) {
			break
		}
	}
	if found == (ids.ObjectAddress{}) {
		if domainBase && raw.Name == "record" && (raw.Schema == nil || raw.Schema.Text == "pg_catalog") {
			return sqltype.Usage{}, typeDiagnostic(raw, "42804", `"record" is not a valid base type for a domain`)
		}
		if raw.Schema == nil && unsupportedColumnType(raw) {
			return sqltype.Usage{}, typeDiagnostic(raw, "0A000", "unsupported column type")
		}
		return sqltype.Usage{}, typeDiagnostic(raw, "42704", fmt.Sprintf(`type "%s" does not exist`, objectNameText(object)))
	}
	d, validType := auth.definitionType(sqltype.ID(found.ObjectID))
	if !validType {
		return sqltype.Usage{}, fmt.Errorf("stuntgres: missing definition type provider")
	}
	if jsonReturning && functions.JSONReturningPseudoType(d) {
		return sqltype.Usage{}, sqlError("0A000", "returning pseudo-types is not supported in SQL/JSON functions")
	}
	if domainBase && (!d.Concrete() || d.IsArray() && d.Element().IsEnum() || d.ID() == sqltype.Void) {
		return sqltype.Usage{}, typeDiagnostic(raw, "42804", fmt.Sprintf(`"%s" is not a valid base type for a domain`, d.Name()))
	}
	if unknownNull && d.ID() == sqltype.Unknown && len(raw.Modifiers) == 0 {
		return d.Usage(), nil
	}
	if unknownNull && d.ID() == 2249 && len(raw.Modifiers) == 0 && len(raw.ArrayBounds) == 0 {
		return sqltype.DeferredRecord().Usage(), nil
	}
	if !d.Concrete() || d.IsArray() && d.Element().IsEnum() && !thresholdArrayInput || d.ID() == sqltype.Char && !stringInput || d.ID() == sqltype.Void {
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
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42601" && raw.Schema != nil {
				// Explicit qualification owns its diagnostic spelling. Unqualified
				// aliases and lowered declarations retain the descriptor's name.
				return sqltype.Usage{}, typeDiagnostic(raw, diagnostic.Code, fmt.Sprintf(`type modifier is not allowed for type "%s"`, objectNameText(object)))
			}
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
		integer, _ := auth.definitionType(sqltype.Int4)
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
		if sqltype.IsTransactionIdentifier(d.BaseType().ID()) {
			return sqltype.Usage{}, typeDiagnostic(raw, "0A000", "unsupported column type")
		}
		array, ok := auth.definitionArray(d)
		if !ok || d.IsEnum() && !thresholdArrayInput {
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
func relationTypeNameOccupied(view catalog.Read, namespace ids.ObjectAddress, name string) bool {
	for _, address := range view.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name}) {
		if relation, ok := view.Relation(address); ok && (relation.RelationKind == catalog.TableRelation || relation.RelationKind == catalog.PartitionedRelation || relation.RelationKind == catalog.ViewRelation || relation.RelationKind == catalog.CompositeRelation) {
			return true
		}
	}
	return false
}
