package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Slot zero remains pages. These released declarations occupy recipe slots 1–3.
var PgBuffercacheEvictionMembers = [...]struct {
	Name string
	Signature catalog.ProcedureSignature
	Kind functions.PgBuffercacheEvictionKind
	Strict bool
	Declaration catalog.InstalledRoutineDeclaration
}{
	{"pg_buffercache_evict", "23", functions.EvictBuffer, true, catalog.InstalledRoutineDeclaration{Parallel: 's', Outputs: catalog.NewRoutineOutputs([]ids.OID{16, 16}, "", "buffer_evicted", "buffer_flushed")}},
	{"pg_buffercache_evict_relation", "2205", functions.EvictRelation, true, catalog.InstalledRoutineDeclaration{Parallel: 's', Outputs: catalog.NewRoutineOutputs([]ids.OID{23, 23, 23}, "", "buffers_evicted", "buffers_flushed", "buffers_skipped")}},
	{"pg_buffercache_evict_all", "", functions.EvictAll, false, catalog.InstalledRoutineDeclaration{Parallel: 's', Outputs: catalog.NewRoutineOutputs([]ids.OID{23, 23, 23}, "buffers_evicted", "buffers_flushed", "buffers_skipped")}},
}

func (provider *PgBuffercacheProvider) stageEvictions(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	for _, member := range PgBuffercacheEvictionMembers {
		oid, err := stage.AllocateOID()
		if err != nil { return err }
		revision, err := stage.Revision()
		if err != nil { return err }
		p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.Name, Signature: member.Signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 2249, Volatility: expr.Volatile, Strict: member.Strict, Installed: member.Declaration, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: member.Name, Binary: "$libdir/pg_buffercache", HasBinary: true}, Implementation: provider}
		if stage.LookupName(catalog.DescriptorNameKey(p)) != (ids.ObjectAddress{}) {
			context := ""
			switch member.Kind {
			case functions.EvictBuffer:
				// Installation first encounters the historical declaration,
				// before 1.6 replaces it with the final two-OUT record.
				context = "SQL statement \"CREATE FUNCTION pg_buffercache_evict(IN int)\nRETURNS bool\nAS '$libdir/pg_buffercache', 'pg_buffercache_evict'\nLANGUAGE C PARALLEL SAFE VOLATILE STRICT\"\nextension script file \"pg_buffercache--1.4--1.5.sql\", near line 3"
			case functions.EvictRelation:
				context = "SQL statement \"CREATE FUNCTION pg_buffercache_evict_relation(\n    IN regclass,\n    OUT buffers_evicted int4,\n    OUT buffers_flushed int4,\n    OUT buffers_skipped int4)\nAS '$libdir/pg_buffercache', 'pg_buffercache_evict_relation'\nLANGUAGE C PARALLEL SAFE VOLATILE STRICT\"\nextension script file \"pg_buffercache--1.5--1.6.sql\", near line 33"
			case functions.EvictAll:
				context = "SQL statement \"CREATE FUNCTION pg_buffercache_evict_all(\n    OUT buffers_evicted int4,\n    OUT buffers_flushed int4,\n    OUT buffers_skipped int4)\nAS '$libdir/pg_buffercache', 'pg_buffercache_evict_all'\nLANGUAGE C PARALLEL SAFE VOLATILE\"\nextension script file \"pg_buffercache--1.5--1.6.sql\", near line 41"
			}
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", member.Name), Context: context}
		}
		p.ACL = stage.CreationRoutineACL(owner, ext.TargetNamespace)
		if err := stage.Create(p); err != nil { return err }
		ext.Members = append(ext.Members, p.Address())
	}
	return nil
}

func ValidatePgBuffercacheEvictionCall(v catalog.Read, proc *catalog.Procedure, call functions.PgBuffercacheEviction) error {
	if proc == nil || PgBuffercacheProviderOf(proc) != StockPgBuffercache || call.RoutineReference != proc {
		return fmt.Errorf("stuntgres: invalid pg_buffercache eviction identity")
	}
	if _, err := ProcedureSymbol(v, proc); err != nil { return err }
	if proc.Origin.Declaration == 0 || int(proc.Origin.Declaration) > len(PgBuffercacheEvictionMembers) {
		return fmt.Errorf("stuntgres: invalid pg_buffercache eviction declaration")
	}
	member := PgBuffercacheEvictionMembers[proc.Origin.Declaration-1]
	if call.Kind != member.Kind { return fmt.Errorf("stuntgres: mismatched pg_buffercache eviction operation") }
	if err := call.ValidateArguments(call.Args); err != nil { return err }
	d, outputs := call.ResultUsage().Type, proc.OutputDeclaration()
	if d == nil || outputs == nil || d.CompositeLen() != outputs.Len() {
		return fmt.Errorf("stuntgres: invalid pg_buffercache eviction record")
	}
	for i := range outputs.Len() {
		field := d.CompositeMember(i)
		typ, ok := v.LookupType(sqltype.ID(outputs.Type(i)))
		if !ok || field.Attribute != int32(i+1) || field.Name != outputs.Name(outputs.Inputs()+i) || field.Usage != typ.Usage() {
			return fmt.Errorf("stuntgres: invalid pg_buffercache eviction OUT field")
		}
	}
	return nil
}
