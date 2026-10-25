// Package typecmds holds the type statement executors (ALTER DOMAIN, ALTER
// TYPE on composites and enums, CREATE TYPE AS ENUM and AS RANGE, ALTER TABLE
// OF and NOT OF), the native domain replay shared with views, and the type
// Dump and Restore checks; the root composition dispatches to them and calls
// them from Dump and its Restore hooks.
package typecmds
