# Ripple verification harness.
#
#   make go-check    — build + vet + unit tests (Go daemon)
#   make bench       — routing protocols comparison (claims gate, exits 1 if
#                      spray-and-wait does not save copies)
#   make integration — 3-node real-mesh delivery test (needs Go, ~30s)
#   make verify      — everything above
#
# Nothing in this project may be claimed working without a passing check
# here or in CI. See AUDIT.md — the reality ledger.

.PHONY: go-check bench integration verify audit

go-check:
	cd go-daemon && go build ./... && go vet ./... && go test -count=1 ./...

bench:
	cd go-daemon && go run ./cmd/routingbench

integration:
	bash scripts/integration_test.sh

verify: go-check bench integration
	@echo ""
	@echo "════════════════════════════════════════════════════"
	@echo "  ALL CHECKS PASSED — nothing claimed, everything proven"
	@echo "  Reality ledger: AUDIT.md"
	@echo "════════════════════════════════════════════════════"

audit:
	@echo "AUDIT.md is the reality ledger: what exists, what is proven,"
	@echo "what is honestly still NOT built. Keep it current on every change."
