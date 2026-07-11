# ClickHouse RBAC is the authorization boundary; the server is a thin typed conduit

**Status:** accepted (2026-07-11) — **supersedes [ADR-0001](0001-verified-read-only-via-write-probe.md)**

The MCP server does not decide what the caller may run. The **ClickHouse user's privileges**
(the connection the operator configures) are the sole authorization boundary. The server's job is
to take one SQL statement, execute it on the right driver path, and return typed results — nothing
more. Read-only is achieved by pointing the server at a `GRANT SELECT`-only user, not by any
server-side guard.

## Why we reversed ADR-0001

ADR-0001 made `readonly=2` (set via `clickhouse.Context(WithSettings{"readonly":2})`) the
security boundary, with a Go allowlist in front and a startup write-probe verifying the guard,
failing closed if a write got through. Runtime verification this session (ClickHouse 25.6 /
clickhouse-go/v2 v2.47.0, live Docker) showed the boundary does not hold:

- **`SETTINGS readonly=0` in the statement overrides a session `readonly=2`.** A bare
  `CREATE TABLE t ...` under `readonly=2` is refused (code 164), but
  `CREATE TABLE t ... SETTINGS readonly=0` **succeeds and creates the table.** Reproduced via the
  driver context, via raw `SET readonly=2; CREATE ... SETTINGS readonly=0`, and against a user whose
  *profile* pins `readonly`. A `<constraints><readonly/></constraints>` profile did not block it
  either. `readonly` — however it is set — is not a wall.
- **A privilege grant is a wall.** A user with `GRANT SELECT ON *.*` and no write privilege
  (its `readonly` setting is 0) refuses `CREATE`, `CREATE ... SETTINGS readonly=0`, `INSERT`, and
  `DROP` alike, while `SELECT` still works. Privilege checks are independent of the `readonly`
  setting, so the in-query override cannot touch them.

So the write-probe verified a property (`readonly=2` refuses a bare write) that is **not** the
boundary it claimed to be: the allowlist, explicitly documented as "never the security boundary,"
was in fact the only thing stopping a write. The guard was security theater layered over the real
control we had told operators was optional hardening.

## Considered options

- **Keep `readonly=2` + allowlist + write-probe** — rejected: verified bypassable; gives a false
  sense of a boundary the code does not actually hold, and couples the server to a guard ClickHouse
  already supersedes with RBAC.
- **Add Go-side settings policing (strip/forbid `SETTINGS readonly=`)** — rejected: an arms race.
  `readonly` is one lever among many; parsing and rewriting caller SQL to forbid settings is exactly
  the client-trust-by-inspection ADR-0001 set out to avoid, and RBAC makes it moot.
- **RBAC as the boundary; server is a thin conduit** — chosen. The operator grants the connected
  user exactly the privileges it should have; ClickHouse enforces them on every statement regardless
  of settings. The server stops guessing intent.

## What the server still does (ergonomics, not authorization)

- **Two tools, routed by caller intent, not by SQL inspection.** `run_query` runs the row-returning
  path (`conn.Query`) and returns typed rows; `run_statement` runs the effect path (`conn.Exec`) for
  writes/DDL and returns success/error. The tool the caller picks selects the driver method — there
  is no allowlist deciding for them. If the connected user lacks the privilege, ClickHouse rejects
  the statement and that error is returned verbatim.
- **Result bounding stays on `run_query`** (tool-injected `LIMIT n+1` + truncation signal, per
  [ADR-0004](0004-result-bounding-via-limit-n-plus-1.md)). This is token economy for the agent
  caller, not a permission control. It applies only on the row path, where a wrap is valid.
- **Typed serialization and `column_types`** stay unchanged.

## Consequences

- **`CLICKHOUSE_ALLOW_WRITE_ACCESS` is removed.** There is no write toggle: the connected user's
  grants decide. Read-only deployments use a SELECT-only user.
- **The write-probe, the `readonly=2` guard context, and the Go allowlist are deleted.** The
  server no longer inspects statement intent for authorization.
- **Operator responsibility is now explicit and load-bearing**, not optional hardening: point the
  server at a user with exactly the intended privileges. The README must say so plainly — a server
  pointed at a full-privilege `default` user *can write*, by design.
- **Single-statement per call remains**, because the native protocol refuses multi-statement
  requests (`conn.Query` on `SELECT 1; SELECT 2` returns code 62, "Multi-statements are not
  allowed"). This is a transport constraint surfaced as a clear message, not a policy choice.
- The read-only integration test changes from "assert a write is refused under the guard" to
  "assert a SELECT-only user is refused a write by ClickHouse" — testing the real boundary.
