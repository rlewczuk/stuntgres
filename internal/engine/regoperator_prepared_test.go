package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// These fixtures publish legitimate owned shells through CatalogStage. They
// exercise alias retention independently of executable operator definitions.
func regoperatorPreparedInstall(t *testing.T, e *Engine, namespace ids.ObjectAddress, oid, left, right ids.OID) {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	d := &catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: oid}, ObjectNamespace: namespace, ObjectRevision: 1, ObjectName: "#=#", Left: left, Right: right, Owner: e.roles.domain.Principal(10)}
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
}

func regoperatorPreparedValues(t *testing.T, r Result, ids []sqltype.ID, texts []string) []sqltype.Value {
	t.Helper()
	if r.Rows == nil || len(r.Rows.Schema()) != len(ids) {
		t.Fatal("missing or wrong-width result", r)
	}
	values := make([]sqltype.Value, len(ids))
	present, err := r.Rows.Next(context.Background(), values)
	if err != nil || !present {
		t.Fatal("missing row", present, err)
	}
	for i, value := range values {
		text, err := value.Type().OutputWithEnvironment(context.Background(), value, r.Rows.OutputEnvironment())
		if err != nil || text != texts[i] || value.Type().ID() != ids[i] || r.Rows.Schema()[i].Usage.Type.ID() != ids[i] {
			t.Fatalf("cell %d: type=%v text=%q error=%v want type=%v text=%q", i, value.Type().ID(), text, err, ids[i], texts[i])
		}
	}
	return values
}

func regoperatorPreparedSnapshot(constants []analyze.RegtypeConstant) []analyze.RegtypeConstant {
	saved := slices.Clone(constants)
	for i := range saved {
		saved[i].Qualified = slices.Clone(saved[i].Qualified)
		saved[i].Operands = slices.Clone(saved[i].Operands)
	}
	return saved
}

func regoperatorPreparedCellValid(t *testing.T, e *Engine, s *Session, cell *regtypeAnalysis) bool {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := e.regtypeAnalysisValidLocked(auth, cell)
	if err != nil {
		t.Fatal(err)
	}
	return valid
}

func TestRegOperatorPreparedMixedIdentity(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	ast, d := sequencePreparedDescription(t, e, s, "SELECT '551'::regtype AS t,'551'::regoper AS r,'551'::regoperator AS s,'597'::regoper AS other")
	if d.regtypes == nil || len(d.regtypes.constants) != 4 {
		t.Fatal("mixed constants did not share one analysis cell", d.regtypes)
	}
	for _, constant := range d.regtypes.constants {
		if len(constant.Operands) != 0 {
			t.Fatal("numeric or name-only input invented operand dependencies", constant)
		}
	}
	cell := d.regtypes
	backing := &cell.constants[0]
	constants := regoperatorPreparedSnapshot(cell.constants)
	signature := slices.Clone(d.Results)
	ids := []sqltype.ID{sqltype.Regtype, sqltype.Regoper, sqltype.Regoperator, sqltype.Regoper}
	texts := []string{"551", "pg_catalog.+", "+(integer,integer)", "||/"}
	r, err := sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Rows.Close()
	values := regoperatorPreparedValues(t, r, ids, texts)
	// Exercise the actual binder discriminator at one source offset. Bad input
	// must not be reparsed when the exact offset/target alias was captured.
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		op.Done()
		t.Fatal(err)
	}
	shared := StatementDescription{regtypes: &regtypeAnalysis{constants: []analyze.RegtypeConstant{
		{Offset: 7, ID: 551},
		{Offset: 7, Operator: sqltype.Regoper, ID: 551},
		{Offset: 7, Operator: sqltype.Regoperator, ID: 551},
		{Offset: 8, Operator: sqltype.Regoper, ID: 597},
	}}}
	b := newExpressionBinder(op.Ctx, e, sqltype.InputEnvironment{}, op, &shared)
	b.Query.Auth = &auth
	for i, id := range ids {
		pos := parser.Position{Offset: 7, Character: 8}
		if i == 3 {
			pos.Offset = 8
		}
		typ, _ := auth.Catalog.LookupType(id)
		var value sqltype.Value
		var failure error
		if id == sqltype.Regtype {
			value, failure = b.RegtypeConstant("no_such_type", pos)
		} else {
			value, failure = b.OperatorConstant("no_such_operator", typ, pos)
		}
		if failure != nil || value.Type().ID() != id || !sqltype.SameDatum(value, values[i]) {
			e.mu.Unlock()
			op.Done()
			t.Fatal("offset/alias discriminator re-input or cross-retagged", i, value, failure)
		}
	}
	e.mu.Unlock()
	op.Done()
	for _, query := range []string{"CREATE ROLE regop_unrelated", "ALTER ROLE regop_unrelated LOGIN", "DROP ROLE regop_unrelated"} {
		sessionSQL(t, s, query)
		next, err := sequencePreparedExecute(e, s, ast, &d)
		if err != nil {
			t.Fatal(err)
		}
		regoperatorPreparedValues(t, next, ids, texts)
		next.Rows.Close()
		if d.regtypes != cell || &d.regtypes.constants[0] != backing || !reflect.DeepEqual(cell.constants, constants) || !reflect.DeepEqual(d.Results, signature) {
			t.Fatal("unrelated role change replaced constants or changed signatures")
		}
	}
}

