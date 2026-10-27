package extension

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// REL_18_6 pgcrypto--1.3.sql and pgcrypto--1.3--1.4.sql, in member order.
var pgcryptoSources = [...]string{"pg_digest", "pg_digest", "pg_hmac", "pg_hmac", "pg_crypt", "pg_gen_salt", "pg_gen_salt_rounds", "pg_encrypt", "pg_decrypt", "pg_encrypt_iv", "pg_decrypt_iv", "pg_random_bytes", "pg_random_uuid", "pg_check_fipsmode"}

// Stage installs the pgcrypto functions into stage, owned by owner, allocating
// identities in member order.
func (p *PgcryptoProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	ext.Members = make([]ids.ObjectAddress, len(functions.PgcryptoMembers))
	for i, member := range functions.PgcryptoMembers {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		ext.Members[i] = address
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.Name, Signature: catalog.ProcedureSignature(member.Signature), RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: member.Result, Volatility: member.Volatility, Strict: member.Strict, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: pgcryptoSources[i]}, Implementation: p}
		proc.Display.Binary, proc.Display.HasBinary = "$libdir/pgcrypto", true
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return nil
}
