package engine

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

// Definitions use stable parent attributes; roots own current live identities.
// Hash equality uses captured immutable buckets; all other reads retain scans.
type indexKey struct {
	attribute        int32
	desc, nullsFirst bool
	derived          *derivedIndexKey
	collation        uint32
	class            sqltype.IndexClass
	explicitClass    bool
}
type indexDefinition struct {
	method           indexMethod
	parent           ObjectAddress
	keys             []indexKey
	include          []int32
	unique           bool
	nullsNotDistinct bool
	constraint       *uniqueConstraint
}
type logicalIndexEntry struct {
	keys    []sqltype.Value
	row     RowID
	version VersionID
}
type logicalIndexRoot struct {
	entries []logicalIndexEntry // Ordered B-tree keys, including NULLs.
	hash    *hashIndexRoot
}
type indexOrdering struct {
	slots       []int
	comparisons []sqltype.IndexComparison
	keys        []indexKey
	derived     []BoundExpr
}

func indexColumnLabel(name string, suffix int) string {
	tail := strconv.Itoa(suffix)
	length := min(len(name), 63-len(tail))
	for length > 0 && length < len(name) && !utf8.RuneStart(name[length]) {
		length--
	}
	return name[:length] + tail
}

func bindIndexOrdering(parent *relationDescriptor, definition *indexDefinition) (indexOrdering, error) {
	if definition.method != btreeMethod {
		return indexOrdering{}, fmt.Errorf("stuntgres: non-btree ordering definition")
	}
	o := indexOrdering{slots: make([]int, len(definition.keys)), comparisons: make([]sqltype.IndexComparison, len(definition.keys)), keys: definition.keys}
	for i, key := range definition.keys {
		if key.derived != nil {
			if definition.unique {
				return o, sqlError("0A000", "unique expression indexes are unsupported")
			}
			if key.derived.definition == nil || key.derived.execution == nil {
				return o, fmt.Errorf("stuntgres: missing derived index expression")
			}
			if o.derived == nil {
				o.derived = make([]BoundExpr, len(definition.keys))
			}
			o.slots[i], o.derived[i] = -1, key.derived.execution
			usage, err := indexKeyUsage(parent, key)
			if err != nil {
				return o, err
			}
			class := key.class
			if class.Name == "" {
				var exists bool
				class, exists = sqltype.DefaultIndexClass(usage.Type)
				if !exists {
					return o, indexDefaultClassError(usage.Type.DiagnosticName(), btreeMethod)
				}
			}
			o.comparisons[i], err = sqltype.BindIndexOrdering(usage, class)
			if err != nil {
				return o, err
			}
			continue
		}
		slot := -1
		for j, attribute := range parent.attributes {
			if attribute == key.attribute {
				slot = j
				break
			}
		}
		if slot < 0 {
			return o, fmt.Errorf("stuntgres: index attribute is missing")
		}
		if key.collation != 0 && parent.columns[slot].Usage.Type.Usage().Collation.ID == 0 {
			return o, sqlError("42804", "collations are not supported by type "+parent.columns[slot].Usage.Type.DiagnosticName())
		}
		usage := indexKeyCollation(parent.columns[slot].Usage, key)
		class := key.class
		if class.Name == "" {
			var exists bool
			class, exists = sqltype.DefaultIndexClass(usage.Type)
			if !exists {
				return o, indexDefaultClassError(usage.Type.DiagnosticName(), btreeMethod)
			}
		}
		comparison, err := sqltype.BindIndexOrdering(usage, class)
		if err != nil {
			return o, err
		}
		o.slots[i], o.comparisons[i] = slot, comparison
	}
	return o, nil
}
func validateIndexDefinition(view CatalogRead, index *relationDescriptor) error {
	d := index.sqlIndex
	if d == nil || len(d.keys) == 0 || len(d.keys)+len(d.include) > 32 || len(index.columns) != len(d.keys)+len(d.include) || d.parent.SubID != 0 || index.sequence != nil || index.view != nil {
		return fmt.Errorf("stuntgres: invalid index definition")
	}
	parent, ok := view.state.descriptors[d.parent].(*relationDescriptor)
	if !ok || parent.relationKind != tableRelation || parent.owner != index.owner || parent.namespace != index.namespace {
		return fmt.Errorf("stuntgres: invalid index parent")
	}
	if d.constraint != nil {
		found := false
		for _, c := range parent.uniqueConstraints {
			if c.Index == index.address && c.OID == d.constraint.OID {
				found = true
				break
			}
		}
		if !found || !d.unique || d.nullsNotDistinct != d.constraint.NullsNotDistinct {
			return fmt.Errorf("stuntgres: orphan unique backing index")
		}
	}
	err := bindIndexProvider(parent, d)
	if err != nil {
		return err
	}
	if d.unique {
		if err := validateUniqueProviders(parent, d, nil); err != nil {
			return err
		}
	}
	for i, key := range d.keys {
		if key.derived != nil {
			derived := key.derived
			if key.attribute != 0 || derived.definition == nil || derived.execution == nil || derived.definition.ResultUsage() != derived.execution.ResultUsage() || plannedVolatility(derived.execution) != Immutable {
				return fmt.Errorf("stuntgres: invalid derived index key")
			}
			if _, err := (scalarRenderer{owner: parent}).sql(context.Background(), view, derived.definition); err != nil {
				return err
			}
		}
		usage, err := indexKeyUsage(parent, key)
		if err != nil {
			return err
		}
		if index.columns[i].Usage != usage {
			return fmt.Errorf("stuntgres: invalid index key usage")
		}
	}
	for i, attribute := range d.include {
		slot := slices.Index(parent.attributes, attribute)
		if slot < 0 || index.columns[len(d.keys)+i].Usage != parent.columns[slot].Usage {
			return fmt.Errorf("stuntgres: invalid included attribute usage")
		}
	}
	return nil
}
func (o indexOrdering) compareKeys(ctx context.Context, a, b logicalIndexEntry) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	for i, key := range o.keys {
		av, bv := a.keys[i], b.keys[i]
		if av.IsNull() || bv.IsNull() {
			if av.IsNull() == bv.IsNull() {
				continue
			}
			if av.IsNull() == key.nullsFirst {
				return -1, nil
			}
			return 1, nil
		}
		c, err := o.comparisons[i].Compare(ctx, av, bv, sqltype.InputEnvironment{})
		if err != nil {
			return 0, err
		}
		if key.desc {
			c = -c
		}
		if c != 0 {
			return c, nil
		}
	}
	return 0, nil
}
func (o indexOrdering) compare(ctx context.Context, a, b logicalIndexEntry) (int, error) {
	c, err := o.compareKeys(ctx, a, b)
	if err != nil || c != 0 {
		return c, err
	}
	if a.row < b.row {
		return -1, nil
	}
	if a.row > b.row {
		return 1, nil
	}
	if a.version < b.version {
		return -1, nil
	}
	if a.version > b.version {
		return 1, nil
	}
	return 0, nil
}
func (o indexOrdering) entry(row rowVersion, evaluation *EvalContext) (logicalIndexEntry, error) {
	keys := make([]sqltype.Value, len(o.slots))
	for i, slot := range o.slots {
		var expression BoundExpr
		if slot < 0 {
			expression = o.derived[i]
		}
		value, err := extractIndexKey(slot, expression, row, evaluation)
		if err != nil {
			return logicalIndexEntry{}, err
		}
		keys[i] = value
	}
	return logicalIndexEntry{keys: keys, row: row.row, version: row.version}, nil
}

