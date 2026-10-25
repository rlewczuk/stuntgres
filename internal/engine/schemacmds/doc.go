// Package schemacmds holds the schema-object statement executors (CREATE,
// ALTER and DROP SCHEMA, TYPE, DOMAIN, COLLATION and CONVERSION, CREATE
// LANGUAGE, routine, cast, operator, operator family and class and access
// method DDL, COMMENT, CREATE TABLESPACE, ALTER TABLE ALL IN TABLESPACE, LOAD,
// CREATE, ALTER and DROP EXTENSION, and the foreign-data DDL and GRANT), the
// extension owner import of a native Restore, and their family hosts
// RoutineHost and SchemaHost; the root composition dispatches to them.
package schemacmds
