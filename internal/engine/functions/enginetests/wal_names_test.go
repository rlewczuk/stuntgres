package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestWALNamesArithmetic(t *testing.T) {
	numeric := seriesUnitType(t, sqltype.Numeric)
	for _, tc := range []struct {
		size, timeline uint32
		lsn            uint64
		name           string
		offset         uint32
		segment        string
	}{
		{16777216, 1, 0, "000000010000000000000000", 0, "0"},
		{16777216, 1, 1, "000000010000000000000000", 1, "0"},
		{16777216, 1, 0xffffff, "000000010000000000000000", 16777215, "0"},
		{16777216, 1, 0x1000000, "000000010000000000000001", 0, "1"},
		{16777216, 1, 0x1000001, "000000010000000000000001", 1, "1"},
		{16777216, 1, 0xffffffff, "0000000100000000000000FF", 16777215, "255"},
		{16777216, 1, 0x100000000, "000000010000000100000000", 0, "256"},
		{16777216, 1, ^uint64(0), "00000001FFFFFFFF000000FF", 16777215, "1099511627775"},
		{1048576, 0, 0x100000, "000000000000000000000001", 0, "1"},
		{1048576, 0, 0x100000000, "000000000000000100000000", 0, "4096"},
		{33554432, 0x80000000, 0x2000000, "800000000000000000000001", 0, "1"},
		{33554432, 0x80000000, 0x1ffffff, "800000000000000000000000", 33554431, "0"},
		{1073741824, 0xffffffff, 0x40000000, "FFFFFFFF0000000000000001", 0, "1"},
		{1073741824, 0xffffffff, ^uint64(0), "FFFFFFFFFFFFFFFF00000003", 1073741823, "17179869183"},
	} {
		m := functions.WALNameMetadata{SegmentSize: tc.size, Timeline: tc.timeline}
		name, offset, err := m.FileName(tc.lsn)
		if err != nil || name != tc.name || offset != tc.offset {
			t.Fatal(tc, name, offset, err)
		}
		n, timeline, err := m.Split(name)
		if err != nil || timeline != int64(tc.timeline) {
			t.Fatal(tc, n, timeline, err)
		}
		v, err := sqltype.NumericUint64Value(numeric, n)
		if err != nil {
			t.Fatal(err)
		}
		text, err := numeric.Output(context.Background(), v)
		if err != nil || text != tc.segment {
			t.Fatal(tc, text, err)
		}
	}
	for _, tc := range []struct {
		size     uint32
		name     string
		segment  uint64
		timeline int64
	}{
		{16777216, "000000000000000000000000", 0, 0},
		{16777216, "000000010000000100000000", 256, 1},
		{16777216, "ffffffFF00000001000000af", 431, 4294967295},
		{16777216, "000000010000000000000100", 256, 1},
		{16777216, "FFFFFFFFFFFFFFFFFFFFFFFF", 1103806594815, 4294967295},
		{1048576, "FFFFFFFFFFFFFFFFFFFFFFFF", 17596481007615, 4294967295},
		{1073741824, "FFFFFFFFFFFFFFFFFFFFFFFF", 21474836475, 4294967295},
	} {
		m := functions.WALNameMetadata{SegmentSize: tc.size}
		n, ti, err := m.Split(tc.name)
		if err != nil || n != tc.segment || ti != tc.timeline {
			t.Fatal(tc, n, ti, err)
		}
	}
	for _, size := range []uint32{0, 1, 1048575, 1048577, 1 << 31} {
		m := functions.WALNameMetadata{SegmentSize: size}
		if m.Validate() == nil {
			t.Fatal("invalid startup size", size)
		}
		for _, call := range []func() error{func() error { _, _, err := m.FileName(0); return err }, func() error { _, _, err := m.Split(strings.Repeat("0", 24)); return err }} {
			err := call()
			var sql *sqlstate.Error
			if err == nil || errors.As(err, &sql) {
				t.Fatal("internal metadata must not invent a SQL diagnostic", err)
			}
		}
	}
	for _, d := range []*sqltype.Descriptor{nil, seriesUnitType(t, sqltype.Text), seriesUnitType(t, sqltype.Int8)} {
		if _, err := sqltype.NumericUint64Value(d, 1); err == nil {
			t.Fatal("invalid numeric authority")
		}
	}
	v, err := sqltype.NumericUint64Value(numeric, ^uint64(0))
	if err != nil {
		t.Fatal(err)
	}
	s, err := numeric.Output(context.Background(), v)
	if err != nil || s != "18446744073709551615" {
		t.Fatal(s, err)
	}
}

