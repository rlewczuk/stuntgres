package reference_test

import (
	"fmt"
	"testing"
)

func pgSequenceAssertNoAccess(t *testing.T, p *referencePair, b referenceBackend, reader referenceExecutor, schema, sequence string, oid int64) {
	t.Helper()
	if b.pg {
		referenceRunStep(t, p.ctx, reader, b.recorder, referenceSelect("SELECT has_schema_privilege('"+schema+"','USAGE'),has_sequence_privilege($1::oid,'USAGE'),has_sequence_privilege($1::oid,'SELECT'),has_sequence_privilege($1::oid,'UPDATE')", []string{"has_schema_privilege", "has_sequence_privilege", "has_sequence_privilege", "has_sequence_privilege"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, false, false, false}}, oid))
		return
	}
	// Native privilege predicates are unsupported. Name resolution proves the
	// private schema denies USAGE; numeric identities independently reach the
	// sequence ACL. Denied nextval/currval jointly exclude USAGE/SELECT/UPDATE.
	denied := controlsReferenceDiagnostic("42501", "permission denied for schema "+schema)
	denied.Position = "16"
	step := referenceImplicitError("SELECT nextval('"+schema+"."+sequence+"')", denied)
	step.selectRows, step.errorStage = true, "execute"
	referenceRunStep(t, p.ctx, reader, b.recorder, step)
	denied = controlsReferenceDiagnostic("42501", "permission denied for sequence "+sequence)
	for _, q := range []string{
		fmt.Sprintf("SELECT nextval('%d'::regclass)", oid),
		fmt.Sprintf("SELECT currval('%d'::regclass)", oid),
		fmt.Sprintf("SELECT setval('%d'::regclass,42,false)", oid),
	} {
		step := referenceImplicitError(q, denied)
		step.selectRows, step.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, reader, b.recorder, step)
	}
}

func TestReferencePGSequencePrivilegesAndTemporary(t *testing.T) {
	roleReferenceCleanup(t, []string{"pgs_reader"})
	schemaReferenceCleanup(t, "pgs_private_schema")
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			owner, reader := referenceLockConnections(t, p.ctx, b.db)
			for _, q := range []string{"CREATE ROLE pgs_reader", "CREATE SCHEMA pgs_private_schema", "CREATE SEQUENCE pgs_private_schema.pgs_private_definition AS INTEGER INCREMENT 7 MINVALUE -20 MAXVALUE 200 START 15 CACHE 9 CYCLE", "CREATE TEMP SEQUENCE pgs_peer_temporary START 17 CACHE 4"} {
				pgSequenceExec(t, p, b, owner, q)
			}
			// New private schemas and sequences have no PUBLIC privileges.
			// Prove absent access with each backend's supported public surface.
			oid := pgDescriptionRelationID(t, p, b, "pgs_private_definition")
			temp := pgDescriptionRelationID(t, p, b, "pgs_peer_temporary")
			temprow := pgSequenceDefault(temp)
			temprow[2], temprow[6] = int64(17), int64(4)
			pgSequenceCheck(t, p, b, owner, temp, temprow)
			pgSequenceExec(t, p, b, reader, "SET ROLE pgs_reader")
			defer reader.ExecContext(p.ctx, "RESET ROLE")
			pgSequenceAssertNoAccess(t, p, b, reader, "pgs_private_schema", "pgs_private_definition", oid)
			pgSequenceCheck(t, p, b, reader, oid, pgSequenceCustom(oid))
			// Raw pg_sequence metadata is visible even for another session's TEMP
			// sequence; it does not open that session's physical generator.
			pgSequenceCheck(t, p, b, reader, temp, temprow)
			pgSequenceExec(t, p, b, reader, "RESET ROLE")
			pgSequenceExec(t, p, b, owner, "DROP SEQUENCE pgs_peer_temporary")
			pgSequenceCheck(t, p, b, reader, temp, nil)
		})
	}
}
