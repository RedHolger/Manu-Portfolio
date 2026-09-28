# Kind resume suite — confusion matrix

full-window: 3 seeds x healthy/error/slow, rate 25 x 330s, kind-sre-lab

| actual \ predicted | PASS | FAIL | INCONCLUSIVE |
|---|---|---|---|
| healthy | healthy-1001.decision.json cand=1612/0/0; healthy-1002.decision.json cand=1523/0/0; healthy-1003.decision.json cand=1446/0/0 | — | — |
| error | — | error-1001.decision.json cand=1614/80/81; error-1002.decision.json cand=1526/79/79; error-1003.decision.json cand=1447/72/74 | — |
| slow | — | slow-1001.decision.json cand=1612/0/1612; slow-1002.decision.json cand=1518/0/1518; slow-1003.decision.json cand=1447/0/1447 | — |