func TestWALNamesProtocols(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for i, call := range []string{"pg_walfile_name('0/1000000')", "pg_walfile_name_offset('0/1000000')", "pg_split_walfile_name('ffffffFF00000001000000af')"} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, call)
		n := node.(functions.WALName)
		before := n
		if n.Kind != functions.WALNameKind(i) || n.Validate() != nil || n.Metadata != e.WALNames {
			t.Fatal("typed recipe", n)
		}
		volatility := expr.Immutable
		if i == 2 {
			volatility = expr.Stable
		}
		if n.IntrinsicVolatility() != volatility || n.Properties().Volatility != volatility || n.PlanningFold() != expr.FoldConstant || n.StrictNull() != expr.StrictAfter {
			t.Fatal("planning", n)
		}
		if admitted, set := n.SourceAdmission(); !admitted || set {
			t.Fatal("scalar source")
		}
		if !n.ClosedDefault(func(expr.Expr) bool { return true }) || n.ClosedDefault(func(expr.Expr) bool { return false }) {
			t.Fatal("default closure")
		}
		children := n.Children()
		children[0] = nil
		if n.Arg == nil {
			t.Fatal("mutable children escaped")
		}
		info := n.Info
		info.Occurrence++
		rebuilt, err := n.Rebuild(info, n.Children())
		if err != nil {
			t.Fatal(err)
		}
		next := rebuilt.(functions.WALName)
		if next.RoutineReference != n.RoutineReference || next.Occurrence != info.Occurrence || next.Metadata != n.Metadata || next.Arg != n.Arg || !reflect.DeepEqual(n, before) {
			t.Fatal("rebuild ownership")
		}
		// Replacement supplies an authentic destination-owned descriptor in
		// Info. Reconstruction must not reinstall the source principal.
		destination, stageErr := e.RestoreStage()
		if stageErr != nil {
			t.Fatal(stageErr)
		}
		recipeSQL := call
		if i == 1 {
			recipeSQL = "(" + call + ").file_name"
		}
		if i == 2 {
			recipeSQL = "(" + call + ").segment_number"
		}
		func() {
			op, done := enginetest.RoleOperation(t, destination, "postgres")
			defer done()
			enginetest.RoleExecute(t, destination, op, "CREATE VIEW wal_names_recipe_owner AS SELECT "+recipeSQL+" AS value")
		}()
		original, err := exec.ExpressionRoutineDescriptor(n)
		if err != nil {
			t.Fatal(err)
		}
		destinationRoutine := destination.CatalogRead().MustProcedure(original.Address())
		remappedInfo := n.Info
		remappedInfo.RoutineReference = destinationRoutine
		for _, recipe := range destination.CatalogRead().State().Relations() {
			if recipe.Name() == "wal_names_recipe_owner" && recipe.View != nil {
				remappedInfo.RoutineRecipeReference = recipe
				break
			}
		}
		if remappedInfo.RoutineRecipeReference == nil {
			t.Fatal("missing authentic destination recipe owner")
		}
		mapped, err := n.Rebuild(remappedInfo, n.Children())
		if err != nil {
			t.Fatal(err)
		}
		destinationNode := mapped.(functions.WALName)
		if destinationRoutine == original || destinationRoutine.Owner == original.Owner || destinationNode.Info != remappedInfo || destinationNode.Metadata != n.Metadata || !reflect.DeepEqual(n, before) {
			t.Fatal("destination reconstruction retained source ownership")
		}
		if _, err := exec.ValidateStoredRoutineDescriptor(destination.CatalogRead(), destinationRoutine); err != nil {
			t.Fatal(err)
		}
		capture, ok, err := n.Capture(expr.CaptureFunction)
		if err != nil || !ok || capture.Count != 1 || !capture.Indexed {
			t.Fatal(capture, ok, err)
		}
		if _, ok, err := n.Capture(expr.CaptureKind(255)); err != nil || ok {
			t.Fatal("capture kind", ok, err)
		}
		operand, err := n.CaptureOperand(0)
		if err != nil || operand != n.Arg {
			t.Fatal(operand, err)
		}
		for _, index := range []int{-1, 1} {
			if _, err := n.CaptureOperand(index); err == nil {
				t.Fatal("invalid capture")
			}
		}
		render, err := n.Render()
		if err != nil || render.Name != n.Kind.Name() || len(render.Args) != 1 || render.Args[0].Expr != n.Arg {
			t.Fatal(render, err)
		}
		if !n.SameGroup(n, func(a, b expr.Expr) bool { return a == b }, false) || !n.SameSourceOccurrence(n) {
			t.Fatal("identity")
		}
		changed := n
		changed.Metadata.Timeline++
		if n.SameGroup(changed, func(a, b expr.Expr) bool { return a == b }, false) || n.SameSourceOccurrence(changed) {
			t.Fatal("metadata ignored")
		}
		for _, children := range [][]expr.Expr{nil, {nil}, {n.Arg, n.Arg}} {
			if _, err := n.Rebuild(n.Info, children); err == nil {
				t.Fatal("invalid reconstruction")
			}
		}
		for _, change := range []string{"kind", "result", "usage", "operand", "size", "fields"} {
			bad := n
			switch change {
			case "kind":
				bad.Kind = 255
			case "result":
				bad.Usage.Type = nil
			case "usage":
				bad.Usage.Typmod = 4
			case "operand":
				bad.Arg = plan.LiteralExpr{Info: expr.Info{Usage: seriesUnitType(t, sqltype.Int4).Usage()}}
			case "size":
				bad.Metadata.SegmentSize = 0
			case "fields":
				if i == 0 {
					bad.Usage.Type = seriesUnitType(t, sqltype.Int4)
				} else {
					shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "wrong", Usage: seriesUnitType(t, sqltype.Text).Usage()}})
					if err != nil {
						t.Fatal(err)
					}
					bad.Usage = shape.Usage()
				}
			}
			if bad.Validate() == nil || bad.ValidateStoredProvider() == nil {
				t.Fatal("invalid provider accepted", change)
			}
		}
		value, err := n.Eval(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		cursor, err := n.OpenSource(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		got, present, err := cursor.Next(context.Background())
		if err != nil || !present || !sqltype.SameDatum(got, value) {
			t.Fatal("scalar source", got, present, err)
		}
		_, present, err = cursor.Next(context.Background())
		if err != nil || present {
			t.Fatal("repeated scalar", err)
		}
		cursor.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		evaluation.Context = ctx
		if _, err := n.Eval(&evaluation); err != context.Canceled {
			t.Fatal("cancellation", err)
		}
	}
	for _, call := range []string{"pg_walfile_name(NULL::pg_lsn)", "pg_walfile_name_offset(NULL::pg_lsn)", "pg_split_walfile_name(NULL::text)"} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, call)
		n := node.(functions.WALName)
		value, err := n.Eval(&evaluation)
		if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
			t.Fatal("strict typed NULL", value, err)
		}
		cursor, err := n.OpenSource(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		value, present, err := cursor.Next(context.Background())
		if err != nil || !present || !value.IsNull() {
			t.Fatal("NULL one-row source", value, present, err)
		}
		cursor.Close()
	}
	// A genuine sequence operand distinguishes one evaluation from duplicate
	// evaluation, and confirms that child volatility is not erased by the family.
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_walfile_name_offset(('0/' || nextval('strict_null_steps')::text)::pg_lsn)")
	if node.Properties().Volatility != expr.Volatile {
		t.Fatal("child volatility erased")
	}
	for want := int64(1); want <= 2; want++ {
		value, err := node.Eval(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		offset, err := value.CompositeField(1).Integer()
		if err != nil || offset != want {
			t.Fatal("operand evaluated more than once", offset, want, err)
		}
	}
	failed, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_walfile_name(((1/$1::integer)::text)::pg_lsn)")
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	zero, err := sqltype.Integer(integer, 0)
	if err != nil {
		t.Fatal(err)
	}
	evaluation.Parameters = []sqltype.Value{zero}
	_, err = failed.Eval(&evaluation)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
		t.Fatal("child error replaced by WAL parser", err)
	}
}

