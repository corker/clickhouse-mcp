# clickhouse-mcp

An MCP server that lets an AI agent inspect and query a ClickHouse instance safely.
This context is about *how a consuming LLM discovers and reads ClickHouse data* — not
about ClickHouse internals.

## Language

**Consuming LLM**:
The AI agent on the other side of the MCP protocol that calls our tools (e.g. Claude). It — not our server — writes SQL.
_Avoid_: client, user (the human is the **operator**)

**Operator**:
The person who runs the server and configures the ClickHouse connection.
_Avoid_: user, admin

**Inspection tool**:
A tool that returns typed, structured schema/catalog information for discovery (`list_databases`, `list_tables`). Exists so the **consuming LLM** can write correct SQL without guessing.
_Avoid_: helper, wrapper

**Guarded query path**:
The single code path through which all raw SQL flows. It fast-fails non-read statements, applies the read-only guard, injects caps, and reports truncation. `run_query` is its only entry point.
_Avoid_: query executor, runner

**Read-only guard**:
The server-enforced guarantee that a query cannot write or run DDL. Enforced by ClickHouse (`readonly=2`), *verified* by the **write-probe** — never by SQL string parsing alone.
_Avoid_: readonly check, SQL filter

**Write-probe**:
A harmless write (`CREATE TEMPORARY TABLE`) attempted once at startup to *verify* the **read-only guard** actually holds against the live connection, across any ClickHouse setup. Its refusal — not the success of a `SET` — is the trusted signal.
_Avoid_: healthcheck, self-test

**Fail-closed**:
If the **write-probe** shows writes are *not* refused (and write access wasn't requested), the **guarded query path** is withheld — `run_query` is not served, though **inspection tools** still are.

**Cap / truncation**:
Server-side row and byte limits (`max_result_rows`, `max_result_bytes`) injected on every query, with the result reporting *why* it was truncated when a cap is hit.
_Avoid_: limit, pagination (pagination is a separate `list_tables` concern)

## Relationships

- A **consuming LLM** calls **inspection tools** to discover schema, then calls `run_query` through the **guarded query path**.
- The **guarded query path** applies both the **read-only guard** and **caps** to every query.
- The **read-only guard** is *asserted* by `readonly=2` and *verified* by the **write-probe**; on probe failure the path is **fail-closed**.
- An **operator** may additionally connect as a dedicated read-only ClickHouse user (documented hardening) — this narrows *read* access, which the **read-only guard** does not.

## Example dialogue

> **Dev:** "If we send `readonly=2`, why do we still need the **write-probe**?"
> **Domain expert:** "Because on some setups the `SET` is silently dropped — a proxy strips it, or the build ignores it. The `SET` succeeding proves nothing. Only a **write** actually getting *refused* proves the **read-only guard** holds."
> **Dev:** "And if the probe shows a write went through?"
> **Domain expert:** "Then we're not read-only. We **fail-closed** — withhold `run_query`, keep the **inspection tools**, and tell the **operator** which layer to fix."

## Flagged ambiguities

- "read-only" was used to mean both *no writes* and *restricted read access* — resolved: the **read-only guard** guarantees only *no writes/DDL*. Restricting which data can be **read** is the **operator**'s job via a dedicated ClickHouse user, not something the guard provides.
- "user" was used for both the human and the AI — resolved: the human is the **operator**, the AI is the **consuming LLM**.
