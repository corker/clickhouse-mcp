# Result bounding via tool-injected LIMIT n+1, reported not silent

**Status:** accepted

Every `run_query` result is bounded and the bound is reported; the tool never substitutes a
summary for data. Bounding uses two layers, both **runtime-verified** against ClickHouse 26.6.1 /
clickhouse-go/v2 v2.47.0:

1. **Primary — tool-injected `LIMIT displayLimit+1`.** Request one more row than we intend to show.
   If `n+1` come back, more existed → return `n`, set `truncated: true` with a hint. This is exact,
   deterministic, and driver-agnostic (verified: `LIMIT 6` with a display limit of 5 returns 6 →
   overflow detected).
2. **Backstop — `throw`-mode `max_result_rows` / `max_result_bytes`.** A hard ceiling that errors
   (code 396) rather than streaming an enormous result. This is a safety net, not the display bound.

The result reports truncation as an explicit field (`truncated`, `shown`, `hint`). When the query
had no `ORDER BY`, the returned prefix is an arbitrary subset — this is surfaced in the hint so the
model does not treat it as a defined result.

## Why not the alternatives (all tested)

- **`result_overflow_mode='break'`** — rejected as the bound: verified block-granular and
  unreliable. `max_result_rows=5` on `SELECT ... LIMIT 100` returned all 100 rows (single block);
  it only cut at block boundaries (`max_block_size=1` → 5 rows). Cannot give a clean "first N".
- **`throw` mode as the display bound** — rejected: returns an error and *zero* rows, no usable
  prefix. Kept only as the hard backstop.
- **Inject `LIMIT` and hide it** — rejected: the tool bounds and *reports*; it never silently
  changes what the model sees without saying so. `LIMIT n+1` is injected but the truncation is
  explicit.
- **Pre-reject unbounded queries** — rejected: punishes the common naturally-small unbounded case
  (`SELECT * FROM system.databases`) and needs the SQL parsing we banned for the read-only guard.
- **Summary-instead-of-rows** — rejected: substituting an aggregate for the requested rows is a
  semantic lie about what ran.

## Boundary

The tool bounds *execution volume* and reports it; it does not rewrite query *meaning* (no added
`WHERE`, no column pruning, no summary). The one SQL the tool does add — `LIMIT n+1` — exists
solely to bound and detect, and its effect is always reported. Richer pagination (cursor/continue)
is deferred to v0.2+ if a bounded prefix proves insufficient.
