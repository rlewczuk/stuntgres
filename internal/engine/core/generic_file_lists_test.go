package core_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

var fileListUnitIDs = []ids.OID{2625, 3297, 3353, 3354, 6271, 6270, 6272, 6400, 5031}

func TestGenericFileListDeclarations(t *testing.T) {
	e, other := engine.New(), engine.New()
	for _, oid := range fileListUnitIDs {
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		p, ok := e.CatalogRead().Procedure(address)
		if !ok {
			t.Fatal("missing declaration", oid)
		}
		q, _ := other.CatalogRead().Procedure(address)
		if p.Builtin == nil || !p.Strict || p.Volatility != expr.Volatile || !p.Builtin.ReturnsSet || p.Builtin.Parallel != 's' || p.Builtin.Defaults != 0 || p.Builtin.Variadic != 0 || p.Builtin.Providers != functions.CatalogRoutineProvider {
			t.Fatal("wrong declaration", oid, p.Builtin)
		}
		want := 2
		if oid == 2625 || oid == 3297 {
			want = 1
		}
		if !p.ACL.Explicit || len(p.ACL.Entries) != want || &p.ACL.Entries[0] == &q.ACL.Entries[0] {
			t.Fatal("restricted ACL ownership", oid)
		}
		roles := e.Roles()
		for i, entry := range p.ACL.Entries {
			target := roles.LookupRef("postgres")
			if i == 1 {
				target = roles.LookupRef("pg_monitor")
			}
			if entry.Grantee.Public || entry.Grantee.Role != target || entry.Grantor != p.Owner || entry.Privileges != authorization.ExecutePrivilege {
				t.Fatal("wrong ACL", oid, entry)
			}
		}
	}
}
func TestGenericFileListBinding(t *testing.T) {
	e := engine.New()
	for kind := functions.GenericFileDir; kind <= functions.GenericFileArchiveStatus; kind++ {
		var args []parser.Expr
		if kind == functions.GenericFileDir || kind == functions.GenericFileReplSlot {
			args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}
		}
		node := catalogRelationsUnitBind(t, e, kind.Name(), args...).(functions.GenericFileList)
		if err := node.Validate(); err != nil {
			t.Fatal(err)
		}
		if !expr.AdmitsSource(node) || !exec.SourceReturnsSet(node) || exec.PlannedVolatility(node) != expr.Volatile {
			t.Fatal("source protocol", kind)
		}
		children := node.Children()
		rebuilt, err := node.Rebuild(node.Info, children)
		if err != nil || !analyze.SameGroupExpression(node, rebuilt) || expr.InfoOf(rebuilt).RoutineReference != node.RoutineReference {
			t.Fatal("reconstruction lost manifest", err)
		}
		if _, err := node.Render(); err != nil {
			t.Fatal(err)
		}
		captured, ok, err := node.Capture(expr.CaptureFunction)
		if err != nil || !ok || captured.Count != len(args) {
			t.Fatal("capture", captured, err)
		}
		for i := range args {
			child, err := captured.At(node, i)
			if err != nil || child != children[i] {
				t.Fatal("capture order", err)
			}
		}
		if len(children) != 0 {
			children[0] = nil
			if node.Args[0] == nil {
				t.Fatal("borrowed child backing")
			}
		}
	}
}
func TestGenericFileListPaths(t *testing.T) {
	for _, tc := range []struct{ input, canonical, log, code string }{
		{"", "", "log", ""}, {".", ".", "log", ""}, {"a/..", ".", "log", ""}, {"a//./b/../", "a", "log", ""},
		{"a/../../x", "../x", "log", "42501"}, {"../", "..", "log", "42501"}, {"../../x", "../../x", "log", "42501"},
		{"/stuntgres/simulated/data", "/stuntgres/simulated/data", "log", ""}, {"/stuntgres/simulated/data/a/../b", "/stuntgres/simulated/data/b", "log", ""},
		{"/stuntgres/simulated/database", "/stuntgres/simulated/database", "log", "42501"}, {"/stuntgres/simulated/data-sibling/x", "/stuntgres/simulated/data-sibling/x", "log", "42501"},
		{"/stuntgres/simulated/data/../outside", "/stuntgres/simulated/outside", "log", "42501"}, {"/etc", "/etc", "log", "42501"},
		{`a\..\b`, `a\..\b`, "log", ""}, {"/logs/server", "/logs/server", "/logs/server", ""}, {"/logs/server/a/../b", "/logs/server/b", "/logs/server", ""}, {"/logs/server-old", "/logs/server-old", "/logs/server", "42501"},
	} {
		got, err := functions.CheckGenericFileListPath(tc.input, "/stuntgres/simulated/data", tc.log, false)
		if got != tc.canonical {
			t.Fatal(tc.input, got)
		}
		if tc.code == "" {
			if err != nil {
				t.Fatal(tc.input, err)
			}
		} else {
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != tc.code {
				t.Fatal(tc.input, err)
			}
		}
		if got, err := functions.CheckGenericFileListPath(tc.input, "/stuntgres/simulated/data", tc.log, true); got != tc.canonical || err != nil {
			t.Fatal("privileged", tc.input, got, err)
		}
	}
	e := engine.New()
	admin := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, admin, "CREATE ROLE gfl_path_actor")
	session := enginetest.StartSession(t, e, nil)
	// Establish a trusted internal settings fixture, not a live SQL SET or
	// provider override of a Sighup setting.
	definition := e.Settings.Definitions()["log_directory"]
	log, err := definition.Parse("/logs//server/")
	if err != nil {
		t.Fatal(err)
	}
	session.Settings.Mu.Lock()
	session.Settings.Values["log_directory"] = definition.Format(log)
	session.Settings.Typed["log_directory"] = log
	session.Settings.Mu.Unlock()
	enginetest.SessionSQL(t, session, "SET ROLE gfl_path_actor")
	op, err := session.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	host := core.HostOf(scope)
	for _, tc := range []struct{ dirname, code string }{{"/logs/server", "0A000"}, {"/logs/server/a/../b", "0A000"}, {"/logs/server-old", "42501"}, {"/tmp/../etc", "42501"}} {
		var diagnostic *sqlstate.Error
		if err := host.GenericFileList(functions.GenericFileDir, tc.dirname); !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatal("live absolute log root", tc, err)
		}
	}
	relative := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, relative, "SET ROLE gfl_path_actor")
	relativeOp, err := relative.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer relativeOp.Done()
	relativeScope, err := e.StartStatement(relativeOp)
	if err != nil {
		t.Fatal(err)
	}
	defer relativeScope.Finish(context.Canceled)
	var diagnostic *sqlstate.Error
	if err := core.HostOf(relativeScope).GenericFileList(functions.GenericFileDir, "/logs/server"); !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
		t.Fatal("relative log root whitelisted absolute path", err)
	}
}

