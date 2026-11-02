package schemacmds_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestUnaccentDictionaryRemap(t *testing.T) {
	source, op, ext := extensionUnitInstalled(t)
	old := unaccentDictionaryExpression(t, source, "public.unaccent", "Hôtel")
	sourceView := source.CatalogRead()
	originalProcedure := old.Procedure
	procedureBefore := *originalProcedure
	originalDictionary, originalArg := old.Dictionary, old.Arg
	sourceContext, sourceScope := unaccentDictionaryContext(t, source, op)
	defer sourceScope.Finish(context.Canceled)
	sourceValue, err := old.Dictionary.Eval(sourceContext)
	if err != nil {
		t.Fatal(err)
	}
	sourceOID, err := sourceValue.RegTSOID()
	if err != nil || ids.OID(sourceOID) != ext.Members[5].ObjectID {
		t.Fatal("source dictionary identity", sourceOID, err)
	}
	if err := sourceScope.Finish(nil); err != nil {
		t.Fatal(err)
	}

	target := engine.New()
	collisionOp, done := enginetest.RoleOperation(t, target, "postgres")
	defer done()
	// Only destination TEMP objects survive replacement. Allocate genuine
	// simple dictionaries through the owning public session until exactly the
	// source dictionary OID is occupied. The frontier gap bounds the work;
	// namespace-pair allocation may consume extra OIDs, never fewer.
	frontier := target.Catalog.NextOID()
	if frontier == 0 || frontier > ids.OID(sourceOID) {
		t.Fatal("source dictionary OID precedes destination frontier", frontier, sourceOID)
	}
	var collision *catalog.TSDictionary
	for i := ids.OID(0); i <= ids.OID(sourceOID)-frontier; i++ {
		name := fmt.Sprintf("binary_collision_%d", i)
		enginetest.RoleExecute(t, target, collisionOp, "CREATE TEXT SEARCH DICTIONARY pg_temp."+name+" (TEMPLATE=pg_catalog.simple)")
		v := target.CatalogRead()
		a := v.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: collisionOp.Session.TemporaryNamespace, Name: name})
		d := v.MustTSDictionary(a)
		if a.ObjectID == ids.OID(sourceOID) {
			collision = d
			break
		}
		if a.ObjectID > ids.OID(sourceOID) {
			t.Fatal("could not establish exact same-class collision", a, sourceOID)
		}
	}
	if collision == nil {
		t.Fatal("exact dictionary collision not established")
	}
	collisionNamespace := target.CatalogRead().MustNamespace(collision.Namespace())
	if collisionNamespace.Address() != collisionOp.Session.TemporaryNamespace || collisionNamespace.Temporary.Domain() != target.Catalog.Domain || collisionNamespace.Temporary != collisionOp.Session.Token() || collision.Address() != (ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: ids.OID(sourceOID)}) {
		t.Fatal("collision lacks genuine owning TEMP identity", collision, collisionNamespace)
	}
	collisionResult, err := collision.Runtime.Lexize(context.Background(), "Hôtel")
	if err != nil || len(collisionResult.Lexemes) != 1 || collisionResult.Lexemes[0].Text != "hôtel" {
		t.Fatal("distinguishing genuine collision", collisionResult, err)
	}
	if _, err := target.ReplaceCatalog(context.Background(), source, sourceView); err != nil {
		t.Fatal(err)
	}
	targetOp := collisionOp
	view := target.CatalogRead()
	if view.Get(collision.Address()) != collision || view.Get(collisionNamespace.Address()) != collisionNamespace || targetOp.Session.TemporaryNamespace != collisionNamespace.Address() {
		t.Fatal("replacement changed retained TEMP collision identity")
	}
	retainedCollision, err := view.MustTSDictionary(collision.Address()).Runtime.Lexize(context.Background(), "Hôtel")
	if err != nil || !reflect.DeepEqual(retainedCollision, collisionResult) {
		t.Fatal("replacement changed genuine simple callback", retainedCollision, err)
	}
	imported := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): imported.Address()}
	for i, a := range ext.Members {
		mapping[a] = imported.Members[i]
	}
	if imported.Members[5].ObjectID == ids.OID(sourceOID) {
		t.Fatal("replacement did not remap occupied dictionary OID")
	}
	mapper := stored.ViewQueryRemapper{Catalog: view, Ref: func(ref ids.ObjectRef) ids.ObjectRef {
		if a, ok := mapping[ref.Address]; ok {
			ref.Address = a
		}
		return target.Catalog.Domain.Ref(view.Epoch(), ref.Address)
	}, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor {
		owned, ok := view.LookupType(d.ID())
		if !ok {
			t.Fatal("missing target descriptor", d.ID())
		}
		return owned
	}, Procedure: extension.ProcedureRemapper(extension.DescriptorRemapper(mapping), replacementProcedurePolicyFixture(source, target), nil)}
	query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "normalized", Usage: old.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{old}}
	mapped, err := mapper.Query(query)
	if err != nil {
		t.Fatal("binary provider/datum remap", err)
	}
	n := mapped.(*plan.ProjectNode).Expressions[0].(plan.InstalledTextExpr)
	if !analyze.ValidInstalledText(n) || n.Ref.Domain() != target.Catalog.Domain || n.Ref.Epoch != view.Epoch() || n.Ref.Address != imported.Members[0] || n.Procedure.Origin.Installation == originalProcedure.Origin.Installation || n.RoutineReference != n.Procedure {
		t.Fatal("destination authority", n)
	}
	ownedText, _ := view.LookupType(sqltype.Text)
	ownedDictionary, _ := view.LookupType(sqltype.Regdictionary)
	if n.Usage.Type != ownedText || n.Arg.ResultUsage().Type != ownedText || n.Dictionary.ResultUsage().Type != ownedDictionary {
		t.Fatal("destination descriptor generations")
	}
	if view.MustTSDictionary(imported.Members[5]) == collision || view.MustTSDictionary(imported.Members[5]) == sourceView.MustTSDictionary(ext.Members[5]) {
		t.Fatal("imported dictionary did not acquire destination descriptor")
	}
	targetContext, targetScope := unaccentDictionaryContext(t, target, targetOp)
	defer targetScope.Finish(context.Canceled)
	v, err := n.Dictionary.Eval(targetContext)
	if err != nil {
		t.Fatal(err)
	}
	oid, err := v.RegTSOID()
	if err != nil || ids.OID(oid) != imported.Members[5].ObjectID || ids.OID(oid) == collision.Address().ObjectID {
		t.Fatal("remapped dictionary datum", oid, err)
	}
	unaccentDictionaryValue(t, n, targetContext, "Hotel")
	collisionValue, err := sqltype.RegTSValue(ownedDictionary, uint32(collision.Address().ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	collisionExpression := n
	collisionExpression.Dictionary = plan.LiteralExpr{Info: expr.Info{Usage: ownedDictionary.Usage()}, Value: collisionValue}
	unaccentDictionaryValue(t, collisionExpression, targetContext, "hôtel")
	refs, err := stored.ExpressionDependRefs(context.Background(), view, n)
	if err != nil || !slices.Contains(refs, imported.Members[0]) || !slices.Contains(refs, imported.Members[5]) || slices.Contains(refs, collision.Address()) {
		t.Fatal("remapped dependency identities", refs, err)
	}
	if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, old); err == nil {
		t.Fatal("foreign source rendered")
	}
	rendered, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, n)
	if err != nil {
		t.Fatal("destination rendering", err)
	}
	collisionSQL, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, collisionExpression)
	if err != nil || rendered == collisionSQL || strings.Contains(rendered, collision.Name()) {
		t.Fatal("destination rendering confused retained collision", rendered, collisionSQL, err)
	}
	if err := targetScope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if source.CatalogRead().State() != sourceView.State() || old.Procedure != originalProcedure || old.Dictionary != originalDictionary || old.Arg != originalArg || !reflect.DeepEqual(*originalProcedure, procedureBefore) || source.CatalogRead().MustProcedure(old.Ref.Address) != originalProcedure {
		t.Fatal("immutable retained source changed")
	}
	retainedContext, retainedScope := unaccentDictionaryContext(t, source, op)
	defer retainedScope.Finish(context.Canceled)
	retained, err := old.Dictionary.Eval(retainedContext)
	if err != nil || retained != sourceValue {
		t.Fatal("retained source dictionary datum", retained, err)
	}
	unaccentDictionaryValue(t, old, retainedContext, "Hotel")
	if err := retainedScope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	// Destination schema/name changes preserve the checked procedure identity
	// and both operands; fresh rendering uses the current qualified SQL label.
	enginetest.RoleExecute(t, target, targetOp, "ALTER FUNCTION public.unaccent(regdictionary,text) RENAME TO selected_normalize")
	renamedContext, renamedScope := unaccentDictionaryContext(t, target, targetOp)
	defer renamedScope.Finish(context.Canceled)
	unaccentDictionaryValue(t, n, renamedContext, "Hotel")
	if rendered, err := (stored.ScalarRenderer{}).SQL(context.Background(), target.CatalogRead(), n); err != nil || !strings.Contains(rendered, "selected_normalize") {
		t.Fatal("renamed retained provider rendering", rendered, err)
	}
	if err := renamedScope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	// Retagging must not launder forged source descriptor authority into valid
	// destination pointers merely because the OIDs happen to match.
	forged := old
	forged.Usage.Type = ownedDictionary
	if _, _, err := stored.RetagTypes(sourceView, forged, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }); err == nil {
		t.Fatal("retag laundered forged descriptor capture")
	}
}
