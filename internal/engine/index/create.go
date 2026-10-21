package index

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// CreatePlan is a CREATE INDEX decision: the index descriptor to stage, its
// name key and namespace (for the IF NOT EXISTS recheck after a conflict),
// or Skip when IF NOT EXISTS found the name taken.
type CreatePlan struct {
	Relation  *catalog.Relation
	Key       ids.NameKey
	Namespace ids.ObjectRef
	Skip      bool
}

// PrepareCreate makes the CREATE INDEX decisions on parent in PostgreSQL's
// order: Restore authority, column limit, relation kind, CREATE privilege,
// method, clauses, predicate and attributes, name, operator class options,
// unique providers, partition coverage, then the new identities. A
// CONCURRENTLY index starts neither ready nor valid.
func PrepareCreate(h Host, auth ddl.Authority, parent *catalog.Relation, s *parser.CreateIndex, input sqltype.InputEnvironment) (CreatePlan, error) {
	trusted, err := h.TrustedRestoreDestination(auth, "index")
	if err != nil {
		return CreatePlan{}, err
	}
	if len(s.Keys)+len(s.Include) > 32 {
		return CreatePlan{}, sqlError("54011", "cannot use more than 32 columns in an index")
	}
	if parent.RelationKind != catalog.TableRelation && parent.RelationKind != catalog.MaterializedViewRelation && parent.RelationKind != catalog.PartitionedRelation {
		if parent.RelationKind == catalog.IndexRelation {
			return CreatePlan{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot open relation "%s"`, parent.Name()), Detail: "This operation is not supported for indexes."}
		}
		kind, _ := h.RelationDiagnostic(auth, parent)
		return CreatePlan{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot create index on relation "%s"`, parent.Name()), Detail: fmt.Sprintf("This operation is not supported for %ss.", kind)}
	}
	ns := ddl.ObjectRef(h, auth, parent.Namespace())
	// A trusted Restore already proved its authority above, so the 42501
	// exception of CheckCreateIn applies exactly to it.
	if err = h.CheckCreateIn(auth, ns); err != nil {
		return CreatePlan{}, err
	}
	if err = ValidateIndexMethod(s); err != nil {
		return CreatePlan{}, err
	}
	if s.Concurrent.Character != 0 && s.Predicate != nil {
		return CreatePlan{}, sqlError("0A000", "partial concurrent indexes are unsupported")
	}
	if len(s.Options) != 0 {
		if err := ValidateIndexReloptions(s.Options, MethodOf(s.Method), false); err != nil {
			return CreatePlan{}, err
		}
	}
	predicate, err := h.BindIndexPredicate(auth, parent, s.Predicate, input)
	if err != nil {
		return CreatePlan{}, err
	}
	definition, columns, attributes, lookup, err := BindAttributes(h, auth, input, parent, s)
	if err != nil {
		return CreatePlan{}, err
	}
	definition.Predicate = predicate
	view := h.AccessView(auth).Catalog
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
		name = ddl.GeneratedRelationName(parent.Name(), addition.String(), "idx", func(n string) bool {
			_, ok := view.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: n})
			return ok
		})
	}
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: name}
	if _, exists := view.LookupName(key); exists {
		if s.IfNotExists {
			return CreatePlan{Key: key, Namespace: ns, Skip: true}, nil
		}
		return CreatePlan{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
	}
	// The released provider-options phase follows catalog name admission.
	for i, raw := range s.Keys {
		if len(raw.Options) != 0 {
			return CreatePlan{}, sqlError("22023", fmt.Sprintf("operator class %s has no options", definition.Keys[i].Class.Name))
		}
	}
	definition.Unique, definition.NullsNotDistinct = s.Unique, s.NullsNotDistinct
	if s.Unique && s.Concurrent.Character == 0 && !trusted {
		if err := ValidateUniqueProviders(parent, definition, s.Keys); err != nil {
			return CreatePlan{}, err
		}
	}
	if err := PartitionUniqueCoverage(parent, definition); err != nil {
		return CreatePlan{}, err
	}
	if len(s.Options) != 0 {
		return CreatePlan{}, Capability("index storage parameters are unsupported", s.WithPos)
	}
	oid, err := h.AllocateOID()
	if err != nil {
		return CreatePlan{}, err
	}
	revision, err := h.Revision()
	if err != nil {
		return CreatePlan{}, err
	}
	for i := range columns {
		columns[i].OriginTable = uint32(oid)
		columns[i].OriginAttribute = int16(i + 1)
	}
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: parent.Namespace(), ObjectName: name, ObjectRevision: revision, Owner: parent.Owner, Columns: columns, Attributes: attributes, Index: lookup, RelationKind: catalog.IndexRelation, SQLIndex: definition}
	d.ColumnMeta.HighWater = int32(len(attributes))
	if s.Concurrent.Character != 0 {
		definition.Ready, definition.Valid = false, false
	}
	return CreatePlan{Relation: d, Key: key, Namespace: ns}, nil
}

// StageCreate stages a prepared index on parent. On a partitioned parent,
// ONLY leaves it valid only without partitions; otherwise every partition
// gets an attached child
func StageCreate(h Host, parent, idx *catalog.Relation, only bool) error {
	if err := h.Stage().Create(idx); err != nil {
		return err
	}
	if parent.Partition == nil {
		return nil
	}
	if only {
		// The staged descriptor shares this definition.
		idx.SQLIndex.Valid = len(parent.Partition.Children) == 0
		return nil
	}
	return StagePartitionIndexChildren(h, parent, idx, true)
}