// Bottom-up merge sort propagates comparison/cancellation failures. No callback
// is allowed to fail after publication, and published vectors are never sorted.
func (o indexOrdering) sort(ctx context.Context, entries []logicalIndexEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(entries) < 2 {
		return nil
	}
	scratch := make([]logicalIndexEntry, len(entries))
	src, dst := entries, scratch
	for width := 1; width < len(entries); width *= 2 {
		for lo := 0; lo < len(entries); lo += 2 * width {
			mid, hi := min(lo+width, len(entries)), min(lo+2*width, len(entries))
			a, b := lo, mid
			for at := lo; at < hi; at++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				take := b == hi
				if a < mid && b < hi {
					c, err := o.compare(ctx, src[a], src[b])
					if err != nil {
						return err
					}
					take = c <= 0
				}
				if a < mid && take {
					dst[at] = src[a]
					a++
				} else {
					dst[at] = src[b]
					b++
				}
			}
		}
		src, dst = dst, src
	}
	copy(entries, src)
	return nil
}
func buildIndexRoot(ctx context.Context, parent *relationDescriptor, d *indexDefinition, store *relationStorage) (*logicalIndexRoot, error) {
	if d.method == hashMethod {
		return buildHashIndexRoot(ctx, parent, d, store, true)
	}
	entries, err := buildIndexEntries(ctx, parent, d, store)
	if err != nil {
		return nil, err
	}
	return &logicalIndexRoot{entries: entries}, nil
}

