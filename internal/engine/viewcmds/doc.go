// Package viewcmds holds the view, rule, policy and trigger statement
// executors (CREATE, ALTER and DROP VIEW, REFRESH MATERIALIZED VIEW, RULE,
// POLICY and TRIGGER DDL), their family host ViewHost, and the materialized
// view snapshot import of a native Restore; the root composition dispatches
// to them and calls the import from its Restore hooks.
package viewcmds
