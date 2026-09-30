# Baseline-versus-resilient paired comparison

source: `compare-20260930T002156Z`; profiles differ only in `--correctness-profile` (D-011).
attempts = logical ops + retries (JSONL holds one line per logical op, final attempt only).

## fault=delay seed=910
- baseline `run-delay-910-baseline`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=815 fail=0.185 p50/p95/p99=5.4/2000.2/2000.7ms attempts=1000 retries=0 amp=1.0 err={'none': 815, 'timeout': 185} recovery=0s pg_committed=845 ambiguous=30 oracle=CLEAN
- resilient `run-delay-910-resilient`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=902 fail=0.098 p50/p95/p99=5.4/4000.3/4000.7ms attempts=1208 retries=208 amp=1.208 err={'none': 902, 'timeout': 98} recovery=0s pg_committed=922 ambiguous=20 oracle=CLEAN
- delta (resilient-baseline): fail -0.0870, good +87, p99 +2000.0ms, amp +0.208, recovery 0s vs 0s, pg_committed +77

## fault=delay seed=920
- baseline `run-delay-920-baseline`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=800 fail=0.2 p50/p95/p99=5.9/2000.2/2000.8ms attempts=1000 retries=0 amp=1.0 err={'none': 800, 'timeout': 200} recovery=0s pg_committed=827 ambiguous=27 oracle=CLEAN
- resilient `run-delay-920-resilient`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=902 fail=0.098 p50/p95/p99=5.8/4000.2/4000.6ms attempts=1193 retries=193 amp=1.193 err={'none': 902, 'timeout': 98} recovery=0s pg_committed=922 ambiguous=20 oracle=CLEAN
- delta (resilient-baseline): fail -0.1020, good +102, p99 +1999.8ms, amp +0.193, recovery 0s vs 0s, pg_committed +95

## fault=delay seed=930
- baseline `run-delay-930-baseline`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=816 fail=0.184 p50/p95/p99=5.5/2000.3/2001.0ms attempts=1000 retries=0 amp=1.0 err={'none': 816, 'timeout': 184} recovery=0s pg_committed=837 ambiguous=21 oracle=CLEAN
- resilient `run-delay-930-resilient`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=901 fail=0.099 p50/p95/p99=5.8/4000.4/4001.3ms attempts=1215 retries=215 amp=1.215 err={'none': 901, 'timeout': 99} recovery=0s pg_committed=920 ambiguous=19 oracle=CLEAN
- delta (resilient-baseline): fail -0.0850, good +85, p99 +2000.3ms, amp +0.215, recovery 0s vs 0s, pg_committed +83

## fault=delay seed=940
- baseline `run-delay-940-baseline`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=806 fail=0.194 p50/p95/p99=5.8/2000.3/2000.9ms attempts=1000 retries=0 amp=1.0 err={'none': 806, 'timeout': 194} recovery=0s pg_committed=822 ambiguous=16 oracle=CLEAN
- resilient `run-delay-940-resilient`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=894 fail=0.106 p50/p95/p99=5.1/4000.3/4001.0ms attempts=1207 retries=207 amp=1.207 err={'none': 894, 'timeout': 106} recovery=0s pg_committed=915 ambiguous=21 oracle=CLEAN
- delta (resilient-baseline): fail -0.0880, good +88, p99 +2000.1ms, amp +0.207, recovery 0s vs 0s, pg_committed +93

## fault=delay seed=950
- baseline `run-delay-950-baseline`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=788 fail=0.212 p50/p95/p99=5.6/2000.4/2000.9ms attempts=1000 retries=0 amp=1.0 err={'none': 788, 'timeout': 212} recovery=0s pg_committed=819 ambiguous=31 oracle=CLEAN
- resilient `run-delay-950-resilient`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=909 fail=0.091 p50/p95/p99=5.7/4000.2/4000.7ms attempts=1208 retries=208 amp=1.208 err={'none': 909, 'timeout': 91} recovery=0s pg_committed=927 ambiguous=18 oracle=CLEAN
- delta (resilient-baseline): fail -0.1210, good +121, p99 +1999.8ms, amp +0.208, recovery 0s vs 0s, pg_committed +108