func buildIndexEntries(ctx context.Context, parent *relationDescriptor, d *indexDefinition, store *relationStorage) ([]logicalIndexEntry, error) {
	o, err := bindIndexOrdering(parent, d)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, fmt.Errorf("stuntgres: index parent storage is missing")
	}
	entries := make([]logicalIndexEntry, 0, len(store.versions))
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1}
	for _, row := range store.versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if liveVersion(row, CommitSeq(^uint64(0)), store.ends, nil) {
			entry, err := o.entry(row, &evaluation)
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry)
		}
	}
	if err := o.sort(ctx, entries); err != nil {
		return nil, err
	}
	return entries, nil
}
func mergeIndexRoot(ctx context.Context, parent, index *relationDescriptor, old []logicalIndexEntry, p *rowPublication, materialize bool, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView) (*logicalIndexRoot, error) {
	d := index.sqlIndex
	o, err := bindIndexOrdering(parent, d)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, fmt.Errorf("stuntgres: index root is missing")
	}
	endedCount := len(p.writes)
	if p.deletion != nil {
		endedCount += len(p.deletion.rows)
	}
	ended := make(map[RowID]VersionID, endedCount)
	added := make([]logicalIndexEntry, 0, len(p.writes))
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1}
	for _, row := range p.writes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if row.base != 0 {
			ended[row.row] = row.base
		}
		if p.deletion != nil {
			if end, ok := p.deletion.rows[row.row]; ok && end.version == row.version {
				continue
			}
		}
		entry, err := o.entry(row, &evaluation)
		if err != nil {
			return nil, err
		}
		added = append(added, entry)
	}
	if p.deletion != nil {
		for id, end := range p.deletion.rows {
			if !end.own {
				ended[id] = end.version
			}
		}
	}
	if err := o.sort(ctx, added); err != nil {
		return nil, err
	}
	var entries []logicalIndexEntry
	if materialize {
		entries = make([]logicalIndexEntry, 0, len(old)+len(added))
	}
	var previous logicalIndexEntry
	havePrevious := false
	accept := func(entry logicalIndexEntry) error {
		if havePrevious {
			if err := validateUniquePair(ctx, parent, index, previous, entry, schema, output, auth, false, o); err != nil {
				return err
			}
		}
		previous, havePrevious = entry, true
		if materialize {
			entries = append(entries, entry)
		}
		return nil
	}
	j := 0
	for _, entry := range old {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if ended[entry.row] == entry.version {
			continue
		}
		for j < len(added) {
			c, err := o.compare(ctx, added[j], entry)
			if err != nil {
				return nil, err
			}
			if c > 0 {
				break
			}
			if err := accept(added[j]); err != nil {
				return nil, err
			}
			j++
		}
		if err := accept(entry); err != nil {
			return nil, err
		}
	}
	for ; j < len(added); j++ {
		if err := accept(added[j]); err != nil {
			return nil, err
		}
	}
	if !materialize {
		return nil, ctx.Err()
	}
	return &logicalIndexRoot{entries: entries}, ctx.Err()
}

