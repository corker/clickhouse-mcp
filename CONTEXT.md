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

**Query path** / **Statement path**:
The two execution tools. `run_query` runs a single row-returning statement (`conn.Query`), bounds it, and reports truncation; `run_statement` runs a single non-row-returning statement (`conn.Exec`) and reports rows written. Which one the **consuming LLM** picks is the routing — the server does not inspect SQL to decide what is allowed.
_Avoid_: guarded query path (there is no server-side guard — see **Authorization boundary**), query executor, runner

**Authorization boundary**:
What the caller may run is decided entirely by the **connected ClickHouse user's privileges** (RBAC), enforced by ClickHouse on every statement. The server does not authorize (ADR-0006). For a read-only deployment the **operator** connects as a `GRANT SELECT`-only user; a `readonly=2` setting is *not* a boundary because an in-query `SETTINGS readonly=0` overrides it.
_Avoid_: read-only guard, write-probe, allowlist (those name the removed server-side-guard approach)

**Cap / truncation**:
Server-side row and byte limits (`max_result_rows`, `max_result_bytes`) injected on every query, with the result reporting *why* it was truncated when a cap is hit.
_Avoid_: limit, pagination (pagination is a separate `list_tables` concern)

### Auth (v0.2, HTTP transport)

**Resource server**:
The server's core v0.2 auth role: it validates the bearer token on every HTTP request — signature (upstream JWKS), issuer, expiry, and audience (the server's own canonical URI, RFC 8707) — and rejects otherwise with `401` + `WWW-Authenticate`. It authenticates the caller; it does not issue tokens. Every consumer (claude.ai, A2A, Cursor, …) reaches it this way (ADR-0007).
_Avoid_: token issuer, authorization server (the server validates tokens, it does not mint them)

**Broker layer**:
An *optional* layer added on top of the **resource server** when interactive clients or a non-compliant IdP need it: it serves protected-resource + authorization-server metadata, Dynamic Client Registration, and a token-exchange proxy so auth-code + PKCE completes. Required for Entra (no DCR, non-standard metadata); unnecessary when the IdP is MCP-spec-compliant.
_Avoid_: OAuth proxy; "the server's role" (it is a layer, not the whole model — ADR-0002's broker-as-primary was superseded by ADR-0007)

**Upstream issuer**:
The single external OIDC provider (Entra, Google, Keycloak, …) configured via `OIDC_ISSUER`. Its endpoints are resolved by OIDC Discovery, so a new provider is a config change, not code.
_Avoid_: IdP (ambiguous — could mean the broker), tenant

**Identity claim**:
The validated-token claim the server reads to identify the user (`OIDC_IDENTITY_CLAIM`, default `email`). Config, not code — absorbs per-provider claim quirks.

**Access claim**:
The group/role claim the **upstream issuer** asserts, checked to allow or deny a user (`OIDC_REQUIRED_CLAIM` contains `OIDC_REQUIRED_VALUE`). The allow/deny decision lives in the IdP, never in our source.
_Avoid_: allowlist, whitelist (those name the rejected email-in-source approach)

**Tool scope**:
An OAuth scope gating which tools a token may call — `clickhouse:read` for `run_query` + inspection tools, a future `clickhouse:write` for the write path. The scope *is* the enforcement seam for the read/write split.

### Testing

**Fast lane**:
Pure-logic unit tests (`go test -short`) — no Docker, sub-second. Covers `config.Load`, the truncation decision, the type-mapping table.
_Avoid_: unit tests (ambiguous — the integration lane also uses Go's testing package)

**Integration lane**:
`//go:build integration` tests that start real ClickHouse (and, in v0.2, Keycloak) via testcontainers. Where correctness is actually proven — the **authorization boundary** (a SELECT-only user refused writes), **caps**, driver types, and the whole auth chain.

## Relationships

- A **consuming LLM** calls **inspection tools** to discover schema, then calls `run_query` (reads) or `run_statement` (writes/DDL).
- In v0.2 the **consuming LLM** authenticates via the **OIDC broker**, which delegates to one **upstream issuer**; the server reads the **identity claim** to know *who*, checks the **access claim** to allow/deny, and checks **tool scopes** for *what*.
- Access decisions live in the **upstream issuer** (group/role membership), not in the server's source.
- The **query path** applies **caps** to every row-returning query; both paths are subject to the **authorization boundary**.
- The **authorization boundary** is the connected ClickHouse user's privileges — for read-only, the **operator** connects as a `GRANT SELECT`-only user. This is the *only* control; there is no server-side guard to bypass.
- The **authorization boundary**, driver types, and auth chain are proven only in the **integration lane** — never mocked, because a mocked boundary is false confidence on the security-critical path.

## Example dialogue

> **Dev:** "Why doesn't the server just set `readonly=2` to guarantee read-only?"
> **Domain expert:** "Because it isn't a boundary. A caller can append `SETTINGS readonly=0` to their statement and it's overridden — verified against a live server. The only thing that actually refuses a write is the connected user *lacking the privilege*."
> **Dev:** "So how does an **operator** get a read-only deployment?"
> **Domain expert:** "They point the server at a ClickHouse user granted only `SELECT`. Then ClickHouse refuses every write, regardless of what SQL we send. The server never inspects intent — that's the **authorization boundary**, and it lives in the database, not our code."

## Flagged ambiguities

- "read-only" was used to mean both *no writes* and *restricted read access* — resolved: both are the **operator**'s job via the connected ClickHouse user's grants. A `GRANT SELECT`-only user gives *no writes/DDL*; narrowing *which* data is readable is further grants on that same user. The server provides neither — it is the **authorization boundary** (ADR-0006).
- "user" was used for both the human and the AI — resolved: the human is the **operator**, the AI is the **consuming LLM**.
- "multi-provider IdP brokering" (original README) suggested brokering several IdPs at once — resolved: the **OIDC broker** fronts *one* **upstream issuer**, chosen by config; "multi-provider" means any OIDC issuer works, not several simultaneously (ADR-0002).
- "allowed users" was an email list in source — resolved: authorization is an **access claim** the **upstream issuer** asserts, not an allowlist we maintain (ADR-0003).
