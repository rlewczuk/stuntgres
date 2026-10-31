package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Bind a genuine existing String provider and authorize its real released body.
// Missing/foreign manifests must not be repaired from an equal catalog OID or
// from successfully reconstructing immutable metadata in another engine.
func TestRoutineBodyOriginalLifetimeAndForeignRetag(t *testing.T) {
	e := engine.New()
	admin, finish := enginetest.RoleOperation(t, e, "postgres")
	defer finish()
	enginetest.RoleExecute(t, e, admin, "CREATE ROLE body_lifetime_reader LOGIN")
	reader, readerDone := enginetest.RoleOperation(t, e, "body_lifetime_reader")
	defer readerDone()
	input, _ := reader.Session.Settings.Presentation()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(reader)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	binder := core.NewExpressionBinder(context.Background(), e, input, reader, nil)
	binder.Query.Auth = &auth
	binder.Query.Namespace = &analyze.QueryNamespace{}
	node, err := binder.Bind(parser.FunctionExpr{Name: preparedBoundaryName("bit_length"), Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "abc"}}}}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	original := binder.Dependencies
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: original}}
	if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
		t.Fatal("genuine original body authority", err)
	}
	var missing []catalog.ExpressionDependency
	for _, dependency := range original {
		if dependency.Ref.Address != (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1374}) {
			missing = append(missing, dependency)
		}
	}
	evaluation.RoutineChecks = missing
	if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err == nil {
		t.Fatal("body acquired lifetime from raw OID after its original ref was removed")
	}
	evaluation.RoutineChecks = []catalog.ExpressionDependency{}
	if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err == nil {
		t.Fatal("empty owning recipe borrowed another manifest")
	}

	other := engine.New()
	otherAdmin, otherDone := enginetest.RoleOperation(t, other, "postgres")
	defer otherDone()
	enginetest.RoleExecute(t, other, otherAdmin, "CREATE ROLE body_lifetime_reader LOGIN")
	otherReader, otherReaderDone := enginetest.RoleOperation(t, other, "body_lifetime_reader")
	defer otherReaderDone()
	other.Mu.Lock()
	foreign, captureErr := other.AuthorizationLocked(otherReader)
	other.Mu.Unlock()
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	evaluation.RoutineAuthority = &foreign
	evaluation.RoutineChecks = original
	if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err == nil {
		t.Fatal("foreign original body accepted")
	}
	remapped, _, err := stored.RetagTypes(other.CatalogRead(), node, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		current, _ := other.CatalogRead().LookupType(old.ID())
		return current
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.AdmitRoutineCall(&evaluation, remapped, true, false); err == nil {
		t.Fatal("retag manufactured destination lifetime from descriptor addresses")
	}
	evaluation.RoutineAuthority = &auth
	evaluation.RoutineChecks = original
	if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
		t.Fatal("negative probes changed source recipe", err)
	}
}

