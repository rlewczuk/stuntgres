package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestRecoveryInfoTypedValues(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, tc := range []struct { name string; oid ids.OID; typ sqltype.ID; kind functions.RecoveryInfoKind }{
		{"pg_is_in_recovery", 3810, sqltype.Bool, functions.RecoveryInProgress},
		{"pg_last_wal_receive_lsn", 3820, sqltype.PgLSN, functions.RecoveryReceiveLSN},
		{"pg_last_wal_replay_lsn", 3821, sqltype.PgLSN, functions.RecoveryReplayLSN},
		{"pg_last_xact_replay_timestamp", 3830, sqltype.Timestamptz, functions.RecoveryReplayTimestamp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, _, evaluation := strictNullUnitBind(t, e, op, scope, tc.name+"()")
			n := node.(functions.RecoveryInfo)
			procedure, err := exec.ExpressionRoutineDescriptor(n)
			if err != nil || procedure == nil || procedure.Address() != (ids.ObjectAddress{ClassID: 1255, ObjectID: tc.oid}) || procedure.Result != ids.OID(tc.typ) || procedure.ReturnsSet() || procedure.Volatility != expr.Volatile { t.Fatal("selected declaration", procedure, err) }
			if n.Kind != tc.kind || n.Usage.Type.ID() != tc.typ || n.Validate() != nil || len(n.Children()) != 0 || n.IntrinsicVolatility() != expr.Volatile || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep { t.Fatal("typed volatile leaf", n) }
			if admitted, set := n.SourceAdmission(); !admitted || set { t.Fatal("scalar source admission") }
			value, err := n.Eval(&evaluation)
			if err != nil || value.Type() != n.Usage.Type { t.Fatal("bound descriptor lost", value, err) }
			if tc.kind == functions.RecoveryInProgress {
				flag, err := value.Boolean()
				if err != nil || flag || value.IsNull() { t.Fatal("recovery false", value, err) }
			} else if !value.IsNull() { t.Fatal("fabricated recovery progress", value) }
			source, err := n.OpenSource(&evaluation)
			if err != nil || source.Type != n.Usage.Type { t.Fatal("typed scalar source", source, err) }
			emitted, present, err := source.Next(context.Background())
			if err != nil || !present || emitted.Type() != n.Usage.Type || !reflect.DeepEqual(emitted, value) {
				t.Fatal("scalar source must emit its typed value once", emitted, present, err)
			}
			eof, present, err := source.Next(context.Background())
			if err != nil || present || eof.Type() != n.Usage.Type || !eof.IsNull() || !source.Exhausted {
				t.Fatal("scalar source must retain typed EOF after its one row", eof, present, err)
			}
			source.Close()
			original := n
			info := n.Info
			info.Occurrence++
			rebuilt, err := n.Rebuild(info, nil)
			if err != nil || !reflect.DeepEqual(rebuilt, functions.RecoveryInfo{Info: info, Kind: tc.kind}) || !reflect.DeepEqual(original, n) { t.Fatal("immutable recipe rebuild", rebuilt, err) }
			if _, err := n.Rebuild(n.Info, []expr.Expr{n}); err == nil { t.Fatal("accepted children") }
			for _, bad := range []functions.RecoveryInfo{{Info: n.Info, Kind: 255}, {Kind: tc.kind}} {
				if bad.Validate() == nil { t.Fatal("invalid kind/nil descriptor accepted") }
			}
			bad := n
			bad.Usage.Type, _ = e.CatalogRead().LookupType(sqltype.Text)
			if _, err := n.Rebuild(bad.Info, nil); err == nil { t.Fatal("wrong scalar result accepted") }
			base, cancel := context.WithCancel(context.Background())
			cancel()
			evaluation.Context = base
			if _, err := n.Eval(&evaluation); err != context.Canceled { t.Fatal("context error suppressed", err) }
		})
	}
}