func (e *Engine) executeCreateIndex(scope *StatementScope, s *parser.CreateIndex, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	for {
		e.mu.Lock()
		fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
		if err := scope.refreshRelationGrantsLocked(e); err != nil {
			return fail(err)
		}
		auth, err := e.statementAuthorizationLocked(scope.op, s)
		if err != nil {
			return fail(err)
		}
		if err = scope.captureIdentityLocked(auth); err != nil {
			return fail(err)
		}
		parent, ok := e.state.descriptors[scope.lockedRef.Address].(*relationDescriptor)
		if !ok {
			return fail(ErrCatalogReplaced)
		}
		if scope.op.restoreTableDestination {
			bootstrap, err := auth.roles.Descriptor(auth.state.Authenticated)
			if err != nil || !bootstrap.Superuser || e.importTargets == nil {
				return fail(fmt.Errorf("stuntgres: invalid trusted restore index authority"))
			}
		}
		if len(s.Keys)+len(s.Include) > 32 {
			return fail(sqlError("54011", "cannot use more than 32 columns in an index"))
		}
		if parent.relationKind != tableRelation {
			if parent.relationKind == indexRelation {
				return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot open relation "%s"`, parent.name), Detail: "This operation is not supported for indexes."})
			}
			return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot create index on relation "%s"`, parent.name), Detail: fmt.Sprintf("This operation is not supported for %ss.", relationDiagnosticKind(parent))})
		}
		ns := ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: parent.namespace}
		if err = auth.CheckCreate(ns); err != nil {
			denied, ok := err.(*sqlstate.Error)
			if !scope.op.restoreTableDestination || !ok || denied.Code != "42501" {
				return fail(err)
			}
		}
		if err = validateIndexMethod(s); err != nil {
			return fail(err)
		}
		if len(s.Options) != 0 {
			method := btreeMethod
			if s.Method == "hash" {
				method = hashMethod
			}
			if err := validateIndexReloptions(s.Options, method, false); err != nil {
				return fail(err)
			}
		}
		definition, columns, attributes, lookup, err := e.bindIndexAttributes(scope.op, scope.input, auth, parent, s)
		if err != nil {
			return fail(err)
		}
		name := s.Name.Text
		if name == "" {
			var addition strings.Builder
			for i, column := range columns {
				if i > 0 {
					addition.WriteByte('_')
				}
				addition.WriteString(column.Name)
				if addition.Len() >= 64 {
					break
				}
			}
			name = generatedRelationName(parent.name, addition.String(), "idx", func(n string) bool {
				_, ok := e.state.names[NameKey{ClassID: RelationClassID, Namespace: parent.namespace, Name: n}]
				return ok
			})
		}
		key := NameKey{ClassID: RelationClassID, Namespace: parent.namespace, Name: name}
		if _, exists := e.state.names[key]; exists {
			e.mu.Unlock()
			if s.IfNotExists {
				scope.op.existingRelationNotice(name)
				scope.completed = true
				return Result{}, nil
			}
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		}
		// The released provider-options phase follows catalog name admission.
		for i, raw := range s.Keys {
			if len(raw.Options) != 0 {
				return fail(sqlError("22023", fmt.Sprintf("operator class %s has no options", definition.keys[i].class.Name)))
			}
		}
		definition.unique, definition.nullsNotDistinct = s.Unique, s.NullsNotDistinct
		if s.Unique {
			if err := validateUniqueProviders(parent, definition, s.Keys); err != nil {
				return fail(err)
			}
		}
		if len(s.Options) != 0 {
			return fail(indexCapability("index storage parameters are unsupported", s.WithPos))
		}
		oid, err := e.allocateOIDLocked()
		if err != nil {
			return fail(err)
		}
		revision, err := e.revisionLocked()
		if err != nil {
			return fail(err)
		}
		for i := range columns {
			columns[i].OriginTable = uint32(oid)
			columns[i].OriginAttribute = int16(i + 1)
		}
		d := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: oid}, namespace: parent.namespace, name: name, revision: revision, owner: parent.owner, columns: columns, attributes: attributes, index: lookup, relationKind: indexRelation, sqlIndex: definition}
		d.attributeHighWater = int32(len(attributes))
		base := auth.catalog
		stage := e.BeginCatalogStage(base)
		stage.authSession, stage.authRevision, stage.roleRevision = scope.op.session, auth.revision, auth.roles.state.revision
		stage.indexOutput = scope.output
		err = stage.Create(d)
		e.mu.Unlock()
		if err != nil {
			e.DiscardCatalogStage(stage)
			return Result{}, err
		}
		prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
		if err != nil {
			e.DiscardCatalogStage(stage)
			return Result{}, err
		}
		_, err = e.publishCatalogOperation(scope.op, prepared, PublicationToken{Epoch: base.Epoch()})
		e.DiscardCatalogStage(stage)
		if _, conflict := err.(CatalogConflict); conflict {
			if contextErr := scope.op.Context().Err(); contextErr != nil {
				return Result{}, contextErr
			}
			if s.Name.Text == "" {
				continue
			}
			if s.IfNotExists {
				e.mu.Lock()
				currentAuth, authErr := e.statementAuthorizationLocked(scope.op, s)
				_, exists := e.state.names[key]
				same := e.state.epoch == base.Epoch() && e.state.descriptors[parent.address] != nil
				if authErr == nil && same && exists {
					authErr = currentAuth.Check(AccessRequirement{Object: scope.lockedRef, OwnerOnly: true})
				}
				if authErr == nil && same && exists {
					authErr = currentAuth.CheckCreate(ns)
				}
				e.mu.Unlock()
				if authErr != nil {
					return Result{}, authErr
				}
				if same && exists {
					scope.op.existingRelationNotice(name)
					scope.completed = true
					return Result{}, nil
				}
			}
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		}
		if err == nil {
			scope.completed = true
		}
		return Result{}, err
	}
}