// Every supported released SQL root is bound through its actual native
// concrete provider. Then remove each selected body lifetime independently:
// an equal released address cannot stand in for that missing original ref.
// This is an admission/provenance unit, not a replacement for public value/ACL
// oracle witnesses, nor an invocation of sleep or a fabricated body evaluator.
func TestRoutineBodyEveryProviderOriginalLifetime(t *testing.T) {
	e := engine.New()
	admin, finish := enginetest.RoleOperation(t, e, "postgres")
	defer finish()
	enginetest.RoleExecute(t, e, admin, "CREATE ROLE body_each_reader LOGIN")
	enginetest.RoleExecute(t, e, admin, "CREATE TABLE body_inputs(t text,i integer,n numeric,b bytea,bs bit(3),d date,tm time,tz timetz,ts timestamp,zt timestamptz,iv interval,id oid,nm name,n8 bigint,ip inet,flag boolean)")
	reader, readerDone := enginetest.RoleOperation(t, e, "body_each_reader")
	defer readerDone()
	ref := enginetest.RoleTableRef(t, e, "public.body_inputs")
	input, _ := reader.Session.Settings.Presentation()
	// Expected callees are independently pinned to REL_18_0 declarations and
	// SQL bodies, not obtained by walking the implementation's closure table.
	cases := []struct {
		id         ids.OID
		expression string
		calls      []ids.OID
	}{
		{879, "lpad(t,i)", []ids.OID{873}}, {880, "rpad(t,i)", []ids.OID{874}},
		{1811, "bit_length(t)", []ids.OID{1374, 141}}, {1810, "bit_length(b)", []ids.OID{720, 141}}, {1812, "bit_length(bs)", []ids.OID{1681}},
		{1708, "round(n)", []ids.OID{1707}}, {1710, "trunc(n)", []ids.OID{1709}}, {1384, "date_part('year',d)", []ids.OID{2024, 2021}}, {2059, "age(ts)", []ids.OID{2024, 2058}},
		{1741, "log(n)", []ids.OID{1736}}, {1481, "log10(n)", []ids.OID{1736}},
		{1296, "tm+d", []ids.OID{1272}}, {1298, "tz+d", []ids.OID{1297}}, {1848, "iv+tm", []ids.OID{1747}}, {2546, "iv+d", []ids.OID{2071}}, {2547, "iv+tz", []ids.OID{1749}},
		{2548, "iv+ts", []ids.OID{2032}}, {2549, "iv+zt", []ids.OID{1189}}, {2550, "i+d", []ids.OID{1141}},
		{2074, `substring(t,'#"b#"','#')`, []ids.OID{1986, 2073}},
		{1285, "quote_literal(i)", []ids.OID{1283}}, {1290, "quote_nullable(i)", []ids.OID{1289}}, {2003, "t||i", []ids.OID{1258}}, {2004, "i||t", []ids.OID{1258}},
		{1285, "quote_literal(flag)", []ids.OID{2971, 1283}}, {1285, "quote_literal(ip)", []ids.OID{730, 1283}},
		{2631, "n8+ip", []ids.OID{2630}},
		{3935, "pg_sleep_for(iv)", []ids.OID{2649, 1189, 6203, 1725, 1746, 2626}}, {3936, "pg_sleep_until(zt)", []ids.OID{2649, 6203, 1725, 1746, 2626}},
		{1305, "(zt,iv) OVERLAPS (zt,iv)", []ids.OID{1189, 1304}}, {1306, "(zt,zt) OVERLAPS (zt,iv)", []ids.OID{1189, 1304}}, {1307, "(zt,iv) OVERLAPS (zt,zt)", []ids.OID{1189, 1304}},
		{2042, "(ts,iv) OVERLAPS (ts,iv)", []ids.OID{2032, 2041}}, {2043, "(ts,ts) OVERLAPS (ts,iv)", []ids.OID{2032, 2041}}, {2044, "(ts,iv) OVERLAPS (ts,ts)", []ids.OID{2032, 2041}},
		{1309, "(tm,iv) OVERLAPS (tm,iv)", []ids.OID{1747, 1308}}, {1310, "(tm,tm) OVERLAPS (tm,iv)", []ids.OID{1747, 1308}}, {1311, "(tm,iv) OVERLAPS (tm,tm)", []ids.OID{1747, 1308}},
		{1215, "obj_description(id,nm)", []ids.OID{184, 62, 65}}, {1993, "shobj_description(id,nm)", []ids.OID{184, 62}}, {1348, "obj_description(id)", []ids.OID{184, 65}}, {1216, "col_description(id,i)", []ids.OID{184, 65}},
	}
	for _, test := range cases {
		t.Run(test.expression, func(t *testing.T) {
			raw := enginetest.Statement(t, "SELECT "+test.expression).(*parser.Select).Targets[0]
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(reader)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			binder := core.NewExpressionBinder(context.Background(), e, input, reader, nil)
			binder.Query.Auth, binder.Target.Ref = &auth, ref
			binder.Target.Relation = auth.Catalog.MustRelation(ref.Address)
			bindTestTargetNamespace(binder)
			node, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			e.Mu.Unlock()
			if err != nil {
				t.Fatal("actual provider selection", err)
			}
			outer, err := exec.ExpressionRoutineDescriptor(node)
			if err != nil || outer == nil || outer.Address().ObjectID != test.id {
				t.Fatal("selected released root", outer, err)
			}
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: binder.Dependencies}}
			if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
				t.Fatal("original body admission", err)
			}
			for _, id := range test.calls {
				address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}
				var missing []catalog.ExpressionDependency
				for _, dependency := range binder.Dependencies {
					if dependency.Ref.Address != address {
						missing = append(missing, dependency)
					}
				}
				evaluation.RoutineChecks = missing
				if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err == nil {
					t.Fatal("selected body lifetime was manufactured or omitted", id)
				}
			}
		})
	}
}
