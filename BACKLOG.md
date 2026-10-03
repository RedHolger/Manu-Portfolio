# BACKLOG.md — v1.1 (and v2) deferred work. Not release blockers.

Source: RELEASE_PLAN.md §9 plus findings deferred during v1 execution.
Priority is relative within v1.1; v2 items are explicitly marked.

## v1.1
- P2 Gateway timeout/concurrency hardening profiles (server-side overload
  protection; v1 comparison covers client retry only — D-011).
- P2 Full per-attempt load history (v1 JSONL preserves final outcomes only;
  disclosed in D-011 and CHANGELOG).
- P3 Statistical canary intervals for release evaluation.
- P3 Stronger YAML validation where needed (scenario/schema strictness gaps).
- P3 Richer Grafana dashboard presentation.
- P3 Reusable CI integration (build/test/lint entry points).
- P2 Independent-review fixes (BG-01–BG-10 re-review of `cad2f7e`; pending).
- P2 Unify RecoverOps template hashing (RegisterGood canonical-JSON vs
  TemplateHash struct-marshal byte forms differ; live restore verified
  semantically 2026-10-03; unify so hash equality proves identity).

## v2 (explicitly not v1)
- Multiple-controller coordination (RecoverOps).
- Cloud environments and broader failure domains (kind models app/pod
  failure only — D-004).

## Shelved ideas (no version; revisit only on demand)
- (none yet)
