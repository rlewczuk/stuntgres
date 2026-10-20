package engine

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

type indexOrdering struct {
	slots       []int
	comparisons []sqltype.IndexComparison
	keys        []catalog.IndexKey
	derived     []expr.Expr
}

func indexColumnLabel(name string, suffix int) string {
	tail := strconv.Itoa(suffix)
	length := min(len(name), 63-len(tail))
	for length > 0 && length < len(name) && !utf8.RuneStart(name[length]) {
		length--
	}
	return name[:length] + tail
}

func bindIndexOrdering(parent *catalog.Relation, definition *catalog.IndexDefinition) (indexOrdering, error) {
	if definition.Method != catalog.BtreeMethod {
		return indexOrdering{}, fmt.Errorf("stuntgres: non-btree ordering definition")
	}
	o := indexOrdering{slots: make([]int, len(definition.Keys)), comparisons: make([]sqltype.IndexComparison, len(definition.Keys)), keys: definition.Keys}
	for i, key := range definition.Keys {
		if key.Derived != nil {
			if definition.Constraint != nil && definition.Constraint.Kind == catalog.UniqueBackingKind {
				attribute, expression, err := indexCatalogKey(parent, key)
				if err != nil {
					return o, err
				}
				if attribute == 0 || expression != nil {
					return o, fmt.Errorf("stuntgres: expression key in unique backing index")
				}
			}
			if key.Derived.Definition == nil || key.Derived.Execution == nil {
				return o, fmt.Errorf("stuntgres: missing derived index expression")
			}
			if o.derived == nil {
				o.derived = make([]expr.Expr, len(definition.Keys))
			}
			o.slots[i], o.derived[i] = -1, key.Derived.Execution
			usage, err := indexKeyUsage(parent, key)
			if err != nil {
				return o, err
			}
			class := key.Class
			if class.Name == "" {
				var exists bool
				class, exists = sqltype.DefaultIndexClass(usage.Type)
				if !exists {
					return o, indexDefaultClassError(usage.Type.DiagnosticName(), catalog.BtreeMethod)
				}
			}
			o.comparisons[i], err = sqltype.BindIndexOrdering(usage, class)
			if err != nil {
				return o, err
			}
			continue
		}
		slot := -1
		for j, attribute := range parent.Attributes {
			if attribute == key.Attribute {
				slot = j
				break
			}
		}
		if slot < 0 {
			return o, fmt.Errorf("stuntgres: index attribute is missing")
		}
		if key.Collation != 0 && parent.Columns[slot].Usage.Type.Usage().Collation.ID == 0 {
			return o, sqlError("42804", "collations are not supported by type "+parent.Columns[slot].Usage.Type.DiagnosticName())
		}
		usage := indexKeyCollation(parent.Columns[slot].Usage, key)
		class := key.Class
		if class.Name == "" {
			var exists bool
			class, exists = sqltype.DefaultIndexClass(usage.Type)
			if !exists {
				return o, indexDefaultClassError(usage.Type.DiagnosticName(), catalog.BtreeMethod)
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
func validateIndexDefinition(view catalog.Read, index *catalog.Relation) error {
	d := index.SQLIndex
	if d == nil || len(d.Keys) == 0 || len(d.Keys)+len(d.Include) > 32 || len(index.Columns) != len(d.Keys)+len(d.Include) || d.Parent.SubID != 0 || index.Sequence != nil || index.View != nil {
		return fmt.Errorf("stuntgres: invalid index definition")
	}
	if d.Valid && (!d.Ready || !d.Live) || d.Ready && !d.Live {
		return fmt.Errorf("stuntgres: invalid index publication state")
	}
	parent, ok := view.Relation(d.Parent)
	if !ok || (parent.RelationKind != catalog.TableRelation && parent.RelationKind != catalog.MaterializedViewRelation && parent.RelationKind != catalog.PartitionedRelation) || parent.Owner != index.Owner || parent.Namespace() != index.Namespace() {
		return fmt.Errorf("stuntgres: invalid index parent")
	}
	if d.Constraint != nil {
		if !backingConstraintPresent(parent, index) || d.Unique != (d.Constraint.Kind == catalog.UniqueBackingKind) {
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
	if p := d.Predicate; p != nil {
		if p.Definition == nil || p.Execution == nil || p.Definition.ResultUsage() != p.Execution.ResultUsage() || p.Execution.ResultUsage().Type == nil || p.Execution.ResultUsage().Type.ID() != sqltype.Bool || plannedVolatility(p.Execution) != expr.Immutable || !slices.Equal(p.Attributes, parent.Attributes) {
			return fmt.Errorf("stuntgres: invalid index predicate")
		}
		if _, err := (scalarRenderer{validation: true, owner: parent}).sql(context.Background(), view, p.Definition); err != nil {
			return err
		}
	}
	for i, key := range d.Keys {
		if key.Derived != nil {
			derived := key.Derived
			if key.Attribute != 0 || derived.Definition == nil || derived.Execution == nil || derived.Definition.ResultUsage() != derived.Execution.ResultUsage() || plannedVolatility(derived.Execution) != expr.Immutable {
				return fmt.Errorf("stuntgres: invalid derived index key")
			}
			if _, err := (scalarRenderer{validation: true, owner: parent}).sql(context.Background(), view, derived.Definition); err != nil {
				return err
			}
		}
		usage, err := indexKeyUsage(parent, key)
		if err != nil {
			return err
		}
		if index.Columns[i].Usage != usage {
			return fmt.Errorf("stuntgres: invalid index key usage")
		}
	}
	for i, attribute := range d.Include {
		slot := slices.Index(parent.Attributes, attribute)
		if slot < 0 || index.Columns[len(d.Keys)+i].Usage != parent.Columns[slot].Usage {
			return fmt.Errorf("stuntgres: invalid included attribute usage")
		}
	}
	return nil
}
func (o indexOrdering) compareKeys(ctx context.Context, a, b storage.IndexEntry) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	for i, key := range o.keys {
		av, bv := a.Keys[i], b.Keys[i]
		if av.IsNull() || bv.IsNull() {
			if av.IsNull() == bv.IsNull() {
				continue
			}
			if av.IsNull() == key.NullsFirst {
				return -1, nil
			}
			return 1, nil
		}
		c, err := o.comparisons[i].Compare(ctx, av, bv, sqltype.InputEnvironment{})
		if err != nil {
			return 0, err
		}
		if key.Desc {
			c = -c
		}
		if c != 0 {
			return c, nil
		}
	}
	return 0, nil
}
func (o indexOrdering) compare(ctx context.Context, a, b storage.IndexEntry) (int, error) {
	c, err := o.compareKeys(ctx, a, b)
	if err != nil || c != 0 {
		return c, err
	}
	if a.Row < b.Row {
		return -1, nil
	}
	if a.Row > b.Row {
		return 1, nil
	}
	if a.Version < b.Version {
		return -1, nil
	}
	if a.Version > b.Version {
		return 1, nil
	}
	return 0, nil
}
func (o indexOrdering) fillKeys(values []sqltype.Value, keys []sqltype.Value, evaluation *EvalContext) error {
	for i, slot := range o.slots {
		var expression expr.Expr
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

func (o indexOrdering) entry(row storage.RowVersion, evaluation *EvalContext) (storage.IndexEntry, error) {
	keys := make([]sqltype.Value, len(o.slots))
	if err := o.fillKeys(row.Values, keys, evaluation); err != nil {
		return storage.IndexEntry{}, err
	}
	return storage.IndexEntry{Keys: keys, Row: row.Row, Version: row.Version}, nil
}

// Bottom-up merge sort propagates comparison/cancellation failures. No callback
// is allowed to fail after publication, and published vectors are never sorted.
func (o indexOrdering) sort(ctx context.Context, entries []storage.IndexEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(entries) < 2 {
		return nil
	}
	scratch := make([]storage.IndexEntry, len(entries))
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
func buildIndexRoot(evaluation *EvalContext, parent *catalog.Relation, d *catalog.IndexDefinition, store *storage.Relation, capture *indexBuildCapture) (*storage.IndexRoot, error) {
	if d.Method == catalog.HashMethod {
		return buildHashIndexRoot(evaluation, parent, d, store, true, capture)
	}
	entries, err := buildIndexEntries(evaluation, parent, d, store, capture)
	if err != nil {
		return nil, err
	}
	return newLogicalIndexRoot(d, entries), nil
}

func buildIndexEntries(evaluation *EvalContext, parent *catalog.Relation, d *catalog.IndexDefinition, store *storage.Relation, capture *indexBuildCapture) ([]storage.IndexEntry, error) {
	ctx := evaluation.Context
	o, err := bindIndexKeys(parent, d)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, fmt.Errorf("stuntgres: index parent storage is missing")
	}
	entries := make([]storage.IndexEntry, 0, len(store.Versions))
	var preparation indexExecutionPreparation
	for position, row := range store.Versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if visibility.LiveVersion(row, ids.CommitSeq(^uint64(0)), store.Ends, nil) {
			matches, err := preparation.predicateMatches(evaluation, parent, d, row.Values, &o, nil)
			if err != nil {
				return nil, err
			}
			if capture != nil {
				capture.outputs[position] = storage.IndexOutput{Index: capture.index, Member: matches}
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
			entry.Position = position
			if capture != nil {
				capture.outputs[position].Keys = entry.Keys
			}
			entries = append(entries, entry)
		}
	}
	if d.Method == catalog.BtreeMethod {
		if err := o.sort(ctx, entries); err != nil {
			return nil, err
		}
	}
	return entries, nil
}
func mergeIndexRoot(evaluation *EvalContext, parent, index *catalog.Relation, old []storage.IndexEntry, p *rowPublication, reconstruct, materialize, building bool, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView) (*storage.IndexRoot, error) {
	ctx := evaluation.Context
	d := index.SQLIndex
	validate := (d.Unique || d.IsExclusion()) && (d.Constraint != nil || d.Valid || reconstruct)
	o, err := bindIndexKeys(parent, d)
	if err != nil {
		return nil, err
	}
	endedCount := len(p.writes)
	if p.deletion != nil {
		endedCount += len(p.deletion.Rows)
	}
	ended := make(map[ids.RowID]ids.VersionID, endedCount)
	added := make([]storage.IndexEntry, 0, len(p.writes))
	var preparation indexExecutionPreparation
	for i, row := range p.writes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if row.Base != 0 {
			ended[row.Row] = row.Base
		}
		if p.deletion != nil {
			if end, ok := p.deletion.Rows[row.Row]; ok && end.Version == row.Version {
				continue
			}
		}
		output, produced := row.IndexOutputs[index.Address()]
		produced = produced && indexOutputMatches(output, index)
		if !reconstruct && !produced && !rowMaintainsIndex(row, index) {
			continue
		}
		var entry storage.IndexEntry
		if produced {
			if !output.Member {
				continue
			}
			entry = storage.IndexEntry{Keys: output.Keys, Row: row.Row, Version: row.Version}
		} else {
			if d.HasRuntimeDomainConversion() && evaluation.liveScope() == nil && row.Born == 0 {
				return nil, fmt.Errorf("stuntgres: missing produced index row")
			}
			matches, err := preparation.predicateMatches(evaluation, parent, d, row.Values, &o, nil)
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
		entry.Position = publicationSlot(p, i)
		added = append(added, entry)
	}
	if p.deletion != nil {
		for id, end := range p.deletion.Rows {
			if !end.Own {
				ended[id] = end.Version
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
	if d.Method == catalog.GistMethod {
		// Preserve immutable predecessor entries and cached produced keys.
		// Validation needs the complete candidate membership even in PREPARE.
		entries := make([]storage.IndexEntry, 0, len(old)+len(added))
		for _, entry := range old {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if ended[entry.Row] != entry.Version {
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
	var entries []storage.IndexEntry
	if materialize {
		entries = make([]storage.IndexEntry, 0, len(old)+len(added))
	}
	var previous storage.IndexEntry
	havePrevious := false
	accept := func(entry storage.IndexEntry) error {
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
		if ended[entry.Row] == entry.Version {
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
	if scope.op.replayOrZero().tableDestination && s.Concurrent.Character != 0 {
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
		parent, ok := auth.Catalog.Relation(scope.lockedRef.Address)
		if !ok {
			return fail(ids.ErrCatalogReplaced)
		}
		if scope.op.replayOrZero().tableDestination {
			bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
			if err != nil || !bootstrap.Superuser || e.importTargets == nil {
				return fail(fmt.Errorf("stuntgres: invalid trusted restore index authority"))
			}
		}
		if len(s.Keys)+len(s.Include) > 32 {
			return fail(sqlError("54011", "cannot use more than 32 columns in an index"))
		}
		if parent.RelationKind != catalog.TableRelation && parent.RelationKind != catalog.MaterializedViewRelation && parent.RelationKind != catalog.PartitionedRelation {
			if parent.RelationKind == catalog.IndexRelation {
				return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot open relation "%s"`, parent.Name()), Detail: "This operation is not supported for indexes."})
			}
			return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot create index on relation "%s"`, parent.Name()), Detail: fmt.Sprintf("This operation is not supported for %ss.", relationDiagnosticKind(parent))})
		}
		ns := e.catalog.Domain.Ref(auth.Catalog.Epoch(), parent.Namespace())
		if err = auth.CheckCreate(ns); err != nil {
			denied, ok := err.(*sqlstate.Error)
			if !scope.op.replayOrZero().tableDestination || !ok || denied.Code != "42501" {
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
			method := catalog.BtreeMethod
			if s.Method == "hash" {
				method = catalog.HashMethod
			}
			if s.Method == "gist" {
				method = catalog.GistMethod
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
		definition.Predicate = predicate
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
			name = generatedRelationName(parent.Name(), addition.String(), "idx", func(n string) bool {
				_, ok := auth.Catalog.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: n})
				return ok
			})
		}
		key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: name}
		if _, exists := auth.Catalog.LookupName(key); exists {
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
				return fail(sqlError("22023", fmt.Sprintf("operator class %s has no options", definition.Keys[i].Class.Name)))
			}
		}
		definition.Unique, definition.NullsNotDistinct = s.Unique, s.NullsNotDistinct
		if s.Unique && s.Concurrent.Character == 0 && !scope.op.replayOrZero().tableDestination {
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
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return fail(err)
		}
		revision, err := e.catalog.revisionLocked()
		if err != nil {
			return fail(err)
		}
		for i := range columns {
			columns[i].OriginTable = uint32(oid)
			columns[i].OriginAttribute = int16(i + 1)
		}
		d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: parent.Namespace(), ObjectName: name, ObjectRevision: revision, Owner: parent.Owner, Columns: columns, Attributes: attributes, Index: lookup, RelationKind: catalog.IndexRelation, SQLIndex: definition}
		d.ColumnMeta.HighWater = int32(len(attributes))
		if s.Concurrent.Character != 0 {
			definition.Ready, definition.Valid = false, false
			if err := e.visibilityWriteLocked(scope.txRef); err != nil {
				return fail(err)
			}
		}
		base := auth.Catalog
		stage := e.BeginCatalogStage(base)
		stage.authorizeLocked(auth, scope.op.session)
		stage.indexOutput = scope.output
		err = stage.Create(d)
		if err == nil && parent.Partition != nil {
			if s.Only {
				definition.Valid = len(parent.Partition.Children) == 0
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
		if _, conflict := err.(catalog.Conflict); conflict {
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
				_, exists := current.LookupName(key)
				same := current.Epoch() == base.Epoch() && current.Get(parent.Address()) != nil
				if authErr == nil && same && exists {
					authErr = currentAuth.Check(access.Requirement{Object: scope.lockedRef, OwnerOnly: true})
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
			e.visibility.ResetPhaseLocked(scope.txRef)
			e.mu.Unlock()
			return e.executeConcurrentIndex(scope, s, d)
		}
		if err == nil {
			scope.completed = true
		}
		return Result{}, err
	}
}
