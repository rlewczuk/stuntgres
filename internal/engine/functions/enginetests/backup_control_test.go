package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestBackupControlTypedRecipes(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, tc := range []struct {
		call string
		code string
	}{
		{"pg_backup_start('x')", "0A000"}, {"pg_backup_start('x',true)", "0A000"}, {"pg_backup_stop()", "0A000"}, {"pg_backup_stop(false)", "0A000"},
		{"pg_backup_start(NULL)", ""}, {"pg_backup_start('x',NULL)", ""}, {"pg_backup_stop(NULL)", ""},
		{"pg_backup_start('" + strings.Repeat("é", 512) + "')", "0A000"}, {"pg_backup_start('" + strings.Repeat("é", 513) + "')", "22023"},
		{"pg_backup_start('" + strings.Repeat("é", 513) + "',NULL)", ""},
	} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, tc.call)
		n := node.(functions.BackupControl)
		before := n
		d, err := exec.ExpressionRoutineDescriptor(n)
		if err != nil || d == nil || !d.Strict || d.Volatility != expr.Volatile || d.Builtin.Parallel != 'r' || d.ReturnsSet() {
			t.Fatal("typed declaration", d, err)
		}
		if n.Validate() != nil || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep || n.IntrinsicVolatility() != expr.Volatile {
			t.Fatal("recipe", n)
		}
		value, err := n.Eval(&evaluation)
		if tc.code == "" {
			if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
				t.Fatal("strict typed NULL", value, err)
			}
		} else {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
				t.Fatal("boundary", err)
			}
			want := "backup label too long (max 1024 bytes)"
			if tc.code == "0A000" {
				want = n.Kind.Name() + " physical online backup lifecycle is unsupported in embedded mode"
			}
			if diagnostic.Message != want || diagnostic.Position != "" {
				t.Fatal(diagnostic)
			}
			cursor, err := n.OpenSource(&evaluation)
			if err == nil || cursor.Type != nil || cursor.State != nil {
				t.Fatal("published physical source", cursor, err)
			}
		}
		if !reflect.DeepEqual(n, before) {
			t.Fatal("physical state retained")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		evaluation.Context = ctx
		if _, err := n.Eval(&evaluation); err != context.Canceled {
			t.Fatal("cancellation hidden", err)
		}
	}
}
func TestBackupControlRebuildValidation(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, _, _ := strictNullUnitBind(t, e, op, scope, "pg_backup_start(fast=>false,label=>'x')")
	n := node.(functions.BackupControl)
	before := n
	if n.Slots != [2]uint8{1, 0} || n.Arity != 2 {
		t.Fatal("formal mapping", n)
	}
	children := n.Children()
	children[0] = nil
	if n.Args[0] == nil {
		t.Fatal("borrowed child storage")
	}
	info := n.Info
	info.Occurrence++
	rebuilt, err := n.Rebuild(info, n.Children())
	if err != nil {
		t.Fatal(err)
	}
	next := rebuilt.(functions.BackupControl)
	if next.Info != info || next.RoutineReference != n.RoutineReference || next.Args != n.Args || !reflect.DeepEqual(n, before) {
		t.Fatal("rebuild authority/provenance")
	}
	rendered, err := next.Render()
	if err != nil || rendered.Args[0].Label != "fast" || rendered.Args[1].Label != "label" {
		t.Fatal(rendered, err)
	}
	capture, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || capture.Count != 2 || !capture.Indexed {
		t.Fatal(capture, err)
	}
	for i := range 2 {
		child, err := n.CaptureOperand(i)
		if err != nil || child != n.Args[i] {
			t.Fatal("written capture", child, err)
		}
	}
	if _, err := n.CaptureOperand(2); err == nil {
		t.Fatal("capture out of range")
	}
	destination, destinationOp, destinationScope := strictNullUnitFixture(t)
	destNode, _, _ := strictNullUnitBind(t, destination, destinationOp, destinationScope, "pg_backup_start(fast=>true,label=>'destination')")
	dest := destNode.(functions.BackupControl)
	mapped, err := n.Rebuild(dest.Info, dest.Children())
	if err != nil {
		t.Fatal(err)
	}
	if mapped.(functions.BackupControl).RoutineReference != dest.RoutineReference || mapped.(functions.BackupControl).RoutineReference == n.RoutineReference || mapped.ResultUsage() != dest.Usage {
		t.Fatal("replacement reinstated source authority")
	}
	for _, change := range []string{"kind", "result", "arity", "nil", "type", "duplicate", "range", "unused", "required"} {
		bad := n
		switch change {
		case "kind":
			bad.Kind = 255
		case "result":
			bad.Usage.Type, _ = e.CatalogRead().LookupType(sqltype.Text)
		case "arity":
			bad.Arity = 3
		case "nil":
			bad.Args[0] = nil
		case "type":
			bad.Args[0] = bad.Args[1]
		case "duplicate":
			bad.Slots[1] = bad.Slots[0]
		case "range":
			bad.Slots[0] = 2
		case "unused":
			bad.Arity = 1
		case "required":
			bad.Arity = 0
			bad.Args = [2]expr.Expr{}
			bad.Slots = [2]uint8{}
		}
		if bad.Validate() == nil || bad.ValidateStoredProvider() == nil {
			t.Fatal("accepted malformed recipe", change)
		}
	}
	if _, err := n.Rebuild(n.Info, nil); err == nil {
		t.Fatal("accepted missing children")
	}
	children = n.Children()
	children[0] = nil
	if _, err := n.Rebuild(n.Info, children); err == nil {
		t.Fatal("accepted nil child")
	}
	node, _, _ = strictNullUnitBind(t, e, op, scope, "pg_backup_stop(NULL)")
	stop := node.(functions.BackupControl)
	for _, change := range []string{"name", "attribute", "type", "count"} {
		members := make([]sqltype.CompositeMember, 3)
		for i := range members {
			members[i] = stop.Usage.Type.CompositeMember(i)
		}
		switch change {
		case "name":
			members[0].Name = "wrong"
		case "attribute":
			members[0].Attribute = 9
		case "type":
			text, _ := e.CatalogRead().LookupType(sqltype.Text)
			members[0].Usage = text.Usage()
		case "count":
			members = members[:2]
		}
		shape, err := sqltype.RecordShape(members)
		if err != nil {
			t.Fatal(err)
		}
		bad := stop
		bad.Usage = shape.Usage()
		if bad.Validate() == nil {
			t.Fatal("accepted wrong record", change)
		}
	}
	if !reflect.DeepEqual(n, before) {
		t.Fatal("rejection changed original")
	}
}
func TestBackupControlCatalogSeeds(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	if got := slices.Collect(routine.RestrictedBackupControlRoutines()); !slices.Equal(got, []ids.OID{2172, 2739}) {
		t.Fatal("restricted roster", got)
	}
	for i, call := range []string{"pg_backup_start('x')", "pg_backup_stop()"} {
		node, _, _ := strictNullUnitBind(t, e, op, scope, call)
		d, err := exec.ExpressionRoutineDescriptor(node)
		if err != nil {
			t.Fatal(err)
		}
		wantOID, wantSignature, wantResult := ids.OID(2172), "25,16", ids.OID(3220)
		if i == 1 {
			wantOID, wantSignature, wantResult = 2739, "16", 2249
		}
		if d.Address().ObjectID != wantOID || string(d.Signature) != wantSignature || d.Result != wantResult {
			t.Fatal("selected full routine identity", d)
		}
		want := authorization.ACLEntry{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: d.Owner}, Privileges: authorization.ExecutePrivilege}
		if !d.ACL.Explicit || !slices.Equal(d.ACL.Entries, []authorization.ACLEntry{want}) {
			t.Fatal("owner-only seed", d.ACL)
		}
		var initial *catalog.InitialPrivilegeRecord
		for address, record := range e.CatalogRead().State().InitialPrivileges() {
			if address == d.Address() {
				initial = record
			}
		}
		if initial == nil || initial.PrivType != 'i' || !reflect.DeepEqual(initial.ACL, d.ACL) {
			t.Fatal("historical seed", initial)
		}
		if &initial.ACL.Entries[0] == &d.ACL.Entries[0] {
			t.Fatal("historical ACL aliases mutable current ACL")
		}
		defaultText := "false"
		if i == 1 {
			defaultText = "true"
		}
		if d.Builtin.Defaults != 1 || d.LiteralDefaults.Len() != 1 || d.LiteralDefaults.At(0) != (catalog.RoutineLiteral{SourceType: 16, Text: defaultText}) {
			t.Fatal("default identity", d)
		}
	}
	base := e.CatalogRead()
	start := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 2172}
	stop := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 2739}
	original := base.State().InitialPrivilege(start)
	history := authorization.ACLValue{Explicit: true, Entries: slices.Clone(original.ACL.Entries)}
	history.Entries[0].GrantOptions = authorization.ExecutePrivilege
	candidate := catalog.NewCandidate(base)
	candidate.RecordInitialPrivilege(start, 'i', history)
	candidate.RemoveInitialPrivileges(stop)
	metadata, err := control.CaptureInitialPrivileges(context.Background(), base.WithState(candidate.State()), e.Roles())
	if err != nil || metadata == nil || !metadata.Complete {
		t.Fatal("changed independent history omitted", metadata, err)
	}
	comment, err := metadata.Comment()
	if err != nil {
		t.Fatal(err)
	}
	decoded, recognized, err := control.DecodeInitialPrivileges(strings.TrimSpace(strings.TrimPrefix(comment, "-- ")))
	if err != nil || !recognized || !reflect.DeepEqual(metadata, decoded) {
		t.Fatal("historical capture roundtrip", decoded, err)
	}
	destination, _, _ := strictNullUnitFixture(t)
	records, err := control.ResolveInitialPrivileges(context.Background(), destination.CatalogRead(), destination.Roles(), decoded)
	if err != nil || records[stop] != nil || records[start] == nil || records[start].ACL.Entries[0].GrantOptions != authorization.ExecutePrivilege {
		t.Fatal("historical edit/deletion replay", records[start], err)
	}
	if records[start].ACL.Entries[0].Grantor == history.Entries[0].Grantor || base.State().InitialPrivilege(start) != original || base.State().InitialPrivilege(stop) == nil || original.ACL.Entries[0].GrantOptions != 0 {
		t.Fatal("history remap imported authority or mutated source")
	}
}
