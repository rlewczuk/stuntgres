package engine

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Reserved identities are allocated by the real catalog allocator. Replaying
// them lets a genuine output-signature descriptor predate its new scalar.
type isnReservedStage struct {
	extension.Stage
	reserved []ids.OID
}

func (s *isnReservedStage) AllocateOID() (ids.OID, error) {
	if len(s.reserved) == 0 {
		return s.Stage.AllocateOID()
	}
	oid := s.reserved[0]
	s.reserved = s.reserved[1:]
	return oid, nil
}

func TestIsnProviderStageCollisions(t *testing.T) {
	for _, family := range []string{"isbn", "isbn13", "ismn", "ismn13", "issn", "issn13", "upc"} {
	slot, inputName, outputName := 3, "isbn_in", "isn_out"
	if family == "isbn13" { slot, inputName, outputName = 6, "isbn13_in", "ean13_out" }
	if family == "ismn" { slot, inputName, outputName = 9, "ismn_in", "isn_out" }
	if family == "ismn13" { slot, inputName, outputName = 12, "ismn13_in", "ean13_out" }
	if family == "issn" { slot, inputName, outputName = 15, "issn_in", "isn_out" }
	if family == "issn13" { slot, inputName, outputName = 18, "issn13_in", "ean13_out" }
	if family == "upc" { slot, inputName, outputName = 21, "upc_in", "isn_out" }
	for _, tc := range []struct {
		name string
		output, existingType bool
	}{
		{"input", false, false},
		{"output", true, false},
		{"input_before_output_and_type", false, true},
		{"output_before_type", true, true},
	} {
		output := tc.output
		t.Run(family+"/"+tc.name, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			var existing *catalog.Type
			if tc.existingType {
				domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE "+family+" AS ENUM ('occupied')"))
				existing = domainUnitType(t, e.CatalogRead(), family)
			}
			before := e.CatalogRead()
			var seed, stage *core.CatalogStage
			defer func() {
				if stage != nil {
					e.DiscardCatalogStage(stage)
				}
				if seed != nil {
					e.DiscardCatalogStage(seed)
				}
			}()
			var original *catalog.Procedure
			var competing *catalog.Procedure
			var ext *catalog.Extension
			var base catalog.Read
			err := func() error {
				e.Mu.Lock()
				defer e.Mu.Unlock()
				auth, err := e.AuthorizationLocked(op)
				if err != nil {
					return err
				}
				host := &extensionHost{Engine: e, Op: op, Auth: auth}
				var reserved [24]ids.OID
				for i := range reserved {
					reserved[i], err = host.AllocateOID()
					if err != nil {
						return err
					}
				}
				procOID, err := host.AllocateOID()
				if err != nil {
					return err
				}
				extOID, err := host.AllocateOID()
				if err != nil {
					return err
				}
				revision, err := host.Revision()
				if err != nil {
					return err
				}
				ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
				seed = e.BeginCatalogStage(before)
				seed.AuthorizeLocked(auth, op.Session)
				// As in the existing citext collision units, a preceding real
				// catalog descriptor owns the signature. No bootstrap textin
				// executable or new pseudo-type SQL DDL is assumed by this unit.
				original = &catalog.Procedure{
					ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: procOID},
					ObjectNamespace: ns, ObjectRevision: revision,
					ObjectName: inputName, Signature: "2275",
					RoutineKind: catalog.FunctionRoutine, Result: ids.OID(sqltype.Text),
					Owner: auth.State.CurrentUser,
				}
				if output {
					original.ObjectName = outputName
					outputType := sqltype.ID(reserved[slot])
					if existing != nil {
						outputType = existing.SQLType.ID()
					}
					original.Signature = extension.IsnSignature(outputType)
					original.Result = 2275
				}
				if err := seed.Create(original); err != nil {
					return err
				}
				if existing != nil && !output {
					oid, err := host.AllocateOID()
					if err != nil {
						return err
					}
					competing = &catalog.Procedure{
						ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid},
						ObjectNamespace: ns, ObjectRevision: revision,
						ObjectName: outputName, Signature: extension.IsnSignature(existing.SQLType.ID()),
						RoutineKind: catalog.FunctionRoutine, Result: 2275, Owner: original.Owner,
					}
					if err := seed.Create(competing); err != nil {
						return err
					}
				}
				base = e.StageRead(seed)
				stage = e.BeginCatalogStage(base)
				stage.AuthorizeLocked(auth, op.Session)
				ext = &catalog.Extension{
					ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: extOID},
					ObjectName: "isn", ObjectRevision: revision, Version: "1.3",
					TargetNamespace: ns, Owner: original.Owner,
				}
				if err := stage.Create(ext); err != nil {
					return err
				}
				host.Stage = stage
				return extension.StockIsn.Stage(&isnReservedStage{Stage: host, reserved: reserved[:]}, ext, original.Owner)
			}()
			if original == nil {
				t.Fatal("collision descriptor setup failed", err)
			}
			var diagnostic *sqlstate.Error
			want := &sqlstate.Error{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723",
				Message: `function "` + original.Name() + `" already exists with same argument types`,
			}
			if family == "isbn" && !output {
				want.Context = "SQL statement \"CREATE FUNCTION isbn_in(cstring)\n\tRETURNS isbn\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 100"
			} else if family == "isbn" {
				want.Context = "SQL statement \"CREATE FUNCTION isn_out(isbn)\n\tRETURNS cstring\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 106"
			}
			if family == "ismn" && !output {
				want.Context = "SQL statement \"CREATE FUNCTION ismn_in(cstring)\n\tRETURNS ismn\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 120"
			} else if family == "ismn" {
				want.Context = "SQL statement \"CREATE FUNCTION isn_out(ismn)\n\tRETURNS cstring\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 126"
			}
			if family == "ismn13" && !output {
				want.Context = "SQL statement \"CREATE FUNCTION ismn13_in(cstring)\n\tRETURNS ismn13\n\tAS '$libdir/isn', 'ismn_in'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 58"
			} else if family == "ismn13" {
				want.Context = "SQL statement \"CREATE FUNCTION ean13_out(ismn13)\n\tRETURNS cstring\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 64"
			}
			if family == "issn" && !output {
				want.Context = "SQL statement \"CREATE FUNCTION issn_in(cstring)\n\tRETURNS issn\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 140"
			} else if family == "issn" {
				want.Context = "SQL statement \"CREATE FUNCTION isn_out(issn)\n\tRETURNS cstring\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 146"
			}
			if family == "issn13" {
				argument, result, source, line := "cstring", family, "'$libdir/isn', 'issn_in'", 78
				if output { argument, result, source, line = family, "cstring", "'$libdir/isn'", 84 }
				want.Context = fmt.Sprintf("SQL statement \"CREATE FUNCTION %s(%s)\n\tRETURNS %s\n\tAS %s\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line %d", original.Name(), argument, result, source, line)
			}
			if family == "upc" {
				argument, result, line := "cstring", family, 160
				if output { argument, result, line = family, "cstring", 166 }
				want.Context = fmt.Sprintf("SQL statement \"CREATE FUNCTION %s(%s)\n\tRETURNS %s\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line %d", original.Name(), argument, result, line)
			}
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				t.Fatal("genuine signature collision", err)
			}
			if e.CatalogRead().State() != before.State() || base.Get(original.Address()) != original || stage.Get(original.Address()) != nil {
				t.Fatal("failed staging changed original catalog or descriptor")
			}
			if competing != nil && (base.Get(competing.Address()) != competing || stage.Get(competing.Address()) != nil) {
				t.Fatal("input precedence changed the competing output descriptor")
			}
			for _, address := range ext.Members {
				if before.Get(address) != nil {
					t.Fatal("failed staging published a member", address)
				}
			}
		})
	}
	}
}

