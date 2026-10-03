# Kind resume suite — confusion matrix

full-window: 3 seeds x healthy/error/slow, rate 25 x 330s, kind-sre-lab

| actual \ predicted | PASS | FAIL | INCONCLUSIVE |
|---|---|---|---|
| healthy | healthy-1004.decision.json cand=1565/0/0; healthy-1005.decision.json cand=1483/0/0; healthy-1006.decision.json cand=1520/0/0; healthy-1007.decision.json cand=1446/0/0; healthy-1008.decision.json cand=1535/0/0; healthy-1009.decision.json cand=1452/0/0; healthy-1010.decision.json cand=1482/0/0 | — | — |
| error | — | error-1004.decision.json cand=1567/78/78; error-1005.decision.json cand=1478/75/75; error-1006.decision.json cand=1513/79/79; error-1007.decision.json cand=1443/74/74; error-1008.decision.json cand=1533/63/63; error-1009.decision.json cand=1453/70/79; error-1010.decision.json cand=1479/77/77 | — |
| slow | — | slow-1004.decision.json cand=1568/0/1568; slow-1005.decision.json cand=1483/0/1483; slow-1006.decision.json cand=1515/0/1515; slow-1007.decision.json cand=1445/0/1445; slow-1008.decision.json cand=1531/0/1531; slow-1009.decision.json cand=1453/0/1453; slow-1010.decision.json cand=1484/0/1484 | — |
