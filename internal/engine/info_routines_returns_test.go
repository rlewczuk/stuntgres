package engine

import (
	"strconv"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestInfoRoutinesNominalReturns(t *testing.T) {
	f := newDefaultUnitFixture(t)
	definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "routines_nominal_handler"}, Wrapper: &fdwUnitWrapper{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.e.InstallForeignDefinitions([]fdw.WrapperDefinition{definition}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoRoutinesOID)
	in := routinesUnitInput(t, f, f.e.Catalog.State, f.owner)
	rows, err := produce(in)
	if err != nil || rows == nil {
		t.Fatal("valid nominal capture rejected", rows, err)
	}
	// These are genuine captured declarations, not a builtin with a forged
	// Result or a projection-domain stand-in. No return codec is invoked.
	for _, tc := range []struct {
		result ids.OID
		name   string
	}{
		{2283, "anyelement"},
		{2280, "language_handler"},
		{3115, "fdw_handler"},
	} {
		var found bool
		for _, descriptor := range in.Catalog.Descriptors() {
			p, ok := descriptor.(*catalog.Procedure)
			if !ok || p.Result != tc.result {
				continue
			}
			identity := p.Name() + "_" + strconv.FormatUint(uint64(p.Address().ObjectID), 10)
			for _, row := range rows {
				if columnsUnitCell(t, row[2]) != identity {
					continue
				}
				if len(row) != 82 || columnsUnitCell(t, row[13]) != tc.name || columnsUnitCell(t, row[29]) != "pg_catalog" || columnsUnitCell(t, row[30]) != tc.name || columnsUnitCell(t, row[35]) != "0" {
					t.Fatal("nominal return projection", identity, row)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("missing genuine nominal return", tc)
		}
	}
}

func TestInfoRoutinesDeclaredReturnGenerations(t *testing.T) {
	for _, kind := range []string{"foreign-array-element", "stale-array-element", "foreign-domain-base", "stale-domain-base"} {
		t.Run(kind, func(t *testing.T) {
			f := newDefaultUnitFixture(t)
			op, done := roleOperation(t, f.e, "postgres")
			defer done()
			// Real staged domains and a real staged routine establish all name,
			// provider and reciprocal array associations before corruption.
			domainUnitPublish(t, f.e, domainUnitStage(t, f.e, op, "CREATE DOMAIN public.ir_result_base AS integer"))
			domainUnitPublish(t, f.e, domainUnitStage(t, f.e, op, "CREATE DOMAIN public.ir_result_domain AS public.ir_result_base"))
			resultName, dataType := "ir_result_domain", "USER-DEFINED"
			resultSQL := "public.ir_result_domain"
			if kind == "foreign-array-element" || kind == "stale-array-element" {
				resultName, dataType = "_ir_result_base", "ARRAY"
				resultSQL = "public.ir_result_base[]"
			}
			auth, err := OperationAuthorization(op)
			if err != nil {
				t.Fatal(err)
			}
			declaration := statement(t, "CREATE FUNCTION public.ir_result() RETURNS "+resultSQL+" LANGUAGE SQL AS 'SELECT NULL::"+resultSQL+"'").(*parser.CreateFunction)
			f.e.Mu.Lock()
			stage := f.e.BeginCatalogStage(auth.Catalog)
			err = routine.StageCreateFunction(RoutineHost{DDLHost{E: f.e, Op: op, CatalogStage: stage}}, auth, declaration, sqltype.InputEnvironment{})
			f.e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			domainUnitPublish(t, f.e, stage)
			valid := f.e.Catalog.State
			view := f.e.Catalog.Read(valid)
			base := domainUnitType(t, view, "ir_result_base")
			result := domainUnitType(t, view, resultName)
			produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoRoutinesOID)
			control := func() {
				t.Helper()
				rows, err := produce(routinesUnitInput(t, f, valid, f.owner))
				if err != nil || rows == nil {
					t.Fatal("valid declared-return control rejected", rows, err)
				}
				var found bool
				for _, row := range rows {
					if columnsUnitCell(t, row[5]) == "ir_result" {
						if columnsUnitCell(t, row[13]) != dataType || columnsUnitCell(t, row[29]) != "public" || columnsUnitCell(t, row[30]) != resultName {
							t.Fatal("declared return control projection", row)
						}
						found = true
					}
				}
				if !found {
					t.Fatal("valid control omitted real routine")
				}
			}
			control()
			broken := catalog.CloneState(valid)
			foreignScalar, _ := New().CatalogRead().LookupType(sqltype.Int4)
			foreignBase, err := sqltype.Domain(base.SQLType.ID(), base.Name(), base.SQLType.SQLName(), foreignScalar.Usage())
			if err != nil {
				t.Fatal(err)
			}
			if foreignBase.ID() != base.SQLType.ID() || foreignBase == base.SQLType {
				t.Fatal("negative fixture did not retain numeric identity")
			}
			switch kind {
			case "foreign-array-element":
				forged := *result
				forged.SQLType, err = sqltype.Array(result.SQLType.ID(), result.Name(), foreignBase)
				broken.SetDescriptor(forged.Address(), &forged)
			case "foreign-domain-base":
				forged := *result
				forged.SQLType, err = sqltype.Domain(result.SQLType.ID(), result.Name(), result.SQLType.SQLName(), foreignBase.Usage())
				broken.SetDescriptor(forged.Address(), &forged)
			case "stale-array-element", "stale-domain-base":
				// Publish a successor with the same OID in the candidate capture;
				// the retained result still references the previous generation.
				successor := *base
				successor.SQLType, err = sqltype.Domain(base.SQLType.ID(), base.Name(), base.SQLType.SQLName(), base.SQLType.DomainBase())
				successor.ObjectRevision++
				broken.SetDescriptor(successor.Address(), &successor)
			}
			if err != nil {
				t.Fatal(err)
			}
			in := routinesUnitInput(t, f, broken, f.owner)
			if in.Catalog.MustType(result.Address()).SQLType.ID() != result.SQLType.ID() || in.Catalog.MustType(base.Address()).SQLType.ID() != base.SQLType.ID() {
				t.Fatal("negative case changed declared numeric identity")
			}
			if rows, err := produce(in); err == nil || rows != nil {
				t.Fatal("foreign/stale declared return exposed rows or prefix", rows, err)
			}
			control()
		})
	}
}