type fileListOperand struct {
	plan.LiteralExpr
	order *[]int
	index int
}

func (n fileListOperand) Eval(expr.Context) (sqltype.Value, error) {
	*n.order = append(*n.order, n.index)
	return n.Value, nil
}
func TestGenericFileListOwnership(t *testing.T) {
	e := engine.New()
	scope := tablespaceInfoUnitScope(t, e)
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	order := []int{}
	n := functions.GenericFileList{Info: expr.Info{Usage: text.Usage()}, Kind: functions.GenericFileDir, Arity: 3}
	for i := range 3 {
		typ := boolean
		if i == 0 {
			typ = text
		}
		n.Args[i] = fileListOperand{plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: sqltype.Null(typ)}, &order, i}
	}
	for range 2 {
		cursor, err := n.OpenSource(c)
		if err != nil {
			t.Fatal(err)
		}
		value, present, err := cursor.Next(context.Background())
		if err != nil || present || !value.IsNull() || value.Type() != text || cursor.State != nil || cursor.Values != nil {
			t.Fatal("typed empty ownership", value, present, err)
		}
		cursor.Close()
	}
	if !reflect.DeepEqual(order, []int{0, 1, 2, 0, 1, 2}) {
		t.Fatal("argument order", order)
	}
	before := e.CatalogRead().State()
	err := core.HostOf(scope).GenericFileList(functions.GenericFileDir, "/etc")
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "0A000" || d.Message != "pg_ls_dir physical directory listing is unsupported in embedded mode" || before != e.CatalogRead().State() {
		t.Fatal("physical boundary", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	c.Context = canceled
	if _, err := n.OpenSource(c); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancellation", err)
	}
}
func TestGenericFileListACLPersistence(t *testing.T) {
	e := engine.New()
	var baseline bytes.Buffer
	if err := engine.Dump(context.Background(), e, &baseline); err != nil || baseline.Len() != 0 {
		t.Fatal("untouched seed must not add dump repairs", baseline.String(), err)
	}
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE ROLE gfl_grantor", "CREATE ROLE gfl_reader", "GRANT EXECUTE ON FUNCTION pg_ls_dir(text) TO gfl_grantor WITH GRANT OPTION", "SET ROLE gfl_grantor", "GRANT EXECUTE ON FUNCTION pg_ls_dir(text) TO gfl_reader", "RESET ROLE"} {
		enginetest.SessionSQL(t, s, q)
	}
	owner := e.Roles().LookupRef("postgres")
	monitor := e.Roles().LookupRef("pg_monitor")
	for _, tc := range []struct {
		oid ids.OID
		acl authorization.ACLValue
	}{
		{3297, authorization.ACLValue{}},
		{3353, authorization.ACLValue{Explicit: true}},
		{3354, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
			{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.ExecutePrivilege},
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege},
		}}},
		{6270, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: monitor}, Privileges: authorization.ExecutePrivilege},
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege},
		}}},
	} {
		view := e.CatalogRead()
		ref := e.Catalog.Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: tc.oid})
		if err := enginetest.SetObjectACL(e, context.Background(), ref, tc.acl); err != nil {
			t.Fatal(err)
		}
	}
	enginetest.SessionSQL(t, s, "GRANT EXECUTE ON FUNCTION pg_ls_summariesdir() TO gfl_reader WITH GRANT OPTION")
	var out bytes.Buffer
	if err := engine.Dump(context.Background(), e, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "proacl=NULL") {
		t.Fatal("NULL restricted ACL omitted")
	}
	target := engine.New()
	dest := enginetest.StartSession(t, target, nil)
	for _, q := range []string{"CREATE ROLE gfl_reader", "CREATE ROLE gfl_grantor"} {
		enginetest.SessionSQL(t, dest, q)
	}
	if err := engine.Restore(context.Background(), target, bytes.NewReader(out.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, oid := range fileListUnitIDs {
		a, _ := e.CatalogRead().Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid})
		b, _ := target.CatalogRead().Procedure(a.Address())
		if a.ACL.Explicit != b.ACL.Explicit || len(a.ACL.Entries) != len(b.ACL.Entries) {
			t.Fatal("raw ACL", oid, a.ACL, b.ACL)
		}
		for i, x := range a.ACL.Entries {
			y := b.ACL.Entries[i]
			grantor, _ := e.Roles().Descriptor(x.Grantor)
			if x.Grantee.Public != y.Grantee.Public || y.Grantor != target.Roles().LookupRef(grantor.Name) || x.Privileges != y.Privileges || x.GrantOptions != y.GrantOptions {
				t.Fatal("remapped ACL", oid, x, y)
			}
			if !x.Grantee.Public {
				grantee, _ := e.Roles().Descriptor(x.Grantee.Role)
				if y.Grantee.Role != target.Roles().LookupRef(grantee.Name) {
					t.Fatal("grantee remap", oid, x, y)
				}
			}
		}
	}
	missing := engine.New()
	state := missing.CatalogRead().State()
	if err := engine.Restore(context.Background(), missing, bytes.NewReader(out.Bytes())); err == nil || missing.CatalogRead().State() != state {
		t.Fatal("missing-role restore atomicity", err)
	}
	// The classifier includes only the selected generic-file identities.
	if routine.GenericFileRoutine(5052) || !routine.GenericFileRoutine(3297) || !routine.GenericFileRoutine(3826) {
		t.Fatal("ACL repair scope")
	}
}
