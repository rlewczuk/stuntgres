package catalog

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// BindingClaim is one name a statement's stage binds to address. Priority
// orders the diagnostics among held claims (types, relations, array types,
// then namespaces); RelationRename marks a relation rowtype renamed with its
// relation.
type BindingClaim struct {
	Key               ids.NameKey
	Address           ids.ObjectAddress
	Priority          int
	RelationRename    bool
	CollationEncoding int32 // Actual staged encoding, including encoding-independent -1.
}

// BindingCurrent rechecks claim against current after its name lock is held:
// the name must still resolve as in base, or already to the claimed address.
// A vanished binding means the catalog was replaced; another binding is the
// unique-index violation PostgreSQL reports.
func BindingCurrent(base, current Read, claim BindingClaim) error {
	address := current.NameAddress(claim.Key)
	if address != base.NameAddress(claim.Key) && address != claim.Address {
		if address == (ids.ObjectAddress{}) {
			return ids.ErrCatalogReplaced
		}
		return bindingDuplicate(claim)
	}
	return nil
}

func bindingDuplicate(claim BindingClaim) error {
	key := claim.Key
	// Renaming/moving a table diagnoses the relation uniqueness index, even
	// when its reciprocal rowtype was the first canonical conflicting claim.
	if key.ClassID == ids.TypeClassID && claim.RelationRename {
		key.ClassID = ids.RelationClassID
	}
	var table, constraint, detail string
	switch key.ClassID {
	case ids.LanguageClassID:
		table, constraint, detail = "pg_language", "pg_language_name_index", fmt.Sprintf("Key (lanname)=(%s) already exists.", key.Name)
	case ids.SubscriptionClassID:
		table, constraint, detail = "pg_subscription", "pg_subscription_subname_index", fmt.Sprintf("Key (subdbid, subname)=(%d, %s) already exists.", key.Namespace.ObjectID, key.Name)
	case ids.PublicationClassID:
		table, constraint, detail = "pg_publication", "pg_publication_pubname_index", fmt.Sprintf("Key (pubname)=(%s) already exists.", key.Name)
	case ids.NamespaceClassID:
		table, constraint, detail = "pg_namespace", "pg_namespace_nspname_index", fmt.Sprintf("Key (nspname)=(%s) already exists.", key.Name)
	case ids.TypeClassID:
		table, constraint, detail = "pg_type", "pg_type_typname_nsp_index", fmt.Sprintf("Key (typname, typnamespace)=(%s, %d) already exists.", key.Name, key.Namespace.ObjectID)
	case ids.ProcedureClassID:
		table, constraint, detail = "pg_proc", "pg_proc_proname_args_nsp_index", fmt.Sprintf("Key (proname, proargtypes, pronamespace)=(%s, %s, %d) already exists.", key.Name, strings.ReplaceAll(key.Signature, ",", " "), key.Namespace.ObjectID)
	case ids.OperatorClassID:
		left, right, _ := strings.Cut(key.Signature, ",")
		table, constraint, detail = "pg_operator", "pg_operator_oprname_l_r_n_index", fmt.Sprintf("Key (oprname, oprleft, oprright, oprnamespace)=(%s, %s, %s, %d) already exists.", key.Name, left, right, key.Namespace.ObjectID)
	case ids.CollationClassID:
		table, constraint, detail = "pg_collation", "pg_collation_name_enc_nsp_index", fmt.Sprintf("Key (collname, collencoding, collnamespace)=(%s, %d, %d) already exists.", key.Name, claim.CollationEncoding, key.Namespace.ObjectID)
	case ids.ConversionClassID:
		table, constraint, detail = "pg_conversion", "pg_conversion_name_nsp_index", fmt.Sprintf("Key (conname, connamespace)=(%s, %d) already exists.", key.Name, key.Namespace.ObjectID)
	case ids.StatisticExtClassID:
		table, constraint, detail = "pg_statistic_ext", "pg_statistic_ext_name_index", fmt.Sprintf("Key (stxname, stxnamespace)=(%s, %d) already exists.", key.Name, key.Namespace.ObjectID)
	case ids.CastClassID:
		source, target, _ := strings.Cut(key.Name, ",")
		table, constraint, detail = "pg_cast", "pg_cast_source_target_index", fmt.Sprintf("Key (castsource, casttarget)=(%s, %s) already exists.", source, target)
	case ids.AccessMethodClassID:
		table, constraint, detail = "pg_am", "pg_am_name_index", fmt.Sprintf("Key (amname)=(%s) already exists.", key.Name)
	case ids.OperatorFamilyClassID:
		table, constraint, detail = "pg_opfamily", "pg_opfamily_am_name_nsp_index", fmt.Sprintf("Key (opfmethod, opfname, opfnamespace)=(%s, %s, %d) already exists.", key.Signature, key.Name, key.Namespace.ObjectID)
	case ids.OperatorClassCatalogID:
		table, constraint, detail = "pg_opclass", "pg_opclass_am_name_nsp_index", fmt.Sprintf("Key (opcmethod, opcname, opcnamespace)=(%s, %s, %d) already exists.", key.Signature, key.Name, key.Namespace.ObjectID)
	case ids.AccessMethodOperatorClassID, ids.AccessMethodProcedureClassID:
		parts := strings.Split(key.Name, ",")
		if key.ClassID == ids.AccessMethodOperatorClassID {
			table, constraint = "pg_amop", "pg_amop_fam_strat_index"
			detail = "Key (amopfamily, amoplefttype, amoprighttype, amopstrategy)=(" + strings.Join(parts[:4], ", ") + ") already exists."
		} else {
			table, constraint = "pg_amproc", "pg_amproc_fam_proc_index"
			detail = "Key (amprocfamily, amproclefttype, amprocrighttype, amprocnum)=(" + strings.Join(parts[:4], ", ") + ") already exists."
		}
	default:
		table, constraint, detail = "pg_class", "pg_class_relname_nsp_index", fmt.Sprintf("Key (relname, relnamespace)=(%s, %d) already exists.", key.Name, key.Namespace.ObjectID)
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: fmt.Sprintf("duplicate key value violates unique constraint %q", constraint), Detail: detail, Schema: "pg_catalog", Table: table, Constraint: constraint}
}
