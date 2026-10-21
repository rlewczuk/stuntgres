package index

import (
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Capture accompanies one genuine DDL build. It includes evaluated exclusions
// and NULL hash keys that intentionally have no physical index entry.
type BuildCapture struct {
	Index   *catalog.Relation
	Outputs []storage.IndexOutput
}

type AlterIndexBuild struct {
	Root    *storage.IndexRoot
	Index   *catalog.Relation
	Store   *storage.Relation
	Outputs []storage.IndexOutput
}

// TYPE reconstruction and changed-value validation share stable dependency keys.
func UniqueIndexTypeAffected(definition *catalog.IndexDefinition, transformations []ColumnTransformation) bool {
	for _, change := range transformations {
		if indexAttributeAffected(definition, change.Attribute) {
			return true
		}
	}
	return false
}

func uniqueIndexValuesAffected(definition *catalog.IndexDefinition, transformations []ColumnTransformation, generations []int32) bool {
	if UniqueIndexTypeAffected(definition, transformations) {
		return true
	}
	for _, attribute := range generations {
		if indexAttributeAffected(definition, attribute) {
			return true
		}
	}
	return false
}

func indexAttributeAffected(definition *catalog.IndexDefinition, attribute int32) bool {
	if slices.Contains(definition.Include, attribute) {
		return true
	}
	dependent := func(ref ids.ObjectRef) bool {
		return ref.Address.ClassID == definition.Parent.ClassID && ref.Address.ObjectID == definition.Parent.ObjectID && ref.Address.SubID == attribute
	}
	for _, key := range definition.Keys {
		if key.Derived == nil {
			if key.Attribute == attribute {
				return true
			}
		} else {
			for _, ref := range key.Derived.Dependencies {
				if dependent(ref) {
					return true
				}
			}
		}
	}
	if predicate := definition.Predicate; predicate != nil {
		for _, ref := range predicate.Dependencies {
			if dependent(ref) {
				return true
			}
		}
	}
	return false
}

// TYPE must fail at Execute, including private writes. Merge overlay successors
// before building a real unique root; this is not a commit-only integrity fence.
func ValidateAlterIndexBuilds(evaluation *exec.Context, view access.View, env DisclosureEnv, candidate catalog.Read, parent *catalog.Relation, store *storage.Relation, writes []storage.RowVersion, deletes *visibility.PrivateDeletes, schema string, output sqltype.OutputEnvironment, transformations []ColumnTransformation, generations []int32, added []ids.ObjectAddress, physicalRewrite bool) (map[string]*AlterIndexBuild, error) {
	ctx := evaluation.Context
	if parent.Partition != nil {
		return nil, nil
	}
	var indexes []*catalog.Relation
	if physicalRewrite || len(transformations) != 0 || len(generations) != 0 {
		for _, descriptor := range candidate.Descriptors() {
			index, ok := descriptor.(*catalog.Relation)
			if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != parent.Address() || !index.SQLIndex.Ready || !index.SQLIndex.Live || !index.SQLIndex.Valid {
				continue
			}
			if !slices.Contains(added, index.Address()) && (physicalRewrite || uniqueIndexValuesAffected(index.SQLIndex, transformations, generations)) {
				indexes = append(indexes, index)
			}
		}
	}
	for _, address := range added {
		index, ok := candidate.Relation(address)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != parent.Address() {
			return nil, ids.ErrCatalogReplaced
		}
		indexes = append(indexes, index)
	}
	if len(indexes) == 0 {
		return nil, nil
	}
	slices.SortFunc(indexes, func(a, b *catalog.Relation) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	builds := make(map[string]*AlterIndexBuild, len(indexes))
	validationStore, err := BuildVisibleStore(ctx, store, writes, deletes)
	if err != nil {
		return nil, err
	}
	view.Recapture(candidate)
	evaluation.Catalog, evaluation.Output = candidate, output
	named := false
	for _, index := range indexes {
		var capture *BuildCapture
		if index.SQLIndex.HasRuntimeDomainConversion() && !named {
			var err error
			evaluation.Input.TypeDiagnosticName, err = env.TypeDiagnosticNames(view)
			if err != nil {
				return nil, err
			}
			named = true
		}
		if index.SQLIndex.HasRuntimeDomainConversion() || len(writes) != 0 {
			capture = &BuildCapture{Index: index, Outputs: make([]storage.IndexOutput, len(validationStore.Versions))}
		}
		root, err := BuildIndexRoot(evaluation, parent, index.SQLIndex, validationStore, capture)
		if err != nil {
			return nil, err
		}
		if index.SQLIndex.Valid || index.SQLIndex.Constraint != nil {
			if err := ValidateUniqueRoot(ctx, parent, index, root, schema, output, &view, env, true); err != nil {
				return nil, err
			}
		}
		build := &AlterIndexBuild{Index: index, Root: root, Store: validationStore}
		if capture != nil {
			build.Outputs = capture.Outputs
		}
		builds[index.Name()] = build
	}
	return builds, nil
}

// A genuine DDL build is reusable even with an own overlay: derive the baseline
// from produced entries, and carry exact private pins rather than invoking SQL.
func PrepareAlterIndexRoots(evaluation *exec.Context, changes *session.Journal, source catalog.Read, parent *catalog.Relation, baseline *storage.Relation, validated map[string]*AlterIndexBuild, metadataOnly bool) error {
	if parent.Partition != nil {
		return nil
	}
	var positions map[RowIdentity]int
	for _, build := range validated {
		if build.Store != baseline {
			var err error
			positions, err = BuildPositions(evaluation.Context, baseline)
			if err != nil {
				return err
			}
			break
		}
	}
	for _, build := range validated {
		descriptor, changed := changes.Descriptors[build.Index.Address()]
		if !changed {
			descriptor = evaluation.Catalog.Get(build.Index.Address())
		}
		index, ok := descriptor.(*catalog.Relation)
		if !ok || !SameIndexKeySemantics(build.Index, index) {
			return ids.ErrCatalogReplaced
		}
		root, err := build.BaselineRoot(evaluation.Context, index, baseline, positions)
		if err != nil {
			return err
		}
		RetainIndexBuild(changes, index, baseline, root)
	}
	for address, descriptor := range changes.Descriptors {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != parent.Address() || !index.SQLIndex.Ready || !index.SQLIndex.Live || !index.SQLIndex.Valid {
			continue
		}
		if build := validated[index.Name()]; build != nil && build.Index.Address() == address && SameIndexKeySemantics(build.Index, index) {
			continue
		}
		if candidate, produced := JournalIndexesOrZero(changes).Candidates[address]; produced && candidate.MatchesIndex(index, baseline) {
			continue
		}
		if original := changes.Originals[address]; original == descriptor {
			continue
		}
		if metadataOnly {
			if previous, ok := source.Relation(address); ok && SameIndexKeySemantics(previous, index) {
				continue
			}
		}
		root, err := BuildIndexRoot(evaluation, parent, index.SQLIndex, baseline, nil)
		if err != nil {
			return err
		}
		RetainIndexBuild(changes, index, baseline, root)
	}
	return evaluation.Context.Err()
}

type ColumnTransformation struct {
	Attribute    int32
	Expression   expr.Expr
	Dependencies []catalog.ExpressionDependency // Original USING binder manifest; borrowed by serial execution preparation.
	Rewrite      bool
}