func TestRegOperatorPreparedPathPublication(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE SCHEMA regop_a", "CREATE SCHEMA regop_b", "SET search_path TO regop_a,regop_b"} {
		sessionSQL(t, s, q)
	}
	view := e.CatalogRead()
	a := view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "regop_a"})
	b := view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "regop_b"})
	regoperatorPreparedInstall(t, e, a, 91000, 23, 23)
	regoperatorPreparedInstall(t, e, b, 91001, 23, 23)
	ast, d := sequencePreparedDescription(t, e, s, "SELECT '#=#'::regoper AS r,'#=#(int4,int4)'::regoperator AS s")
	oldConstants := regoperatorPreparedSnapshot(d.regtypes.constants)
	oldSignature := slices.Clone(d.Results)
	r, err := sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Rows.Close()
	typeIDs := []sqltype.ID{sqltype.Regoper, sqltype.Regoperator}
	texts := []string{"#=#", "#=#(integer,integer)"}
	// Hold the unread finalized cursor across successful reanalysis.
	sessionSQL(t, s, "SET search_path TO regop_b,regop_a")
	next, err := sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	newValues := regoperatorPreparedValues(t, next, typeIDs, texts)
	next.Rows.Close()
	for _, c := range d.regtypes.constants {
		if c.ID != 91001 {
			t.Fatal("effective operator path did not reanalyze", c)
		}
	}
	oldValues := regoperatorPreparedValues(t, r, typeIDs, texts)
	for i := range oldValues {
		oldOID, _ := oldValues[i].OperatorOID()
		newOID, _ := newValues[i].OperatorOID()
		if oldOID != 91000 || newOID != 91001 || oldConstants[i].ID != 91000 || !reflect.DeepEqual(d.Results, oldSignature) {
			t.Fatal("successful publication mutated held identities/signature", oldOID, newOID)
		}
	}
	// The first literal can resolve while the later signature fails. Neither
	// capture may be published from that unsuccessful binder attempt.
	stage := e.BeginCatalogStage(e.CatalogRead())
	old := e.CatalogRead().MustOperator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 91000})
	changed := *old
	changed.Right = 20
	changed.ObjectRevision++
	if err := stage.Replace(&changed); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	sessionSQL(t, s, "SET search_path TO regop_a")
	before := regoperatorPreparedSnapshot(d.regtypes.constants)
	analysis := d.analysis
	failed, err := sequencePreparedExecute(e, s, ast, &d)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" || failed.Rows != nil {
		t.Fatal("later literal failure did not reject entire result", err, failed.Rows)
	}
	if !reflect.DeepEqual(d.regtypes.constants, before) || d.analysis != analysis || !reflect.DeepEqual(d.Results, oldSignature) {
		t.Fatal("failed reanalysis partially published constants, plan or signature")
	}
	sessionSQL(t, s, "SET search_path TO regop_b")
	recovered, err := sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	regoperatorPreparedValues(t, recovered, typeIDs, texts)
	recovered.Rows.Close()
}

