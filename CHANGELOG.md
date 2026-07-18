# Changelog

All notable changes to `REPO_NAME` are documented here. This project adheres to
[Semantic Versioning](https://semver.org) and the OpenStrata release process.

## [1.0.0-alpha] - 2026-07-19

Re-cut: codegen implementation merged to main.

### Changed
- Merged `feat/codegen-260718` implementing full DDD 4-layer architecture, HTTP
  servers, and tests into `main`.
- Integration test harness (`openstrata-e2e-test/smoke`) validates all 7 services:
  PASS=27, FAIL=0, GAP=0.

### Notes
- This re-cut supersedes the 2026-07-18 alpha; the previous tag was a docs-only scaffold.
- The `feat/codegen-260718` codegen branch workflow is retired; all future work lands on `main`.

[Semantic Versioning](https://semver.org) and the OpenStrata release process.

## [1.0.0-alpha] - 2026-07-18

Initial alpha baseline (codegen wave).

### Added
- Domain-driven (DDD 4-layer) service scaffold generated from `openstrata-meta`
  `docs/DESIGN.md` / `SPECS.md` (arch-design-260717).
- Offline-verifiable build and test for the repository (stdlib or pinned deps).
- Repository CI workflow (`.github/workflows/ci.yml`): build / test / scan / publish.

### Notes
- Alpha scaffold: structure and SPI ports are in place; business logic is stubbed
  and is implemented on the `release/v1.0.0-alpha` stabilization branch.
- The `docs/arch-design-260717` codegen workflow is retired; subsequent work lands
  on `main` and is stabilized on `release/*` branches.
