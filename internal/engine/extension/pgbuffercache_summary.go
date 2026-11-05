package extension

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Append after pages (0) and all eviction declarations (1–3).
const PgBuffercacheAggregateStart = 4

var PgBuffercacheAggregateMembers = [...]struct {
	Name string
	Kind functions.PgBuffercacheAggregateKind
	Declaration catalog.InstalledRoutineDeclaration
}{
	{"pg_buffercache_summary", functions.BuffercacheSummary, catalog.InstalledRoutineDeclaration{Parallel: 's', Outputs: catalog.NewRoutineOutputs([]ids.OID{23, 23, 23, 23, 701}, "buffers_used", "buffers_unused", "buffers_dirty", "buffers_pinned", "usagecount_avg")}},
	{"pg_buffercache_usage_counts", functions.BuffercacheUsageCounts, catalog.InstalledRoutineDeclaration{Parallel: 's', ReturnsSet: true, Rows: 1000, Outputs: catalog.NewRoutineOutputs([]ids.OID{23, 23, 23, 23}, "usage_count", "buffers", "dirty", "pinned")}},
}

func (provider *PgBuffercacheProvider) stageAggregates(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	monitor := stage.CurrentRead().Owner().Principals.Principal(3373)
	for _, member := range PgBuffercacheAggregateMembers {
		oid, err := stage.AllocateOID()
		if err != nil { return err }
		revision, err := stage.Revision()
		if err != nil { return err }
		p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.Name, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 2249, Volatility: expr.Volatile, Installed: member.Declaration, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: member.Name, Binary: "$libdir/pg_buffercache", HasBinary: true}, Implementation: provider}
		if stage.LookupName(catalog.DescriptorNameKey(p)) != (ids.ObjectAddress{}) {
			statement := "CREATE FUNCTION pg_buffercache_summary(\n    OUT buffers_used int4,\n    OUT buffers_unused int4,\n    OUT buffers_dirty int4,\n    OUT buffers_pinned int4,\n    OUT usagecount_avg float8)\nAS '$libdir/pg_buffercache', 'pg_buffercache_summary'\nLANGUAGE C PARALLEL SAFE"
			line := 6
			if member.Kind == functions.BuffercacheUsageCounts {
				statement = "CREATE FUNCTION pg_buffercache_usage_counts(\n    OUT usage_count int4,\n    OUT buffers int4,\n    OUT dirty int4,\n    OUT pinned int4)\nRETURNS SETOF record\nAS '$libdir/pg_buffercache', 'pg_buffercache_usage_counts'\nLANGUAGE C PARALLEL SAFE"
				line = 15
			}
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", member.Name), Context: fmt.Sprintf("SQL statement \"%s\"\nextension script file \"pg_buffercache--1.3--1.4.sql\", near line %d", statement, line)}
		}
		p.ACL, err = stage.RoutineCreationACLWithoutPublic(owner, ext.TargetNamespace)
		if err != nil { return err }
		p.ACL, _, err = authorization.ChangeObjectACL(context.Background(), stage.Roles(), p.ACL, owner, owner, []authorization.ACLGrantee{{Role: monitor}}, authorization.ExecutePrivilege, false, false, false, false)
		if err != nil { return err }
		if err := stage.Create(p); err != nil { return err }
		stage.RecordInitialPrivilege(p.Address(), 'e', p.ACL)
		ext.Members = append(ext.Members, p.Address())
	}
	return nil
}

func ValidatePgBuffercacheAggregateCall(v catalog.Read, proc *catalog.Procedure, call functions.PgBuffercacheAggregate) error {
	if proc == nil || PgBuffercacheProviderOf(proc) != StockPgBuffercache || call.RoutineReference != proc {
		return fmt.Errorf("stuntgres: invalid pg_buffercache aggregate identity")
	}
	if _, err := ProcedureSymbol(v, proc); err != nil { return err }
	slot := proc.Origin.Declaration
	if slot < PgBuffercacheAggregateStart || int(slot) >= PgBuffercacheAggregateStart+len(PgBuffercacheAggregateMembers) {
		return fmt.Errorf("stuntgres: invalid pg_buffercache aggregate declaration")
	}
	member := PgBuffercacheAggregateMembers[slot-PgBuffercacheAggregateStart]
	if call.Kind != member.Kind { return fmt.Errorf("stuntgres: mismatched pg_buffercache aggregate operation") }
	if err := call.ValidateArguments(call.Children()); err != nil { return err }
	d, outputs := call.ResultUsage().Type, proc.OutputDeclaration()
	if d == nil || d.ID() != 2249 || call.ResultUsage() != d.Usage() || outputs == nil || d.CompositeLen() != outputs.Len() { return fmt.Errorf("stuntgres: invalid pg_buffercache aggregate record") }
	for i := range outputs.Len() {
		field := d.CompositeMember(i)
		typ, ok := v.LookupType(sqltype.ID(outputs.Type(i)))
		if !ok || field.Attribute != int32(i+1) || field.Name != outputs.Name(i) || field.Usage != typ.Usage() {
			return fmt.Errorf("stuntgres: invalid pg_buffercache aggregate OUT field")
		}
	}
	return nil
}
