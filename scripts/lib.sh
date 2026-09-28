#!/bin/bash
# lib.sh — shared suite guards. Source from suite scripts, never execute.
# halted <run-dir>: true when the watchdog planted STOP. Callers must check
# between EVERY class block: a STOP breaks one loop, and without these
# guards execution would proceed to the next class (H3).
halted() { [ -f "$1/STOP" ]; }
