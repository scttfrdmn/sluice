# sluice — E0 pipeline. See PROJECT.md and CC_PROMPT.md.

GO      ?= go
BIN     := bin
CMDS    := harvest tag matrix label report probe
CONFIG  ?= config/sluice.yaml
RUN     ?=
RUNS    ?= runs

E0_CONFIG := testdata/sluice.test.yaml
E0_RUN    := e0-testdata
E0_RAW    := testdata/raw

.PHONY: all build test vet e0 pull-sessions probes clean

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
	$(BIN)/tag    -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN)
	$(BIN)/matrix -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN)
	$(BIN)/label  -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN)
	$(BIN)/report -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN) -split all
	@echo "--- second pass (must be a no-op) ---"
	$(BIN)/tag    -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN)
	$(BIN)/matrix -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN) | tee $(RUNS)/$(E0_RUN)/second-pass.txt
	@grep -q " 0 pending" $(RUNS)/$(E0_RUN)/second-pass.txt || (echo "e0: matrix resume was not a no-op" && exit 1)
	$(BIN)/label  -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN) | tee -a $(RUNS)/$(E0_RUN)/second-pass.txt
	@grep -q " 0 new labels" $(RUNS)/$(E0_RUN)/second-pass.txt || (echo "e0: label resume was not a no-op" && exit 1)
	$(BIN)/report -config $(E0_CONFIG) -runs $(RUNS) -run $(E0_RUN) -split all
	@test -s $(RUNS)/$(E0_RUN)/report.all.md && test -s $(RUNS)/$(E0_RUN)/accuracy_cost.all.png
	@echo "e0: OK ($(RUNS)/$(E0_RUN)/report.all.md)"

# Copy Claude Code session logs from every configured host.
pull-sessions: build
	$(BIN)/harvest pull-sessions -config $(CONFIG) -dest corpus/raw/claude-code

# Schema probes on the real raw inputs (field names only; no message text).
probes: build
	$(BIN)/probe claude-code corpus/raw/claude-code
	@test -f corpus/raw/claude-export/conversations.json && $(BIN)/probe claude-export corpus/raw/claude-export/conversations.json || echo "probe: no claude.ai export at corpus/raw/claude-export/conversations.json"
	@test -f corpus/raw/chatgpt-export/conversations.json && $(BIN)/probe chatgpt-export corpus/raw/chatgpt-export/conversations.json || echo "probe: no ChatGPT export at corpus/raw/chatgpt-export/conversations.json"

clean:
	rm -rf $(BIN)