func TestRegOperatorPreparedOperandManifest(t *testing.T) {
	for _, change := range []string{"Revision", "Identity", "Authority", "Role", "TypePath"} {
		t.Run(change, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			admin := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE ROLE regop_operand_a", "CREATE ROLE regop_operand_b", "CREATE SCHEMA regop_types AUTHORIZATION regop_operand_a", "CREATE DOMAIN regop_types.secret AS integer", "SET search_path TO public"} {
				sessionSQL(t, s, q)
			}
			ns := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "regop_types"})
			address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ns, Name: "secret"})
			typ := e.CatalogRead().MustType(address)
			regoperatorPreparedInstall(t, e, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, 92000, ids.OID(typ.SQLType.ID()), 23)
			sessionSQL(t, s, "SET ROLE regop_operand_a")
			query := "SELECT '||/'::regoper AS first,'public.#=#(regop_types.secret,integer)'::regoperator AS second"
			if change == "TypePath" {
				sessionSQL(t, s, "SET search_path TO regop_types,public")
				query = "SELECT '||/'::regoper AS first,'public.#=#(secret,integer)'::regoperator AS second"
			}
			ast, d := sequencePreparedDescription(t, e, s, query)
			old := regoperatorPreparedSnapshot(d.regtypes.constants)
			signature := slices.Clone(d.Results)
			operands := old[1].Operands
			if len(operands) != 2 || operands[0].Ref.Address != typ.Address() || operands[0].Revision != typ.Revision() || operands[1].Ref.Address != (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23}) {
				t.Fatal("shared cell lacks exact ordered operand identity/revision manifest", operands)
			}
			if !regoperatorPreparedCellValid(t, e, s, d.regtypes) {
				t.Fatal("unchanged operand group failed direct shared-cell validation")
			}
			r, err := sequencePreparedExecute(e, s, ast, &d)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Rows.Close()
			// Preserve the unread result across both success and failure below.
			if change == "Revision" {
				found := false
				for _, dep := range d.analysis.dependencies {
					if dep.Ref.Address == typ.Address() && dep.Revision == typ.Revision() {
						found = true
					}
				}
				if !found {
					t.Error("resolved operand type identity/revision missing from prepared dependency manifest")
				}
				stage := e.BeginCatalogStage(e.CatalogRead())
				changed := *typ
				changed.ObjectRevision++
				if err := stage.Replace(&changed); err != nil {
					t.Fatal(err)
				}
				collationUnitPublish(t, e, stage)
			} else if change == "Identity" {
				stage := e.BeginCatalogStage(e.CatalogRead())
				changed := *typ
				changed.SQLType, err = sqltype.Domain(typ.SQLType.ID(), "old_secret", "regop_types.old_secret", typ.SQLType.DomainBase())
				if err != nil {
					t.Fatal(err)
				}
				changed.ObjectRevision++
				if err := stage.Replace(&changed); err != nil {
					t.Fatal(err)
				}
				e.mu.Lock()
				err = e.reownStagedTypeGenerationsLocked(stage)
				e.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				collationUnitPublish(t, e, stage)
				sessionSQL(t, admin, "CREATE DOMAIN regop_types.secret AS integer")
			} else if change == "Authority" {
				sessionSQL(t, admin, "ALTER SCHEMA regop_types OWNER TO regop_operand_b")
			} else if change == "Role" {
				sessionSQL(t, s, "SET ROLE regop_operand_b")
			} else {
				sessionSQL(t, s, "SET search_path TO public")
			}
			if regoperatorPreparedCellValid(t, e, s, d.regtypes) {
				t.Error("changed operand environment passed direct shared-cell validation", change)
			}
			beforeAnalysis := d.analysis
			result, failure := sequencePreparedExecute(e, s, ast, &d)
			if change == "Revision" {
				if failure != nil {
					t.Fatal(failure)
				}
				result.Rows.Close()
				if d.analysis == beforeAnalysis {
					t.Error("actual operand revision change reused stale prepared analysis")
				}
			} else {
				code := "42501"
				if change == "Identity" {
					code = "42883"
				} else if change == "TypePath" {
					code = "42704"
				}
				var diagnostic *sqlstate.Error
				if !errors.As(failure, &diagnostic) || diagnostic.Code != code || result.Rows != nil {
					if result.Rows != nil {
						result.Rows.Close()
					}
					t.Errorf("operand %s change: error=%v rows=%v want SQLSTATE %s/no rows", change, failure, result.Rows, code)
				}
				if !reflect.DeepEqual(d.regtypes.constants, old) || d.analysis != beforeAnalysis {
					t.Error("failed operand reanalysis published partial state")
				}
			}
			if !reflect.DeepEqual(d.Results, signature) {
				t.Fatal("operand invalidation mutated public result signature")
			}
			text := "#=#(regop_types.secret,integer)"
			if change == "TypePath" {
				text = "#=#(secret,integer)"
			}
			regoperatorPreparedValues(t, r, []sqltype.ID{sqltype.Regoper, sqltype.Regoperator}, []string{"||/", text})
			if change == "Authority" || change == "Role" || change == "TypePath" {
				if change == "Authority" {
					sessionSQL(t, admin, "ALTER SCHEMA regop_types OWNER TO regop_operand_a")
				} else if change == "Role" {
					sessionSQL(t, s, "SET ROLE regop_operand_a")
				} else {
					sessionSQL(t, s, "SET search_path TO regop_types,public")
				}
				recovered, err := sequencePreparedExecute(e, s, ast, &d)
				if err != nil {
					t.Fatal("restored operand environment did not recover held description", err)
				}
				regoperatorPreparedValues(t, recovered, []sqltype.ID{sqltype.Regoper, sqltype.Regoperator}, []string{"||/", text})
				recovered.Rows.Close()
				if !reflect.DeepEqual(d.Results, signature) {
					t.Fatal("recovery changed public signature")
				}
			}
		})
	}
}

