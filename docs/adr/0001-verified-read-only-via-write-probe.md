# Verified read-only via startup write-probe, fail-closed

**Status:** accepted

`run_query` must be read-only, and must *also* inject row/byte caps. We enforce read-only
with ClickHouse's `readonly=2` server setting (not Go SQL parsing) and we *verify* it with a
one-time startup **write-probe** (`CREATE TEMPORARY TABLE`): if a harmless write is refused, the
guard holds and we serve `run_query`; if the write succeeds and write access wasn't requested, we
**fail closed** — withhold `run_query` while still serving the inspection tools — and log which
layer to fix. A Go allowlist runs in front as a fast, friendly rejection and as the point where
caps are injected, but it is never the security boundary.

## Considered options

- **Go allowlist only** — rejected: ClickHouse's grammar (CTEs, `EXPLAIN`, subqueries,
  `INTO OUTFILE`) defeats prefix-matching; the server would trust the client entirely.
- **`readonly=1`** — rejected: it forbids `SET`, so the required row/byte caps
  (`max_result_rows` / `max_result_bytes`) can no longer be injected per query. Read-only and
  caps become mutually exclusive.
- **`readonly=2`, assumed to have taken effect** — rejected: on some setups (a proxy that strips
  per-query settings, a build that ignores the setting) the `SET` *appears* to succeed but no wall
  exists. A read-only tool would silently ship a write hole.
- **Dedicated read-only ClickHouse user only** — kept, but as *documented operator hardening*, not
  as the mechanism: it requires operator setup and breaks "single binary, works against a vanilla
  connection."

## Why (the setup matrix)

The design cannot branch per ClickHouse setup, because the dangerous setups (settings silently
dropped) are indistinguishable from the safe ones by inspecting whether the `SET` command
succeeded. Across every setup — vanilla OSS full-privilege, OSS under a `readonly` profile,
ClickHouse Cloud default, Cloud constrained role, setting-stripping proxy — the *only* signal that
reliably separates safe from unsafe is **"does an actual write get refused?"** The write-probe
tests exactly that invariant once and lets every permutation fall into refused (serve) or allowed
(fail-closed). This collapses an open-ended matrix into a single verified property.

## Consequences

- The read-only guarantee lives server-side, so it cannot be proven by a Go unit test — it
  requires an **integration test against a real ClickHouse** (e.g. testcontainers) asserting that a
  write is refused.
- `readonly=2` is a *write* guard, not a read-access boundary: the connecting user can still read
  everything it is granted. Restricting readable data is the operator's job (dedicated user +
  grants), documented as hardening.
- Write mode (`CLICKHOUSE_ALLOW_WRITE_ACCESS=true`, not yet implemented) is a clean toggle: it
  simply does not set `readonly=2` and does not fail-closed on a successful probe.
