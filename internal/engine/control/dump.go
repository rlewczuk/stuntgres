package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

// Schema is one ordinary namespace Dump creates.
type Schema struct {
	Name, Owner string
	Configured  bool
}

// OwnedStatement is one owned definition statement: a view, extension or
// index.
type OwnedStatement struct {
	Owner, SQL string
}

// SchemaComment is a schema comment that differs from its baseline.
type SchemaComment struct{ Name, Text string }

// Capture is everything Dump reads from the engine. The root fills it under
// Engine.mu and hands it over: after the unlock Dump reads only the capture,
// sorting and completing it in place, and the root never reads it again.
type Capture struct {
	// CatalogView binds private key names until the terminal renames;
	// OriginalCatalog keeps the captured names for later definitions and
	// values.
	CatalogView, OriginalCatalog catalog.Read
	OriginalRoles, DomainRoles   authorization.Read
	ConstraintReplay             *partition.ConstraintReplay
	ACLOutput                    regobj.ACLItemOutput
	InitialPrivileges            *InitialPrivilegesMetadata

	// Preamble reconstructs bootstrap identity changes before ordinary
	// namespaces are created.
	Preamble              []string
	Schemas               []Schema
	SchemaComments        []SchemaComment
	Sequences             []sequence.Dump
	Domains               []*catalog.Type
	Collations            []*catalog.Collation
	Conversions           []*catalog.Conversion
	Operators             []*catalog.Operator
	AccessMethods         []*catalog.AccessMethod
	OperatorFamilies      []catalog.Descriptor
	OwnedRoutines         []*catalog.Procedure
	Transforms            []*catalog.Transform
	EventTriggers         []*catalog.EventTrigger
	Subscriptions         []*catalog.Subscription
	ReplicationOrigins    []*catalog.ReplicationOrigin
	OriginProgress        map[ids.OID]uint64
	Publications          []*catalog.Publication
	PublicationRelations  []*catalog.PublicationRelation
	PublicationNamespaces []*catalog.PublicationNamespace
	Dictionaries          []*catalog.TSDictionary
	Templates             []*catalog.TSTemplate
	Configurations        []*catalog.TSConfiguration
	Parsers               []*catalog.TSParser
	Statistics            []*catalog.Statistics
	ExtendedStatistics    []storage.ExtendedStatisticsSnapshot
	AttributeStatistics   []storage.AttributeStatisticsSnapshot
	RelationStatistics    []storage.RelationStatisticsSnapshot
	Providers             []providerReplay
	Tables                []table.DumpRelation
	LargeObjects          []*catalog.LargeObject // Separate class-local order, outside Graph's numeric keys.
	Materialized          []table.DumpRelation
	Views                 []OwnedStatement
	Indexes               []OwnedStatement
	ViewIndices           map[ids.OID]int
	TableIndices          map[ids.OID]int
	IndexIndices          map[ids.OID]int
	Composites            map[ids.OID]*catalog.Relation
	Graph                 dump.Graph
	ForeignDeclarations   map[ids.OID]dump.Statement
	// Foreign defaults replay after every provider/name phase, independently
	// of ordinary table deferral and without physical rows or storage.
	ForeignDefaults map[ids.OID]map[int32]bool
	// Deferral removes only eligible definitions' edges. Inline consumers keep
	// their prerequisites, including providers shared with deferred defaults.
	InlinePredecessors map[ids.OID][]ids.OID
	DeferDefaults      bool
}

// Dump orders a captured catalog and writes it to dst as a SQL script. It
// takes over capture: it sorts and completes it in place. Dump reads no engine
// state; it renders against the capture's catalog and role reads only.
func Dump(ctx context.Context, capture *Capture, dst io.Writer) error {
	// Enable exact literal rendering, but bootstrap only if an emitted value
	// actually needs raw replay. Declared REAL columns alone require no helper.
	ctx = regobj.Float4DumpContext(regobj.ACLItemOutputContext(ctx, capture.ACLOutput))
	script, err := buildDumpScript(ctx, capture)
	if err != nil {
		return err
	}
	w := &dumpWriter{ctx: ctx, dst: dst}
	if len(script.foreignKeyModes) != 0 {
		w.seal, w.sealBuffer = sha256.New(), make([]byte, 4096)
	}
	for _, runtime := range capture.CatalogView.Owner().HostRuntimes {
		if err := w.emit(runtime.ReplayComment()); err != nil {
			return err
		}
	}
	if script.rawFloat4 {
		final, exists := capture.CatalogView.Cast(sqltype.Xid, sqltype.Float4)
		if !exists || final.Method != "b" || script.rawFinalDeferred {
			if err := w.forceOwner("postgres"); err != nil {
				return err
			}
			if err := w.emit("CREATE CAST (pg_catalog.xid AS pg_catalog.float4) WITHOUT FUNCTION;\n"); err != nil {
				return err
			}
			script.rawHelper = true
		}
	}
	for _, section := range dumpSections {
		if err := section.emit(ctx, script, w); err != nil {
			return err
		}
	}
	if w.comparisonContext {
		if err := w.emit("-- " + ComparisonContextEnd + "\nRESET search_path;\n"); err != nil {
			return err
		}
	}
	if err := emitDumpPendingDetaches(ctx, script, w); err != nil {
		return err
	}
	if w.seal != nil {
		seal := hex.EncodeToString(w.seal.Sum(nil))
		w.seal = nil // The terminal native footer is excluded from the SQL seal.
		for _, record := range script.foreignKeyModes {
			record.Seal = seal
			comment, err := record.Comment()
			if err != nil {
				return err
			}
			if err := w.emit(comment); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
