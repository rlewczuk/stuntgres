package reference_test

// Independently expected REL_18_6 view display, corroborated by the root's pinned
// PostgreSQL 18.6 oracle run (including unqualified projected column names).
const pgFileSettingsViewDefinition = " SELECT sourcefile,\n    sourceline,\n    seqno,\n    name,\n    setting,\n    applied,\n    error\n   FROM pg_show_all_file_settings() a(sourcefile, sourceline, seqno, name, setting, applied, error);"