func TestWALNamesDeclarations(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for i, call := range []string{"pg_walfile_name('0/0')", "pg_walfile_name_offset(lsn => '0/0')", "pg_split_walfile_name(file_name => '000000010000000000000000')"} {
		node, _, _ := strictNullUnitBind(t, e, op, scope, call)
		n := node.(functions.WALName)
		d, err := exec.ExpressionRoutineDescriptor(n)
		if err != nil {
			t.Fatal(err)
		}
		oid := []uint32{2851, 2850, 6213}[i]
		sig := "3220"
		result := uint32(2249)
		vol := expr.Immutable
		if i == 0 {
			result = 25
		}
		if i == 2 {
			sig = "25"
			vol = expr.Stable
		}
		if uint32(d.Address().ObjectID) != oid || string(d.Signature) != sig || uint32(d.Result) != result || !d.Strict || d.ReturnsSet() || d.Volatility != vol || d.Builtin.Parallel != 's' || d.Builtin.Defaults != 0 || d.Builtin.Variadic != 0 || d.ACL.Explicit {
			t.Fatal("release declaration", d)
		}
		if i == 0 && (d.InputNames.Len() != 0 || d.OutputDeclaration() != nil) {
			t.Fatal("unnamed scalar", d)
		}
		if i != 0 {
			if n.Usage.Type.ID() != 2249 || n.Usage.Type.CompositeLen() != 2 {
				t.Fatal("record shape")
			}
			labels := []string{"file_name", "file_offset"}
			types := []sqltype.ID{sqltype.Text, sqltype.Int4}
			if i == 2 {
				labels = []string{"segment_number", "timeline_id"}
				types = []sqltype.ID{sqltype.Numeric, sqltype.Int8}
			}
			for j := range 2 {
				m := n.Usage.Type.CompositeMember(j)
				if m.Attribute != int32(j+1) || m.Name != labels[j] || m.Usage.Type.ID() != types[j] || m.Usage != m.Usage.Type.Usage() {
					t.Fatal("OUT shape", m)
				}
			}
		}
	}
}
