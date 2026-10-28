# sluice — E0 pipeline. See PROJECT.md and CC_PROMPT.md.

GO      ?= go
BIN     := bin
CMDS    := harvest tag matrix label report probe estimate
CONFIG  ?= config/sluice.yaml
RUN     ?=
RUNS    ?= runs

E0_CONFIG := testdata/sluice.test.yaml
E0_RUN    := e0-testdata
E0_RAW    := testdata/raw
E0_CAP    := -max-spend 1000

.PHONY: all build test vet e0 pull-sessions probes open-test pilot clean

all: build

build:
	@mkdir -p $(BIN)
	$(GO) build -o $(BIN)/ $(addprefix ./cmd/,$(CMDS))

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

# End-to-end pipeline on the synthetic testdata with the fake backend.
# This is the CI test. It runs every stage twice; the second pass must find
# nothing to do (idempotence and resume).
e0: build
	rm -rf $(RUNS)/$(E0_RUN)
	$(BIN)/harvest -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN) \
		-claude-code $(E0_RAW)/claude-code \
		-claude-export $(E0_RAW)/claude-export/conversations.json \
		-chatgpt-export $(E0_RAW)/chatgpt-export/conversations.json \
		-repos $(E0_RAW)/repos -issues=false \
		-wildchat 4 -wildchat-file $(E0_RAW)/wildchat/sample.jsonl \
		-review $(RUNS)/$(E0_RUN)/review
	$(BIN)/harvest subsample -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN)
	$(BIN)/tag    -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN) $(E0_CAP)
	$(BIN)/matrix -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN) $(E0_CAP)
	$(BIN)/label  -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN) $(E0_CAP)
	$(BIN)/report -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN) -split all
	@echo "--- second pass (must be a no-op) ---"
	$(BIN)/tag    -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN) $(E0_CAP)
	$(BIN)/matrix -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN) $(E0_CAP) | tee $(RUNS)/$(E0_RUN)/second-pass.txt
	@grep -q " 0 pending" $(RUNS)/$(E0_RUN)/second-pass.txt || (echo "e0: matrix resume was not a no-op" && exit 1)
	$(BIN)/label  -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN) $(E0_CAP) | tee -a $(RUNS)/$(E0_RUN)/second-pass.txt
	@grep -q " 0 new labels" $(RUNS)/$(E0_RUN)/second-pass.txt || (echo "e0: label resume was not a no-op" && exit 1)
	$(BIN)/report -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN) -split all
	@test -s $(RUNS)/$(E0_RUN)/report.all.md && test -s $(RUNS)/$(E0_RUN)/accuracy_cost.all.png
	@echo "e0: OK ($(RUNS)/$(E0_RUN)/report.all.md)"

# Open the test split once the calibration result warrants it: tag the
# matching test subsample (subsample_test.json), run and label its matrix,
# and write the one-time test report. Each stage needs its own cap, set on
# the spend-approval issue:
#   make open-test RUN=r2 TAG_CAP=.. MATRIX_CAP=.. LABEL_CAP=..
open-test: build
	@test -n "$(RUN)" -a -n "$(TAG_CAP)" -a -n "$(MATRIX_CAP)" -a -n "$(LABEL_CAP)" || (echo "open-test: set RUN, TAG_CAP, MATRIX_CAP, LABEL_CAP" && exit 1)
	$(BIN)/tag    -config $(CONFIG) -runs $(RUNS) -run $(RUN) -subsample test -max-spend $(TAG_CAP)
	$(BIN)/matrix -config $(CONFIG) -runs $(RUNS) -run $(RUN) -split test -max-spend $(MATRIX_CAP)
	$(BIN)/label  -config $(CONFIG) -runs $(RUNS) -run $(RUN) -split test -max-spend $(LABEL_CAP)
	$(BIN)/report -config $(CONFIG) -runs $(RUNS) -run $(RUN) -split test

# Matrix+label pilot on runs/<id>/pilot.json, all on demand, then the
# pilot-measured projection. Caps are set on the spend-approval issue:
#   make pilot RUN=r2 MATRIX_CAP=.. LABEL_CAP=..
pilot: build
	@test -n "$(RUN)" -a -n "$(MATRIX_CAP)" -a -n "$(LABEL_CAP)" || (echo "pilot: set RUN, MATRIX_CAP, LABEL_CAP" && exit 1)
	test -f $(RUNS)/$(RUN)/pilot.json || $(BIN)/harvest pilot -config $(CONFIG) -runs $(RUNS) -run $(RUN)
	$(BIN)/matrix -config $(CONFIG) -runs $(RUNS) -run $(RUN) -pilot -on-demand -max-spend $(MATRIX_CAP)
	$(BIN)/label  -config $(CONFIG) -runs $(RUNS) -run $(RUN) -pilot -max-spend $(LABEL_CAP)
	$(BIN)/estimate -config $(CONFIG) -runs $(RUNS) -run $(RUN)

# Copy Claude Code session logs from every configured host.
pull-sessions: build
	$(BIN)/harvest pull-sessions -config $(CONFIG) -dest corpus/raw/claude-code

# Schema probes on the real raw inputs (field names only; no message text).
probes: build
	$(BIN)/probe claude-code corpus/raw/claude-code
	@test -f corpus/raw/claude-export/conversations.json && $(BIN)/probe claude-export corpus/raw/claude-export/conversations.json || echo "probe: no claude.ai export at corpus/raw/claude-export/conversations.json"
	@test -e corpus/raw/chatgpt-export && $(BIN)/probe chatgpt-export corpus/raw/chatgpt-export || echo "probe: no ChatGPT export under corpus/raw/chatgpt-export/"

clean:
	rm -rf $(BIN)