## fault=pod seed=910
- baseline `run-pod-910-baseline`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=1000 fail=0.0 p50/p95/p99=5.2/9.4/20.1ms attempts=1000 retries=0 amp=1.0 err={'none': 1000} recovery=0s pg_committed=1000 ambiguous=0 oracle=CLEAN
- resilient `run-pod-910-resilient`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=1000 fail=0.0 p50/p95/p99=5.1/7.7/10.8ms attempts=1000 retries=0 amp=1.0 err={'none': 1000} recovery=0s pg_committed=1000 ambiguous=0 oracle=CLEAN
- delta (resilient-baseline): fail +0.0000, good +0, p99 -9.3ms, amp +0.000, recovery 0s vs 0s, pg_committed +0

## fault=pod seed=920
- baseline `run-pod-920-baseline`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=1000 fail=0.0 p50/p95/p99=4.7/7.8/15.5ms attempts=1000 retries=0 amp=1.0 err={'none': 1000} recovery=0s pg_committed=1000 ambiguous=0 oracle=CLEAN
- resilient `run-pod-920-resilient`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=1000 fail=0.0 p50/p95/p99=4.8/7.6/11.8ms attempts=1000 retries=0 amp=1.0 err={'none': 1000} recovery=0s pg_committed=1000 ambiguous=0 oracle=CLEAN
- delta (resilient-baseline): fail +0.0000, good +0, p99 -3.7ms, amp +0.000, recovery 0s vs 0s, pg_committed +0

## fault=pod seed=930
- baseline `run-pod-930-baseline`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=1000 fail=0.0 p50/p95/p99=5.1/9.3/44.5ms attempts=1000 retries=0 amp=1.0 err={'none': 1000} recovery=0s pg_committed=1000 ambiguous=0 oracle=CLEAN
- resilient `run-pod-930-resilient`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=1000 fail=0.0 p50/p95/p99=4.8/7.9/12.9ms attempts=1000 retries=0 amp=1.0 err={'none': 1000} recovery=0s pg_committed=1000 ambiguous=0 oracle=CLEAN
- delta (resilient-baseline): fail +0.0000, good +0, p99 -31.6ms, amp +0.000, recovery 0s vs 0s, pg_committed +0

## fault=pod seed=940
- baseline `run-pod-940-baseline`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=1000 fail=0.0 p50/p95/p99=5.2/7.8/11.7ms attempts=1000 retries=0 amp=1.0 err={'none': 1000} recovery=0s pg_committed=1000 ambiguous=0 oracle=CLEAN
- resilient `run-pod-940-resilient`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=1000 fail=0.0 p50/p95/p99=4.8/7.8/11.5ms attempts=1000 retries=0 amp=1.0 err={'none': 1000} recovery=0s pg_committed=1000 ambiguous=0 oracle=CLEAN
- delta (resilient-baseline): fail +0.0000, good +0, p99 -0.2ms, amp +0.000, recovery 0s vs 0s, pg_committed +0

## fault=pod seed=950
- baseline `run-pod-950-baseline`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=1000 fail=0.0 p50/p95/p99=4.8/7.3/9.5ms attempts=1000 retries=0 amp=1.0 err={'none': 1000} recovery=0s pg_committed=1000 ambiguous=0 oracle=CLEAN
- resilient `run-pod-950-resilient`: offered=1000 dropped=0 achieved=10.0rps logical=1000 good=1000 fail=0.0 p50/p95/p99=4.9/7.8/11.1ms attempts=1000 retries=0 amp=1.0 err={'none': 1000} recovery=0s pg_committed=1000 ambiguous=0 oracle=CLEAN
- delta (resilient-baseline): fail +0.0000, good +0, p99 +1.6ms, amp +0.000, recovery 0s vs 0s, pg_committed +0

## Aggregates (valid pairs only)
- delay baseline (n=5): mean good=805.0 fail=0.1950 p99=2000.9ms amp=1.000
- delay resilient (n=5): mean good=901.6 fail=0.0984 p99=4000.9ms amp=1.206
- delay mean delta: fail -0.0966, good +96.6, p99 +2000.0ms, amp +0.206
- delay regressions under resilient: higher tail latency (p99 +2000.0ms)
- pod baseline (n=5): mean good=1000.0 fail=0.0000 p99=20.3ms amp=1.000
- pod resilient (n=5): mean good=1000.0 fail=0.0000 p99=11.6ms amp=1.000
- pod mean delta: fail +0.0000, good +0.0, p99 -8.6ms, amp +0.000
- pod regressions under resilient: none