func TestRecoveryInfoShapeValidation(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, _, _ := strictNullUnitBind(t, e, op, scope, "pg_get_wal_resource_managers()")
	n := node.(functions.WALResourceManagers)
	procedure, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil || procedure == nil || procedure.Address() != (ids.ObjectAddress{ClassID: 1255, ObjectID: 6224}) || procedure.Result != 2249 || !procedure.ReturnsSet() || procedure.RowEstimate() != 50 || procedure.Volatility != expr.Volatile { t.Fatal("released set declaration", procedure, err) }
	outputs := procedure.OutputDeclaration()
	if outputs == nil || outputs.Inputs() != 0 || outputs.Len() != 3 || n.Validate() != nil { t.Fatal("fixed outputs", outputs, n) }
	names := []string{"rm_id", "rm_name", "rm_builtin"}
	types := []ids.OID{23, 25, 16}
	members := make([]sqltype.CompositeMember, 3)
	for i := range members {
		members[i] = n.Usage.Type.CompositeMember(i)
		if outputs.Name(i) != names[i] || outputs.Type(i) != types[i] || members[i].Name != names[i] || members[i].Usage.Type.ID() != sqltype.ID(types[i]) || members[i].Attribute != int32(i+1) { t.Fatal("OUT identity", i, members[i]) }
	}
	if admitted, set := n.SourceAdmission(); !admitted || !set { t.Fatal("set admission") }
	if len(n.Children()) != 0 || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep || n.IntrinsicVolatility() != expr.Volatile { t.Fatal("set protocols") }
	for _, kind := range []string{"nil", "scalar", "count", "name", "type", "attribute"} {
		bad := n
		switch kind {
		case "nil": bad.Usage.Type = nil
		case "scalar": bad.Usage.Type, _ = e.CatalogRead().LookupType(sqltype.Text)
		default:
			changed := append([]sqltype.CompositeMember(nil), members...)
			switch kind {
			case "count": changed = changed[:2]
			case "name": changed[1].Name = "wrong"
			case "type": changed[1].Usage = members[0].Usage
			case "attribute": changed[1].Attribute = 4
			}
			shape, err := sqltype.RecordShape(changed)
			if err != nil { t.Fatal(err) }
			bad.Usage = shape.Usage()
		}
		if bad.Validate() == nil { t.Fatal("malformed record accepted", kind) }
		if _, err := n.Rebuild(bad.Info, nil); err == nil { t.Fatal("rebuild accepted malformed record", kind) }
	}
	if _, err := n.Rebuild(n.Info, []expr.Expr{n}); err == nil { t.Fatal("set children accepted") }
	info := n.Info
	info.Occurrence++
	rebuilt, err := n.Rebuild(info, nil)
	if err != nil || !reflect.DeepEqual(rebuilt, functions.WALResourceManagers{Info: info}) || rebuilt.ResultUsage().Type != n.Usage.Type || rebuilt.(functions.WALResourceManagers).RoutineReference != n.RoutineReference { t.Fatal("record backing/authority lost", rebuilt, err) }
}

func TestRecoveryInfoSourceBoundary(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_get_wal_resource_managers()")
	n := node.(functions.WALResourceManagers)
	before := n
	for range 2 {
		source, err := n.OpenSource(&evaluation)
		if err == nil || source.Type != nil || source.Values != nil || source.State != nil || source.Advance != nil { t.Fatal("physical source published", source, err) }
		state, ok := err.(interface { SQLState() string })
		if !ok || state.SQLState() != "0A000" || err.Error() != "stuntgres: pg_get_wal_resource_managers physical WAL resource manager registry is unsupported in embedded mode" { t.Fatal("physical boundary diagnostic", err) }
	}
	if !reflect.DeepEqual(n, before) { t.Fatal("opening mutated retained recipe") }
	bad := n
	bad.Usage.Type = nil
	if _, err := bad.OpenSource(&evaluation); err == nil || err.Error() != "stuntgres: invalid WAL resource manager record" { t.Fatal("validation hidden", err) }
	base, cancel := context.WithCancel(context.Background())
	cancel()
	evaluation.Context = base
	if _, err := n.OpenSource(&evaluation); err != context.Canceled { t.Fatal("context hidden", err) }
}
