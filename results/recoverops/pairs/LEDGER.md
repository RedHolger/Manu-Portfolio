# R4 paired-seed ledger (persistent; count pairs, report all outcomes)

Rule versions: RV1 = resolved-alert alone could mark RESOLVED (pre-fix);
RV2 = RESOLVED requires persisted `verified` record via POST
/v1/incidents/{id}/verify (live template == known-good + 3 passing
windows); unverified clears → SUPPRESSED. Grandfathered RV1 rows count
only with executed patch + 3 passing measured windows + genuine resolved
webhook all on disk (marked GF). Cooldown/budget refusals are valid policy
evidence, never pair arms.

Conventions: alert_at = Prometheus firing activeAt; acted_at = execution
observed; resolved_at = terminal transition. Seeds are labload seeds;
degrade loads use the arm seed, windows use seed+1..3 (+10 offset rule for
second arm of a pair).

## Pilot (exploratory + first valid pair arms)
- pilot-100/controller seed 100: incident `70e51910` alert 16:13:34 →
  acted 16:13:51 → resolved 16:16:14. Windows 101-103: 3x150 @100/100.
  RV1/GF (executed + windows + genuine resolved). Provenance: reconstructed
  from journal (port-forward dead during run); counts as pilot controller arm.
- pilot-200/baseline seed 200: incident `e102a674` alert 16:36:34 (stale
  firing — waited too little for clear; caveat recorded) → 120s scale-0
  hold → acted 16:39:59 → resolved 16:42:18. Windows 201-203: 3x150 @100/100.
  RV1/GF. Counts as pilot baseline arm (hold timing genuine).
- Refusals (policy evidence, not arms): `7172efbe` cooldown 3m59s;
  `ce847b0c` budget 3/3. Under RV2 these would be SUPPRESSED, not RESOLVED.

## Genuine-path proving cycle (not a pair arm)
- r4-am `0bc3b543`: alert 15:54:34 → acted 15:54:46 → resolved 15:57:14.
  Windows 3x150 @100/100, restart persistence confirmed. RV1/GF.

## Matrix pair-01 (controller-first, seeds 300/310) — RV1/GF
- controller `ebe18a73`: alert 17:02:19 → acted 17:03:50 → resolved 17:06:08.
  Windows 301-303: 3x150 @100/100.
- baseline `e7d6f92a`: alert 17:14:34 → 120s hold → acted 17:18:03 →
  resolved 17:20:23. Windows 311-313: 3x150 @100/100.

## Matrix pairs 02-11 (RV2; pending at ledger creation)
- pairs/pair-02..pair-11: seeds 320..500 step 20, alternating
  (02 baseline-first). Resume: `nohup /tmp/matrix-run.sh` equivalent for
  remaining pairs — see HANDOFF.md resume command. exec.log paces 3/h.
