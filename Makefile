# sre-portfolio Makefile — real commands only, no echo placeholders.
# M0 note: cluster/image targets require disk ≥15GiB + Docker up + approval.
# `doctor` is diagnostic; nonzero exit = missing prerequisite (NOT a pass).

SHELL := /bin/bash
KIND_CLUSTER := sre-lab
KIND_CONTEXT := kind-$(KIND_CLUSTER)

.PHONY: doctor bootstrap test test-race lint test-rules lab-up smoke \
        test-workload test-integration test-e2e demo-budgetguard report lab-down

doctor:
	./scripts/doctor.sh

bootstrap:
	./scripts/bootstrap.sh

test:
	go test ./...

test-race:
	go test -race ./...

lint:
	gofmt -l cmd internal
	go vet ./...
	./scripts/validate-configs.sh

test-rules:
	promtool check rules monitoring/generated-rules.yaml
	promtool test rules monitoring/rule-tests.yaml

lab-up:
	./scripts/require-context.sh
	kubectl --context $(KIND_CONTEXT) -n sre-lab apply -k deploy/base
	./scripts/wait-ready.sh

smoke:
	go run ./cmd/labload smoke --seed 1

test-workload:
	go test -run 'TestUnique|TestDuplicate|TestConflicting|TestAmbiguous|TestValidation|TestPostgres' ./internal/workload/... -v

test-integration:
	go test -tags integration ./internal/... -v

test-e2e:
	go test -tags e2e ./tests/... -v

demo-budgetguard:
	./scripts/demo-budgetguard.sh

report:
	python3 scripts/report.py results/budgetguard

lab-down:
	kubectl config use-context $(KIND_CONTEXT)
	kind delete cluster --name $(KIND_CLUSTER)

.PHONY: setup-recoverops register-recoverops demo-portfolio test-scripts
setup-recoverops:
	./scripts/setup-recoverops.sh
register-recoverops:
	./scripts/register-recoverops.sh
demo-portfolio:
	python3 scripts/demo-portfolio.py --out results/portfolio/$$(date -u +%Y%m%dT%H%M%SZ)
test-scripts:
	python3 -m unittest discover -s scripts -p '*_test.py'
	./scripts/selftest.sh
