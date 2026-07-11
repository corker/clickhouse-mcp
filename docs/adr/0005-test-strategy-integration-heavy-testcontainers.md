# Test strategy: integration-heavy diamond, testcontainers behind a build tag

**Status:** accepted

This project's correctness lives at boundaries a unit test cannot reach, so the test suite is a
**diamond**, not the classic wide-unit pyramid: a thin pure-unit base, a thick integration
middle, and a thin end-to-end top.

- **Fast lane** — pure-logic unit tests run under `go test -short`: `config.Load` and its parsing
  helpers, the `LIMIT n+1` truncation *decision*, the ClickHouse→JSON type-*mapping* table. No
  Docker, sub-second, laptop-friendly, TDD-able. Extract enough pure logic out of the DB-touching
  code that this base is meaningful, not token.
- **Integration lane** — gated by `//go:build integration`, uses **testcontainers-go** to start
  pinned ClickHouse and Keycloak containers in-process with wait-strategy readiness. This is where
  confidence comes from: the authorization boundary (a SELECT-only user refused writes), result
  caps, real driver type scanning, OIDC discovery/JWKS/claim-gate, and PKCE enforcement.
- **E2E** — 1–2 full MCP-client → tool → real-ClickHouse round-trips, also integration-tagged.

CI runs the fast lane on every push and the integration lane as a separate job (a flake or Docker
issue there is visibly distinct from a unit failure and does not block a docs-only PR).

## Why a diamond, not a pyramid

Verified this session (see [[clickhouse-driver-verified-behavior]]): the load-bearing properties
are all integration-only. A `GRANT SELECT`-only user having writes refused (the authorization
boundary — including that an in-query `SETTINGS readonly=0` cannot override it), `LIMIT n+1`
truncation, the type→JSON contract (including the `Array(UInt8)`→base64 bug), and the whole
OIDC/PKCE chain **cannot be proven by a pure unit test** — the boundary lives in ClickHouse, the
types are the driver's runtime behavior, the auth needs a real issuer. Two design
assumptions this session were *wrong* and only a live probe caught them. A wide unit base here
would be fast tests that pass while the real behavior is broken — negative value on the
security-critical path. So we never mock the guard, the driver types, or the IdP.

## Why testcontainers-go, not the alternatives

- **GitHub Actions `services:`** — rejected: CI-only, no local parity. A contributor debugging a
  failing integration test would hand-run `docker run` and hope port/env match — the exact
  two-code-paths-drift trap. This session's bugs were caught by a *local* Docker probe; local↔CI
  parity is the property that matters most, and testcontainers gives identical code on laptop and CI.
- **Manual `docker run` (CI step + Makefile)** — rejected: reinvents readiness polling and teardown
  that testcontainers' wait strategies already solve (the fragile "poll SELECT 1 in a loop" from
  this session), across two drift-prone paths, on fixed ports that race.
- **docker-compose** — has parity but adds a separate tool + file for no gain over testcontainers at
  this topology (one CH + one Keycloak).
- **Mock / fake ClickHouse** — rejected outright: a mocked "readonly blocks writes" is false
  confidence on the exact path a real probe proved fragile.

## Consequences

- CI needs a Docker-enabled runner for the integration job (GitHub `ubuntu-latest` has Docker, so
  free there). `ci.yml` gains a separate integration job; the current fast jobs are unchanged.
- testcontainers-go is a dev-only dependency (integration-tagged), not in the shipped binary.
- Container images are pinned and readiness centralized in one shared helper — flake is the
  diamond's main risk and is mitigated deliberately, not by accident.
- New tools cost one integration test against the shared container; the harness amortizes.