func TestRegOperatorPreparedOperandBoundaries(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	_, d := sequencePreparedDescription(t, e, s, "SELECT '-(NONE,int4)'::regoperator,'551'::regoperator,'||/'::regoper")
	constants := d.regtypes.constants
	if len(constants) != 3 || len(constants[0].Operands) != 1 || constants[0].Operands[0].Ref.Address != (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23}) || len(constants[1].Operands) != 0 || len(constants[2].Operands) != 0 {
		t.Fatal("NONE, numeric, or name-only input invented operand identities", constants)
	}
}

// REL_18_0 resolves the current operand before applying FUNC_MAX_ARGS. This
// exercises the real input resolver rather than simulating a type callback.
func TestRegOperatorSignatureArgumentLimit(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	input, err := e.captureOperatorInput(auth, op)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, text, code, message, hint string }{
		{"over_limit_missing_type", "+(" + strings.Repeat("integer,", 100) + "no_such_regop_type)", "42704", `type "no_such_regop_type" does not exist`, ""},
		{"over_limit_valid_type", "+(" + strings.Repeat("integer,", 100) + "integer)", "54023", "too many arguments", ""},
		{"at_limit_operator_arity", "+(" + strings.Repeat("integer,", 99) + "integer)", "54023", "too many arguments", "Provide two argument types for operator."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := input.ResolveRegoperator(context.Background(), tc.text)
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != tc.code || d.Message != tc.message || d.Hint != tc.hint {
				t.Fatal("argument limit preempted resolution or operator arity", err)
			}
		})
	}
}