func TestIsnGeneratedArrayDisplacement(t *testing.T) {
	for _, occupied := range []string{"isbn", "_isbn", "isbn13", "_isbn13", "ismn", "_ismn", "ismn13", "_ismn13", "issn", "_issn", "issn13", "_issn13", "upc", "_upc"} {
		t.Run(occupied, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE holder AS ENUM ('a')"))
			before := e.CatalogRead()
			element := domainUnitType(t, before, "holder")
			original := before.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(element.Array)})
			seed := e.BeginCatalogStage(before)
			defer e.DiscardCatalogStage(seed)
			array := *original
			var err error
			array.SQLType, err = sqltype.Array(original.SQLType.ID(), occupied, element.SQLType)
			if err != nil {
				t.Fatal(err)
			}
			var stage *core.CatalogStage
			defer func() {
				if stage != nil {
					e.DiscardCatalogStage(stage)
				}
			}()
			err = func() error {
				e.Mu.Lock()
				defer e.Mu.Unlock()
				if err := seed.ReplaceDescriptor(&array, true, true); err != nil {
					return err
				}
				auth, err := e.AuthorizationLocked(op)
				if err != nil {
					return err
				}
				auth.Recapture(e.StageRead(seed))
				host := &extensionHost{Engine: e, Op: op, Auth: auth}
				staged, err := extension.StageCreate(host, auth, auth.View, &parser.CreateExtension{Name: parser.Identifier{Text: "isn"}})
				// BeginStage may have allocated a private stage even on failure.
				stage = host.Stage
				if err != nil {
					return err
				}
				stage, err = stageOf(staged)
				return err
			}()
			if err != nil {
				t.Fatal(err)
			}
			view := e.StageRead(stage)
			ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "isn"}))
			slot := 3
			if occupied == "isbn13" || occupied == "_isbn13" { slot = 6 }
			if occupied == "ismn" || occupied == "_ismn" { slot = 9 }
			if occupied == "ismn13" || occupied == "_ismn13" { slot = 12 }
			if occupied == "issn" || occupied == "_issn" { slot = 15 }
			if occupied == "issn13" || occupied == "_issn13" { slot  = 18 }
			if occupied == "upc" || occupied == "_upc" { slot = 21 }
			isbn := view.MustType(ext.Members[slot])
			companion := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(isbn.Array)})
			if err := extension.StockIsn.ValidateExtension(view, ext); err != nil {
				t.Fatal(err)
			}
			retained := view.MustType(array.Address())
			if companion.Address() == array.Address() || companion.SQLType.Element() != isbn.SQLType || retained.SQLType.Element() != element.SQLType || view.MustType(element.Address()).Array != array.SQLType.ID() {
				t.Fatal("displacement broke reciprocal identities")
			}
			if !strings.HasPrefix(occupied, "_") && retained.Name() == occupied || strings.HasPrefix(occupied, "_") && retained.Name() != occupied {
				t.Fatal("wrong generated-array displacement", retained.Name())
			}
			if before.MustType(original.Address()) != original || original.SQLType.Name() != "_holder" || e.CatalogRead().State() != before.State() {
				t.Fatal("private displacement mutated original catalog")
			}
		})
	}
}
