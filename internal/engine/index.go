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
	collationRef     sqltype.CollationRef
	class            sqltype.IndexClass
	explicitClass    bool
}
type indexDefinition struct {
	method             indexMethod
	parent             ObjectAddress
	partitionParent    ObjectAddress
	keys               []indexKey
	include            []int32
	unique             bool
	nullsNotDistinct   bool
	constraint         *backingConstraint
	ready, valid, live bool
	predicate          *indexPredicate
	exclusion          []sqltype.ExclusionOperator
}

func (d *indexDefinition) publicationState() (ready, valid, live bool) {
	return d.ready, d.valid, d.live
}

type logicalIndexEntry struct {
	keys     []sqltype.Value
	row      RowID
	version  VersionID
	position int // Physical slot in the simultaneously published immutable storage.
}
type logicalIndexRoot struct {
	entries    []logicalIndexEntry // Ordered B-tree keys or unordered logical GiST membership.
	hash       *hashIndexRoot
	membership map[RowID]VersionID // Immutable; partial roots only.
	rowEntries map[RowID]int       // Entry ordinals, only checked unique/exclusion expression keys.
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
			if definition.constraint != nil && definition.constraint.kind == uniqueBackingKind {
				attribute, expression, err := indexCatalogKey(parent, key)
				if err != nil {
					return o, err
				}
				if attribute == 0 || expression != nil {
					return o, fmt.Errorf("stuntgres: expression key in unique backing index")
				}
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
	if d.valid && (!d.ready || !d.live) || d.ready && !d.live {
		return fmt.Errorf("stuntgres: invalid index publication state")
	}
	parent, ok := view.state.descriptors[d.parent].(*relationDescriptor)
	if !ok || (parent.relationKind != tableRelation && parent.relationKind != materializedViewRelation && parent.relationKind != partitionedRelation) || parent.owner != index.owner || parent.namespace != index.namespace {
		return fmt.Errorf("stuntgres: invalid index parent")
	}
	if d.constraint != nil {
		if !backingConstraintPresent(parent, index) || d.unique != (d.constraint.kind == uniqueBackingKind) {
			return fmt.Errorf("stuntgres: orphan constraint backing index")
		}
	}
	if err := validatePartitionIndex(view, index, parent); err != nil {
		return err
	}
	err := bindIndexProvider(parent, d)
	if err != nil {
		return err
	}
	if p := d.predicate; p != nil {
		if p.definition == nil || p.execution == nil || p.definition.ResultUsage() != p.execution.ResultUsage() || p.execution.ResultUsage().Type == nil || p.execution.ResultUsage().Type.ID() != sqltype.Bool || plannedVolatility(p.execution) != Immutable || !slices.Equal(p.attributes, parent.attributes) {
			return fmt.Errorf("stuntgres: invalid index predicate")
		}
		if _, err := (scalarRenderer{validation: true, owner: parent}).sql(context.Background(), view, p.definition); err != nil {
			return err
		}
	}
	for i, key := range d.keys {
		if key.derived != nil {
			derived := key.derived
			if key.attribute != 0 || derived.definition == nil || derived.execution == nil || derived.definition.ResultUsage() != derived.execution.ResultUsage() || plannedVolatility(derived.execution) != Immutable {
				return fmt.Errorf("stuntgres: invalid derived index key")
			}
			if _, err := (scalarRenderer{validation: true, owner: parent}).sql(context.Background(), view, derived.definition); err != nil {
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
func (o indexOrdering) fillKeys(values []sqltype.Value, keys []sqltype.Value, evaluation *EvalContext) error {
	for i, slot := range o.slots {
		var expression BoundExpr
		if slot < 0 {
			expression = o.derived[i]
		}
		value, err := extractIndexKey(slot, expression, values, evaluation)
		if err != nil {
			return err
		}
		keys[i] = value
	}
	return nil
}

func (o indexOrdering) entry(row rowVersion, evaluation *EvalContext) (logicalIndexEntry, error) {
	keys := make([]sqltype.Value, len(o.slots))
	if err := o.fillKeys(row.values, keys, evaluation); err != nil {
		return logicalIndexEntry{}, err
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
func buildIndexRoot(evaluation *EvalContext, parent *relationDescriptor, d *indexDefinition, store *relationStorage, capture *indexBuildCapture) (*logicalIndexRoot, error) {
	if d.method == hashMethod {
		return buildHashIndexRoot(evaluation, parent, d, store, true, capture)
	}
	entries, err := buildIndexEntries(evaluation, parent, d, store, capture)
	if err != nil {
		return nil, err
	}
	return newLogicalIndexRoot(d, entries), nil
}

func buildIndexEntries(evaluation *EvalContext, parent *relationDescriptor, d *indexDefinition, store *relationStorage, capture *indexBuildCapture) ([]logicalIndexEntry, error) {
	ctx := evaluation.Context
	o, err := bindIndexKeys(parent, d)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, fmt.Errorf("stuntgres: index parent storage is missing")
	}
	entries := make([]logicalIndexEntry, 0, len(store.versions))
	var preparation indexExecutionPreparation
	for position, row := range store.versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if liveVersion(row, CommitSeq(^uint64(0)), store.ends, nil) {
			matches, err := preparation.predicateMatches(evaluation, parent, d, row.values, &o, nil)
			if err != nil {
				return nil, err
			}
			if capture != nil {
				capture.outputs[position] = indexRowOutput{index: capture.index, member: matches}
			}
			if !matches {
				continue
			}
			if err := preparation.prepareKeys(evaluation, parent, d, &o, nil); err != nil {
				return nil, err
			}
			entry, err := o.entry(row, evaluation)
			if err != nil {
				return nil, err
			}
			entry.position = position
			if capture != nil {
				capture.outputs[position].keys = entry.keys
			}
			entries = append(entries, entry)
		}
	}
	if d.method == btreeMethod {
		if err := o.sort(ctx, entries); err != nil {
			return nil, err
		}
	}
	return entries, nil
}
func mergeIndexRoot(evaluation *EvalContext, parent, index *relationDescriptor, old []logicalIndexEntry, p *rowPublication, reconstruct, materialize, building bool, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView) (*logicalIndexRoot, error) {
	ctx := evaluation.Context
	d := index.sqlIndex
	validate := (d.unique || d.isExclusion()) && (d.constraint != nil || d.valid || reconstruct)
	o, err := bindIndexKeys(parent, d)
	if err != nil {
		return nil, err
	}
	endedCount := len(p.writes)
	if p.deletion != nil {
		endedCount += len(p.deletion.rows)
	}
	ended := make(map[RowID]VersionID, endedCount)
	added := make([]logicalIndexEntry, 0, len(p.writes))
	var preparation indexExecutionPreparation
	for i, row := range p.writes {
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
		output, produced := row.indexOutputs[index.address]
		produced = produced && output.matches(index)
		if !reconstruct && !produced && !rowMaintainsIndex(row, index) {
			continue
		}
		var entry logicalIndexEntry
		if produced {
			if !output.member {
				continue
			}
			entry = logicalIndexEntry{keys: output.keys, row: row.row, version: row.version}
		} else {
			if d.hasRuntimeDomainConversion() && evaluation.liveScope() == nil && row.born == 0 {
				return nil, fmt.Errorf("stuntgres: missing produced index row")
			}
			matches, err := preparation.predicateMatches(evaluation, parent, d, row.values, &o, nil)
			if err != nil {
				return nil, err
			}
			if !matches {
				continue
			}
			if err := preparation.prepareKeys(evaluation, parent, d, &o, nil); err != nil {
				return nil, err
			}
			entry, err = o.entry(row, evaluation)
			if err != nil {
				return nil, err
			}
		}
		entry.position = publicationSlot(p, i)
		added = append(added, entry)
	}
	if p.deletion != nil {
		for id, end := range p.deletion.rows {
			if !end.own {
				ended[id] = end.version
			}
		}
	}
	if len(added) == 0 && len(ended) == 0 {
		if validate {
			if err := validateUniqueEntries(ctx, parent, index, old, schema, output, auth, building, o); err != nil {
				return nil, err
			}
		}
		// No changed root to install; the publisher keeps its actual predecessor.
		return nil, ctx.Err()
	}
	if d.method == gistMethod {
		// Preserve immutable predecessor entries and cached produced keys.
		// Validation needs the complete candidate membership even in PREPARE.
		entries := make([]logicalIndexEntry, 0, len(old)+len(added))
		for _, entry := range old {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if ended[entry.row] != entry.version {
				entries = append(entries, entry)
			}
		}
		entries = append(entries, added...)
		root := newLogicalIndexRoot(d, entries)
		if validate {
			if err := validateExclusionRoot(ctx, parent, index, root, schema, output, auth, building); err != nil {
				return nil, err
			}
		}
		if !materialize {
			return nil, ctx.Err()
		}
		return root, ctx.Err()
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
		if havePrevious && validate {
			if err := validateUniquePair(ctx, parent, index, previous, entry, schema, output, auth, building, o); err != nil {
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
	return newLogicalIndexRoot(d, entries), ctx.Err()
}

func (e *Engine) executeCreateIndex(scope *StatementScope, s *parser.CreateIndex, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if scope.op.restoreTableDestination && s.Concurrent.Character != 0 {
		return Result{}, fmt.Errorf("stuntgres: restore cannot execute concurrent index phases")
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
		parent, ok := auth.catalog.state.descriptors[scope.lockedRef.Address].(*relationDescriptor)
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
		if parent.relationKind != tableRelation && parent.relationKind != materializedViewRelation && parent.relationKind != partitionedRelation {
			if parent.relationKind == indexRelation {
				return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot open relation "%s"`, parent.name), Detail: "This operation is not supported for indexes."})
			}
			return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot create index on relation "%s"`, parent.name), Detail: fmt.Sprintf("This operation is not supported for %ss.", relationDiagnosticKind(parent))})
		}
		ns := e.domain.Ref(auth.catalog.Epoch(), parent.namespace)
		if err = auth.CheckCreate(ns); err != nil {
			denied, ok := err.(*sqlstate.Error)
			if !scope.op.restoreTableDestination || !ok || denied.Code != "42501" {
				return fail(err)
			}
		}
		if err = validateIndexMethod(s); err != nil {
			return fail(err)
		}
		if s.Concurrent.Character != 0 && s.Predicate != nil {
			return fail(sqlError("0A000", "partial concurrent indexes are unsupported"))
		}
		if len(s.Options) != 0 {
			method := btreeMethod
			if s.Method == "hash" {
				method = hashMethod
			}
			if s.Method == "gist" {
				method = gistMethod
			}
			if err := validateIndexReloptions(s.Options, method, false); err != nil {
				return fail(err)
			}
		}
		predicate, err := e.bindIndexPredicate(scope.op, scope.input, auth, parent, s.Predicate)
		if err != nil {
			return fail(err)
		}
		definition, columns, attributes, lookup, err := e.bindIndexAttributes(scope.op, scope.input, auth, parent, s)
		if err != nil {
			return fail(err)
		}
		definition.predicate = predicate
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
				_, ok := auth.catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: parent.namespace, Name: n}]
				return ok
			})
		}
		key := NameKey{ClassID: RelationClassID, Namespace: parent.namespace, Name: name}
		if _, exists := auth.catalog.state.names[key]; exists {
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
		if s.Unique && s.Concurrent.Character == 0 && !scope.op.restoreTableDestination {
			if err := validateUniqueProviders(parent, definition, s.Keys); err != nil {
				return fail(err)
			}
		}
		if err := partitionUniqueCoverage(parent, definition); err != nil {
			return fail(err)
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
		if s.Concurrent.Character != 0 {
			definition.ready, definition.valid = false, false
			if err := e.visibilityWriteLocked(scope.txRef); err != nil {
				return fail(err)
			}
		}
		base := auth.catalog
		stage := e.BeginCatalogStage(base)
		stage.authorizeLocked(auth, scope.op.session)
		stage.indexOutput = scope.output
		err = stage.Create(d)
		if err == nil && parent.partition != nil {
			if s.Only {
				definition.valid = len(parent.partition.children) == 0
			} else {
				err = e.stagePartitionIndexChildren(scope.op, stage, parent, d, true)
			}
		}
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
		_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: base.Epoch()})
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
				current := e.privateCatalogLocked(scope.owner.catalog)
				_, exists := current.state.names[key]
				same := current.Epoch() == base.Epoch() && current.state.descriptors[parent.address] != nil
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
		if err == nil && s.Concurrent.Character != 0 {
			e.mu.Lock()
			e.resetVisibilityPhaseLocked(scope.txRef)
			e.mu.Unlock()
			return e.executeConcurrentIndex(scope, s, d)
		}
		if err == nil {
			scope.completed = true
		}
		return Result{}, err
	}
}
