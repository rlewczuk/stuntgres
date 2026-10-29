package core_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestLanguageValidatorRemovalOwnerAdmission(t *testing.T) {
	source, destination := engine.New(), engine.New()
	session := enginetest.StartSession(t, source, nil)
	for _, q := range []string{
		"CREATE ROLE removal_compile_owner",
		"GRANT USAGE,CREATE ON SCHEMA public TO removal_compile_owner",
		"CREATE TYPE public.removal_compile_token AS ENUM ('a')",
		"REVOKE USAGE ON TYPE public.removal_compile_token FROM PUBLIC",
		"GRANT USAGE ON TYPE public.removal_compile_token TO removal_compile_owner",
		"CREATE TRUSTED LANGUAGE removal_compile_language HANDLER pg_catalog.plpgsql_call_handler VALIDATOR pg_catalog.plpgsql_validator",
		"CREATE FUNCTION public.removal_compile(x integer) RETURNS integer LANGUAGE removal_compile_language AS 'DECLARE token_value public.removal_compile_token; BEGIN RETURN x+1; END'",
		"ALTER FUNCTION public.removal_compile(integer) OWNER TO removal_compile_owner",
		"CREATE OR REPLACE TRUSTED LANGUAGE removal_compile_language HANDLER pg_catalog.plpgsql_call_handler",
	} {
		enginetest.SessionSQL(t, session, q)
	}
	targetSession := enginetest.StartSession(t, destination, nil)
	enginetest.SessionSQL(t, targetSession, "CREATE ROLE removal_compile_owner")
	var saved bytes.Buffer
	if err := engine.Dump(context.Background(), source, &saved); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), destination, bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	read := destination.CatalogRead()
	namespace := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	var declaration *catalog.Procedure
	for d := range catalog.DefinitionProceduresNamedAt(read, nil, namespace, "removal_compile") {
		declaration = d
	}
	if declaration == nil || declaration.SQL == nil || declaration.SQL.PL == nil || declaration.SQL.PLSyntax == nil || declaration.SQL.PL.ValidateDefinition(declaration.SQL) != nil {
		t.Fatal("owner-paired replay did not reconstruct genuine checked program")
	}
	token := read.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: "removal_compile_token"})
	found := false
	for _, dependency := range declaration.DeclarationDependencies {
		if dependency == token {
			found = true
		}
	}
	if token == (ids.ObjectAddress{}) || !found {
		t.Fatal("checked local type dependency was not rebuilt", declaration.DeclarationDependencies)
	}
	// The source checked program legitimately survives later privilege removal.
	// Restore must recompile under its actual owner, not privileged DEFAULT.
	enginetest.SessionSQL(t, session, "REVOKE USAGE ON TYPE public.removal_compile_token FROM removal_compile_owner")
	var denied bytes.Buffer
	if err := engine.Dump(context.Background(), source, &denied); err != nil {
		t.Fatal(err)
	}
	before := destination.CatalogRead()
	program, syntax := declaration.SQL.PL, declaration.SQL.PLSyntax
	var diagnostic *sqlstate.Error
	if err := engine.Restore(context.Background(), destination, bytes.NewReader(denied.Bytes())); !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
		t.Fatal("checked reconstruction did not enforce actual owner type USAGE", err)
	}
	if destination.CatalogRead().State() != before.State() || destination.CatalogRead().Get(declaration.Address()) != declaration || declaration.SQL.PL != program || declaration.SQL.PLSyntax != syntax {
		t.Fatal("failed owner compilation changed target program backing")
	}
}

func TestLanguageValidatorRemovalReplayClosure(t *testing.T) {
	e := engine.New()
	before := e.CatalogRead()
	header := "CREATE FUNCTION public.removal_not_pl() RETURNS integer LANGUAGE sql "
	comment, err := (routine.PLCheckedReplay{Header: header, Owner: "postgres"}).Comment()
	if err != nil {
		t.Fatal(err)
	}
	stream := comment + header + "AS 'SELECT 7'; ALTER FUNCTION public.removal_not_pl() OWNER TO postgres;"
	if err = engine.Restore(context.Background(), e, strings.NewReader(stream)); err == nil || !strings.Contains(err.Error(), "unconsumed checked PL replay metadata") {
		t.Fatal("non-PL declaration consumed checked compilation authority", err)
	}
	if e.CatalogRead().State() != before.State() {
		t.Fatal("unconsumed replay metadata changed target")
	}
	// Failed native metadata cannot leak a compile lease into a later restore.
	if err = engine.Restore(context.Background(), e, strings.NewReader("CREATE TRUSTED LANGUAGE removal_plain HANDLER pg_catalog.plpgsql_call_handler; CREATE FUNCTION public.removal_plain() RETURNS integer LANGUAGE removal_plain AS 'BEGIN RETURN 1;';")); err != nil {
		t.Fatal("failed metadata leaked checked admission into ordinary source-only replay", err)
	}
}

// Use actual Dump bytes and the actual parser callback offsets: a handwritten
// qualified-int4 header misses CREATE's integer versus ALTER's int4 AST spelling.
func TestLanguageValidatorRemovalGeneratedPairing(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE ROLE removal_pair_owner",
		"GRANT USAGE,CREATE ON SCHEMA public TO removal_pair_owner",
		"CREATE TRUSTED LANGUAGE removal_pair_language HANDLER pg_catalog.plpgsql_call_handler VALIDATOR pg_catalog.plpgsql_validator",
		"CREATE FUNCTION public.removal_pair(x integer, flag boolean, wide bigint, fraction real) RETURNS integer LANGUAGE removal_pair_language AS 'BEGIN RETURN x+1; END'",
		"ALTER FUNCTION public.removal_pair(integer,boolean,bigint,real) OWNER TO removal_pair_owner",
		"CREATE OR REPLACE TRUSTED LANGUAGE removal_pair_language HANDLER pg_catalog.plpgsql_call_handler",
	} {
		enginetest.SessionSQL(t, session, q)
	}
	var saved bytes.Buffer
	if err := engine.Dump(context.Background(), e, &saved); err != nil {
		t.Fatal(err)
	}
	var records []routine.PositionedPLChecked
	statements, err := parser.ParseWithOptions(saved.String(), parser.Options{
		Context: context.Background(), StandardConformingStrings: true,
		LineComment: func(comment string, position parser.Position) error {
			record, recognized, err := routine.DecodePLChecked(comment)
			if err == nil && recognized {
				records = append(records, routine.PositionedPLChecked{Record: record, End: position.Offset + len(comment)})
			}
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	pairs, err := routine.PairPLChecked(saved.String(), statements, records)
	if err != nil || len(records) != 1 || len(pairs) != 1 {
		t.Fatal("real generated keyword-alias declaration failed strict owner pairing", pairs, err)
	}
	for create, record := range pairs {
		if len(create.Arguments) != 4 || create.Arguments[0].Type.Name != "integer" || record.Owner != "removal_pair_owner" {
			t.Fatal("generated alias/owner regression did not exercise its distinguishing shape", create, record)
		}
	}
}
