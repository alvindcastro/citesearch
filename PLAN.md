# Ask Banner — Implementation Plan

> **Branch:** `feat/chatbot`
> **TDD mandate:** Every task that touches Go code follows Red → Green → Refactor.
> Run `go test ./... -v` to verify at each step (CGO not available; omit `-race`).

---

## Completed (Archived)

| Phase | Description | Status |
|-------|-------------|--------|
| 0 | Replace 6 student intents with 5 internal-user intents | ✅ Done |
| 1 | Fix escalate threshold code vs spec mismatch (0.01 → 0.5) | ✅ Done |
| 2 | Simplify routing: collapse `student`/`general` → `banner`; 400 on invalid source | ✅ Done |
| 3 | Classifier comments update | ✅ Done |
| 4 | Agent 8 (Internal Chatbot) documented in CLAUDE_AGENTS.md | ✅ Done |
| 5 | Wiki docs updated | ✅ Done |

---

## Open Issue: Escalate Threshold Miscalibration

### Observed Evidence

```
Query: "What changed in Banner?"
confidence: 0.033   sources: [3 docs]   escalate: true   ← WRONG — useful answer
```

```
Query: "What changed in Banner 9.3.37?"
confidence: 0.000   sources: []         escalate: true   ← CORRECT — nothing indexed
```

### Root Cause

CLAUDE.md specifies `confidence < 0.5 → escalate`. This was written assuming a normalized
0–1 confidence scale. Azure AI Search returns raw hybrid scores (BM25 + semantic re-ranker)
that cluster between **0.01 and 0.05** for valid, well-grounded answers. A threshold of 0.5
causes every real answer to escalate, making the chatbot useless for any indexed content.

### Objectively Correct Resolution

**Two-signal escalation:**

| Signal | Meaning | Reliability |
|--------|---------|-------------|
| `retrieval_count == 0` | Index returned no documents | Hard binary — always reliable |
| `confidence < threshold` | Index returned docs but score is suspiciously low | Soft — requires calibration |

The **only reliable escalation signal today** is `retrieval_count == 0`. Any score > 0 with
sources present means Azure AI Search found semantically relevant chunks — the answer has
documentary backing.

A soft score floor is still valuable (e.g., protect against a 0.001 tangential hit), but it
must be calibrated from real query data, not assumed. Based on the one observed data point
(0.033 = good answer), any threshold ≥ 0.01 would have been fine; 0.5 is 15× too high.

**Interim decision:** `escalate = retrieval_count == 0 || confidence < 0.01`

This preserves a safety floor against near-zero-score tangential matches while not escalating
real answers. The calibration agent (Phase B) will validate or revise this floor with real data.

---

## Phase A — Calibrate Azure AI Search Score Distribution

**Goal:** Collect real score data across a range of queries to determine the correct floor.
**Agent required:** Agent 7 (Index Health & Diagnostics) — already designed in CLAUDE_AGENTS.md.
**New agent required:** Agent 9 (Confidence Calibration) — see Phase B for design.

### Tasks

- [x] **A.1 — Run baseline diagnostic with Agent 7**

  **Prompt for implementer:**
  Use Agent 7 (CLAUDE_AGENTS.md § Agent 7) to run a diagnostic session against the live
  backend (http://localhost:8000). Execute the following test queries using the `banner_ask`
  tool and record the `sources[0].score` for each:

  **Query set:**
  ```
  # Should find results (Banner docs indexed):
  "What changed in Banner General?"
  "What is new in Banner 9.3.37.2?"
  "What are the Banner General release notes?"
  "What are the breaking changes in Banner?"
  "What support changes were made in Banner 8?"

  # Should NOT find results (not indexed or too specific):
  "What changed in Banner Finance 9.3.37?" (if Finance not indexed)
  "What are the Banner HR module changes?"
  "Banner Student 9.4.1 release notes"
  "What is the Banner General 8.26 changelog?"
  ```

  Record results in a table:
  | Query | retrieval_count | sources[0].score | Has useful answer? |
  |-------|----------------|------------------|--------------------|

  Run Agent 7 from `agents/diagnostics.py` once built, or curl the backend directly:
  ```bash
  curl -s -X POST http://localhost:8000/banner/ask \
    -H "Content-Type: application/json" \
    -d '{"question":"What changed in Banner General?","module_filter":"General","top_k":5}' | jq '{count:.retrieval_count, score:.sources[0].score, answer:.answer[:80]}'
  ```

- [x] **A.2 — Identify score distribution boundaries**

  **Prompt for implementer:**
  From the data collected in A.1, identify:
  1. **Minimum score for a genuinely useful answer** (has documentary evidence, answer makes sense)
  2. **Maximum score for a genuinely useless answer** (retrieval_count > 0 but answer is wrong/tangential)
  3. The natural gap between these two groups, if one exists

  Record the findings in `wiki/RUNBOOK.md` under a new section:
  "## Azure AI Search Score Distribution"

  Format:
  ```
  ## Azure AI Search Score Distribution
  Observed: YYYY-MM-DD  Backend version: [git sha]

  | Category | Score Range | Example |
  |----------|------------|---------|
  | Good answer | 0.XXX–0.XXX | "What changed in Banner General?" → 0.033 |
  | Tangential / useless | 0.XXX–0.XXX | [query] → [score] |
  | No results | 0 | (retrieval_count == 0) |

  Recommended floor: 0.0XX
  ```

- [x] **A.3 — Decide final threshold**

  **Prompt for implementer:**
  Based on A.2 findings, choose the escalate floor. Decision criteria:
  - **If no natural gap exists** (good and bad answers have overlapping scores): use
    `retrieval_count == 0` as the sole signal. Score threshold = 0.0 (disabled).
  - **If a clear gap exists** (e.g., good ≥ 0.02, bad ≤ 0.005): set floor midway in the gap.
  - **Minimum defensible floor:** 0.005 (protects against near-zero noise hits).
  - **Never set above 0.05** until significantly more data is collected.

  Document the chosen value and rationale in `wiki/RUNBOOK.md` (same section as A.2).
  Update `CLAUDE.md` escalate rule to match.

---

## Phase B — Fix Escalate Logic (TDD)

**Goal:** Update `internal/adapter/client.go:mapResponse` to use the calibrated threshold.
All changes follow Red → Green → Refactor.

### Tasks

- [x] **B.1 — Write failing tests for `retrieval_count == 0` hard gate**

  **Prompt for implementer:**
  Open `internal/adapter/client_test.go`. Confirm `TestAdapterClient_BannerAsk_NoResults_Escalates`
  already exists and passes (it tests `retrieval_count == 0 → escalate`). If it does, mark this
  task done. If it doesn't, add it:
  ```go
  func TestAdapterClient_NoResults_AlwaysEscalates(t *testing.T) {
      // even if score were somehow non-zero, zero retrieval_count must escalate
      raw := ragAskResponse{RetrievalCount: 0, Sources: []ragSourceChunk{}}
      resp := mapResponse(raw)
      assert.True(t, resp.Escalate)
      assert.Zero(t, resp.Confidence)
  }
  ```
  Run `go test ./internal/adapter/... -v` — must be **GREEN** (gate already exists).

- [x] **B.2 — Write failing test for calibrated floor**

  **Prompt for implementer:**
  In `internal/adapter/client_test.go`, update `TestAdapterClient_BannerAsk_LowConfidence_SetsEscalate`
  (currently uses score 0.3) to use the threshold value decided in A.3. Also add:

  ```go
  func TestAdapterClient_ScoreAboveFloor_DoesNotEscalate(t *testing.T) {
      // score at or just above the floor with results present must NOT escalate
      srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          json.NewEncoder(w).Encode(ragAskResponse{
              Answer:         "Banner General 9.3.37 changed X.",
              RetrievalCount: 3,
              Sources:        []ragSourceChunk{{Score: 0.033}}, // observed real-world score
          })
      }))
      defer srv.Close()
      client := NewAdapterClient(srv.URL)
      result, err := client.AskBanner(context.Background(), "What changed in Banner?", AskOptions{})
      require.NoError(t, err)
      assert.False(t, result.Escalate, "score 0.033 with 3 sources should not escalate")
  }
  ```

  Run `go test ./internal/adapter/... -v` — `TestAdapterClient_ScoreAboveFloor_DoesNotEscalate`
  must be **RED** while 0.5 threshold is in place. This is the failing test that drives the fix.

- [x] **B.3 — Fix threshold in adapter/client.go**

  **Prompt for implementer:**
  In `internal/adapter/client.go`, update `mapResponse`:
  ```go
  // Escalate if no documents retrieved (hard gate) or score is below the calibrated floor.
  // Azure AI Search hybrid scores for this index cluster 0.01–0.05 for valid results.
  // Threshold derived from empirical calibration — see wiki/RUNBOOK.md § Score Distribution.
  escalate := raw.RetrievalCount == 0 || confidence < [VALUE_FROM_A.3]
  ```
  Replace `[VALUE_FROM_A.3]` with the threshold decided in Phase A (interim: `0.01`).

  Run `go test ./... -v` — `TestAdapterClient_ScoreAboveFloor_DoesNotEscalate` must be
  **GREEN**. All other tests must remain **GREEN**. If any break, fix them.

- [x] **B.4 — Update CLAUDE.md escalate rule**

  **Prompt for implementer:**
  In `CLAUDE.md`, find the line:
  ```
  Confidence = sources[0].score; Escalate = true when confidence < 0.5 or retrieval_count == 0
  ```
  Replace with:
  ```
  Confidence = sources[0].score (raw Azure AI Search hybrid score; typical range 0.01–0.05 for valid results).
  Escalate = true when retrieval_count == 0 (hard gate) OR confidence < [threshold from RUNBOOK].
  NOTE: 0.5 is NOT a valid threshold for this index — Azure scores never reach that range.
  ```

---

## Phase C — Agent 9: Confidence Calibration Agent

**Goal:** Add a reusable calibration agent to `wiki/CLAUDE_AGENTS.md` so any team member can
re-run score distribution analysis as the index grows (new documents change score distributions).

### Tasks

- [x] **C.1 — Design and document Agent 9 in CLAUDE_AGENTS.md**

  **Prompt for implementer:**
  Append Agent 9 to `wiki/CLAUDE_AGENTS.md` using the design in the "Agent 9" section
  added to that file (see current state of CLAUDE_AGENTS.md § Agent 9).

  Agent 9 uses Agent 7's tools (`banner_ask`, `index_stats`) but drives a structured
  calibration protocol: runs a fixed set of known-good and known-bad test queries,
  records scores, and outputs a calibration report with a recommended threshold.

  The agent must:
  - Accept a module_filter parameter to calibrate per-module (Finance scores may differ from General)
  - Run at minimum 5 known-good and 5 boundary queries
  - Output a Markdown table: query, retrieval_count, score, has_useful_answer, verdict
  - Conclude with: recommended floor, confidence in recommendation, next review date

- [x] **C.2 — Add Agent 9 to Tool Reference table in CLAUDE_AGENTS.md**

  **Prompt for implementer:**
  In the Tool Reference table, Agent 9 reuses existing tools. Add a note row:
  ```
  | (calibration protocol) | POST | `/banner/ask` | 9 |
  | (calibration protocol) | GET  | `/index/stats` | 9 |
  ```

---

## Phase D — Documentation Sweep

### Tasks

- [x] **D.1 — Update CLAUDE.md**

  Already covered in B.4. Additionally: confirm the "Non-negotiable rules" section
  escalate rule is updated. Confirm the backend API comment block still accurately
  describes `sources[0].score`.

- [x] **D.2 — Add score distribution section to wiki/RUNBOOK.md**

  **Prompt for implementer:**
  Create or update `wiki/RUNBOOK.md`. Add section "## Azure AI Search Score Behavior"
  explaining:
  - Scores are raw hybrid (BM25 + semantic) values, NOT normalized confidence
  - Typical range for this index: 0.01–0.05 (update after Phase A data collection)
  - `retrieval_count == 0` is the reliable escalation trigger
  - The score floor is a secondary guard against near-zero noise, not a quality gate
  - Include the calibration query results table from Phase A.2

- [x] **D.3 — Update wiki/CHATBOT.md escalation section**

  **Prompt for implementer:**
  In `wiki/CHATBOT.md`, find any reference to the escalate threshold (0.5 or 0.01).
  Update the description of the `escalate` field in the `/chat/ask` response:
  ```
  escalate: true when:
    - retrieval_count == 0 (nothing indexed for this query)
    - confidence < [calibrated floor] (near-zero score even with some results)
  NOTE: confidence values for this index are typically 0.01–0.05. A value of 0.033
  with sources present is a GOOD answer, not a low-confidence one.
  ```

---

## Acceptance Criteria

- [ ] `go test ./... -v` passes with 0 failures
- [ ] `score=0.033, retrieval_count=3` → `escalate: false`
- [ ] `score=0, retrieval_count=0` → `escalate: true`
- [x] CLAUDE.md escalate rule no longer references 0.5
- [x] wiki/RUNBOOK.md has score distribution section with real data
- [x] Agent 9 documented in CLAUDE_AGENTS.md
- [ ] `/chat/ask` returns a useful answer for "What changed in Banner?"

---

## Not In Scope

- Changes to the citesearch backend scoring model
- Modifying Azure AI Search index configuration
- Botpress flow changes (the `escalate` boolean drives those; logic change is transparent)
- gRPC changes

---

## Phase E — Banner User Guide Q&A

**Goal:** Let the chatbot answer "how to use Banner" questions by routing to the indexed
user guide PDFs (`source_type=banner_user_guide`). No version/year filters needed — these
are functional user guides, not release notes.

**Backend reality (do not modify the backend):**
- User guide PDFs sit in `data/docs/banner/<module>/use/` folders.
- The ingest pipeline already detects `/use/` paths → tags chunks as `banner_user_guide`.
- `POST /banner/:module/ask` (ModuleAsk handler) accepts `source_type` in the request body.
  Passing `source_type=banner_user_guide` scopes search to user guide chunks only.
- `/banner/student/ask` (StudentAsk handler) hardcodes `SourceTypeBannerGuide` — already works.

**Available PDFs (as of 2026-04-16):**
- `data/docs/banner/general/use/Banner General - Use - Ellucian.pdf` — General module
- `data/docs/banner/student/use/Banner Student - Use - Ellucian.pdf` — Student module
- `data/docs/banner/finance/use/` — Finance module (PDFs TBD)

**New adapter method:**
```go
AskBannerGuide(ctx context.Context, question string, module string) (AdapterResponse, error)
// Calls POST /banner/:module/ask with body {"question":"...","source_type":"banner_user_guide"}
// No version_filter, no year_filter — never set them for user guide queries.
```

**New intent: `BannerUsage`** (6th intent, extends the 5-intent set)

Covers questions about navigating Banner ERP — forms, fields, screens, lookups, workflows.
Differentiates from `SopQuery` (internal IT procedures) and `BannerAdmin` (config/setup).

Keyword scoring: each match = word_count × 0.3. Multi-word phrases beat SopQuery's generic
"how to" (0.6) when more specific phrases match (e.g., "how to use" = 0.9).

**New source values for `/chat/ask`:**
- `source=user_guide` → module=General → `AskBannerGuide(ctx, msg, "general")`
- `source=user_guide_student` → module=Student → `AskBannerGuide(ctx, msg, "student")`
- `source=user_guide_finance` → module=Finance → `AskBannerGuide(ctx, msg, "finance")`
  (returns 400 until finance user guide PDFs are ingested)

**Intent → source routing addition:**
`BannerUsage` → `user_guide` (default module=General)

---

### Tasks

- [ ] **E.1 — TDD: `BannerUsage` intent keywords**

  **Prompt for implementer:**
  In `internal/intent/classifier.go`, add `BannerUsage` as a 6th intent. Add to `IntentConfig`
  and `DefaultIntentConfig`. Keyword set (start with these, refine based on test failures):
  ```go
  BannerUsage: []string{
      "how to use", "navigate banner", "banner navigation",
      "banner form", "banner screen", "banner menu",
      "user guide", "where do i", "look up a",
      "what is the banner", "how do i find",
  },
  ```

  TDD sequence:
  1. **RED** — add tests in `internal/intent/classifier_test.go`:
     - `"How do I navigate the Banner main menu?"` → `BannerUsage`
     - `"Where do I find the journal entry form in Banner?"` → `BannerUsage`
     - `"How to use the Banner Finance module"` → `BannerUsage` (not SopQuery)
     - `"How to restart the Banner server"` → `SopQuery` (must NOT change)
     - `"What changed in Banner Finance?"` → `BannerRelease` (must NOT change)
  2. **GREEN** — add `BannerUsage Intent = "BannerUsage"` constant, extend `IntentConfig`,
     add keywords to `DefaultIntentConfig`.
  3. **REFACTOR** — check that all pre-existing intent tests still pass.

  Run: `go test ./internal/intent/... -v`

- [ ] **E.2 — TDD: `AskBannerGuide` adapter method**

  **Prompt for implementer:**
  In `internal/adapter/client.go`, add:
  ```go
  // bannerGuideAskRequest is the JSON body for /banner/:module/ask with user guide source.
  type bannerGuideAskRequest struct {
      Question   string `json:"question"`
      SourceType string `json:"source_type"`
      TopK       int    `json:"top_k,omitempty"`
  }

  // AskBannerGuide queries /banner/:module/ask scoped to banner_user_guide source type.
  // No version or year filter — user guide docs are not versioned.
  func (c *AdapterClient) AskBannerGuide(ctx context.Context, question string, module string) (AdapterResponse, error) {
      path := fmt.Sprintf("/banner/%s/ask", strings.ToLower(module))
      body := bannerGuideAskRequest{
          Question:   question,
          SourceType: "banner_user_guide",
      }
      // ... same POST + mapResponse pattern as AskBanner
  }
  ```

  TDD sequence:
  1. **RED** — in `internal/adapter/client_test.go`, add:
     - `TestAdapterClient_AskBannerGuide_Success` — httptest server returns valid response,
       assert `Escalate==false`, `Confidence > 0`, URL path == `/banner/general/ask`,
       body has `source_type=banner_user_guide`, no `version_filter`/`year_filter`
     - `TestAdapterClient_AskBannerGuide_NoResults_Escalates` — server returns
       `retrieval_count=0`, assert `Escalate==true`
     - `TestAdapterClient_AskBannerGuide_Module` — assert URL path uses the provided module
       (e.g., `/banner/student/ask`)
  2. **GREEN** — implement `AskBannerGuide`.
  3. **REFACTOR** — extract any shared HTTP POST + mapResponse helper if >2 methods share it.

  Also add `AskBannerGuide` to the `AdapterClient` interface in `api/handlers.go`.

  Run: `go test ./internal/adapter/... -v`

- [x] **E.3 — TDD: `user_guide` source routing in `/chat/ask`**

  **Prompt for implementer:**
  In `api/handlers.go`:
  1. Add `AskBannerGuide(ctx context.Context, question string, module string) (AdapterResponse, error)`
     to the `AdapterClient` interface.
  2. In `sourceFromIntent`, map `BannerUsage` → `"user_guide"`.
  3. In `askHandler`, add cases:
     ```go
     case "user_guide":
         resp, err = client.AskBannerGuide(r.Context(), req.Message, "general")
     case "user_guide_student":
         resp, err = client.AskBannerGuide(r.Context(), req.Message, "student")
     case "user_guide_finance":
         resp, err = client.AskBannerGuide(r.Context(), req.Message, "finance")
     ```
  4. Add `"user_guide"`, `"user_guide_student"`, `"user_guide_finance"` to the valid source
     reject-list guard (they must NOT return 400).

  TDD sequence:
  1. **RED** — in `api/handlers_test.go`, add:
     - `TestChatAsk_UserGuide_RoutesToGeneral` — POST `{source:"user_guide"}`, mock client
       receives `AskBannerGuide` call with module="general"
     - `TestChatAsk_UserGuide_Student` — POST `{source:"user_guide_student"}`, module="student"
     - `TestChatAsk_UserGuide_Finance` — POST `{source:"user_guide_finance"}`, module="finance"
     - `TestChatAsk_BannerUsageIntent_RoutesToUserGuide` — POST `{intent:"BannerUsage"}` with
       no source field → resolved to `user_guide` → module="general"
  2. **GREEN** — wire intent+source routing.
  3. **REFACTOR** — ensure mock interface in test file includes `AskBannerGuide`.

  Run: `go test ./api/... -v`

- [x] **E.4 — Update CLAUDE.md**

  **Prompt for implementer:**
  In `CLAUDE.md`:
  1. Add `BannerUsage` to the intent set (extend from 5 to 6 intents):
     ```
     BannerUsage    → questions about navigating Banner forms, screens, fields, lookups, workflows
     ```
  2. Add to the intent → backend routing table:
     ```
     BannerUsage    → /banner/general/ask  source_type=banner_user_guide  module=General
     ```
  3. Add new source override values:
     ```
     source=user_guide         → /banner/general/ask  source_type=banner_user_guide
     source=user_guide_student → /banner/student/ask  source_type=banner_user_guide
     source=user_guide_finance → /banner/finance/ask  source_type=banner_user_guide
     ```
  4. Add note: "No version_filter or year_filter for user_guide sources — user guide PDFs
     carry no version metadata."

- [x] **E.5 — Document Agent 10 in wiki/CLAUDE_AGENTS.md**

  **Prompt for implementer:**
  Append Agent 10 (Banner User Guide Navigation Agent) to `wiki/CLAUDE_AGENTS.md`.
  See the Agent 10 section already added below. Add to TOC and Tool Reference table.

- [x] **E.6 — Update wiki/CHATBOT.md**

  **Prompt for implementer:**
  In `wiki/CHATBOT.md`:
  1. Add `BannerUsage` to the intent set table.
  2. Add `user_guide`, `user_guide_student`, `user_guide_finance` to the source override table.
  3. Add a new section "## User Guide Q&A" describing when to use `user_guide` vs `banner`
     and the no-version-filter rule.

---

### Phase E Acceptance Criteria

- [ ] `go test ./... -v` passes with 0 failures
- [ ] `intent="BannerUsage"` + no source → routes to `AskBannerGuide(..., "general")`
- [ ] `source="user_guide_student"` → routes to `AskBannerGuide(..., "student")`
- [ ] `source="user_guide"` request body has `source_type=banner_user_guide`, no `version_filter`
- [ ] "How do I navigate the Banner main menu?" → intent `BannerUsage` (not SopQuery)
- [ ] "How to restart the Banner server" → intent `SopQuery` (unchanged)
- [ ] CLAUDE.md documents 6th intent and 3 new source values
- [ ] Agent 10 documented in CLAUDE_AGENTS.md

---

## Phase F — Student Wizard: `POST /student/wizard` (TDD)

**Goal:** Implement the shared wizard engine that powers all Banner Student guided workflows.
Phase 1.1 (Create a Course) is the scope of this phase — it delivers the engine in full.
The engine is generic: adding Phase 2–4 wizards requires only new step sequences, no engine changes.

**Constraints:**
- No external dependencies — stdlib + testify only
- No LLM calls from Go code — all validation is deterministic
- The citesearch escape hatch is a plain HTTP call through the existing `AdapterClient.AskBannerGuide`
- `go test ./... -v` (no `-race`; CGO unavailable) must stay green at every task boundary

**Package structure (new):**
```
internal/wizard/
├── types.go    → WizardRequest, WizardResponse, WizardSession, WizardReview
├── session.go  → SessionStore interface + MemorySessionStore
├── registry.go → StepDefinition type + Registry
├── validate.go → field validators: subject code, course number, credit hours, term code, etc.
├── catalog.go  → CourseCreationSteps (Phase 1.1), DefaultRegistry()
└── engine.go   → WizardEngine, ErrUnknownWizard, ErrSessionNotFound
```

**Handler wiring (existing files, minimal change):**
```
api/handlers.go → adds WizardRunner interface + /student/wizard route
                  NewChatHandler gains a second parameter: WizardRunner
cmd/server/main.go → constructs WizardEngine + wires into NewChatHandler
```

---

### Tasks

- [ ] **F.1 — Define wizard types in `internal/wizard/types.go`**

  **Prompt for implementer:**
  Create `internal/wizard/types.go` with these types. Do not add any logic — types only.

  ```go
  package wizard

  // WizardRequest is the JSON body for POST /student/wizard.
  // Exactly one of Value or Question should be non-empty on non-init turns.
  type WizardRequest struct {
      SessionID string `json:"session_id"` // required always
      Wizard    string `json:"wizard"`     // required on init (Step == ""); ignored on subsequent turns
      Step      string `json:"step"`       // current step being answered; empty = init
      Value     string `json:"value"`      // user's field answer (mutually exclusive with Question)
      Question  string `json:"question"`   // escape hatch: citesearch lookup instead of validation
  }

  // WizardResponse is returned by POST /student/wizard.
  // On a normal step advance: Valid, NextStep, Prompt, Options, Session.
  // On escape hatch: Answer, ResumeStep, Prompt, Session.
  // On review: Review, Prompt, Session.
  type WizardResponse struct {
      Valid     *bool    `json:"valid,omitempty"`     // true=accepted, false=rejected; nil for lookup/init
      NextStep  string   `json:"next_step,omitempty"` // next step name; "review" when all required done
      Prompt    string   `json:"prompt"`              // always present: question or resume message
      Options   []string `json:"options,omitempty"`   // non-nil = picklist field

      Answer     string `json:"answer,omitempty"`      // escape hatch: citesearch answer text
      ResumeStep string `json:"resume_step,omitempty"` // escape hatch: same value as req.Step

      Review *WizardReview `json:"review,omitempty"` // populated when all required fields collected

      Session WizardSession `json:"session"` // always present
  }

  // WizardReview is included when all required fields are collected, before final confirmation.
  type WizardReview struct {
      Fields  map[string]string `json:"fields"`
      Message string            `json:"message"` // e.g. "Here's what I have. Does everything look correct?"
  }

  // WizardSession accumulates collected fields across turns.
  type WizardSession struct {
      SessionID   string            `json:"session_id"`
      Wizard      string            `json:"wizard"`
      CurrentStep string            `json:"current_step"` // "review" when awaiting confirmation
      Fields      map[string]string `json:"fields"`
      Complete    bool              `json:"complete"` // true after confirm
  }
  ```

  TDD in `internal/wizard/types_test.go`:
  1. **RED** — write JSON round-trip tests for both `WizardRequest` and `WizardResponse`:
     ```go
     func TestWizardRequest_JSONRoundTrip(t *testing.T) {
         req := WizardRequest{SessionID: "s1", Wizard: "course_creation", Step: "subject_code", Value: "COMP"}
         data, err := json.Marshal(req)
         require.NoError(t, err)
         var got WizardRequest
         require.NoError(t, json.Unmarshal(data, &got))
         assert.Equal(t, req, got)
     }
     func TestWizardResponse_OmitsNilFields(t *testing.T) {
         // Valid=nil and Options=nil must not appear in JSON output
         resp := WizardResponse{Prompt: "What is the subject code?", Session: WizardSession{SessionID: "s1", Fields: map[string]string{}}}
         data, _ := json.Marshal(resp)
         assert.NotContains(t, string(data), `"valid"`)
         assert.NotContains(t, string(data), `"options"`)
     }
     ```
  2. **GREEN** — the struct definitions above make these pass immediately.
  3. **REFACTOR** — none at this stage.

  Run: `go test ./internal/wizard/... -v`

- [ ] **F.2 — `SessionStore` interface + `MemorySessionStore` in `internal/wizard/session.go`**

  **Prompt for implementer:**
  Create `internal/wizard/session.go`:

  ```go
  package wizard

  import "sync"

  // SessionStore manages wizard session state across turns.
  type SessionStore interface {
      Get(sessionID string) (WizardSession, bool)
      Put(session WizardSession)
      Delete(sessionID string)
  }

  // MemorySessionStore is a thread-safe in-memory SessionStore.
  type MemorySessionStore struct {
      mu       sync.RWMutex
      sessions map[string]WizardSession
  }

  func NewMemorySessionStore() *MemorySessionStore {
      return &MemorySessionStore{sessions: make(map[string]WizardSession)}
  }
  ```

  Implement `Get` (RLock), `Put` (Lock), `Delete` (Lock).

  TDD in `internal/wizard/session_test.go`:
  1. **RED** — tests before implementation:
     ```go
     func TestMemorySessionStore_GetMiss(t *testing.T) {
         store := NewMemorySessionStore()
         _, ok := store.Get("s1")
         assert.False(t, ok)
     }
     func TestMemorySessionStore_PutAndGet(t *testing.T) {
         store := NewMemorySessionStore()
         sess := WizardSession{SessionID: "s1", Wizard: "course_creation", CurrentStep: "subject_code", Fields: map[string]string{}}
         store.Put(sess)
         got, ok := store.Get("s1")
         require.True(t, ok)
         assert.Equal(t, sess, got)
     }
     func TestMemorySessionStore_Delete(t *testing.T) {
         store := NewMemorySessionStore()
         store.Put(WizardSession{SessionID: "s1", Fields: map[string]string{}})
         store.Delete("s1")
         _, ok := store.Get("s1")
         assert.False(t, ok)
     }
     func TestMemorySessionStore_OverwritePut(t *testing.T) {
         store := NewMemorySessionStore()
         store.Put(WizardSession{SessionID: "s1", CurrentStep: "a", Fields: map[string]string{}})
         store.Put(WizardSession{SessionID: "s1", CurrentStep: "b", Fields: map[string]string{}})
         got, _ := store.Get("s1")
         assert.Equal(t, "b", got.CurrentStep)
     }
     ```
  2. **GREEN** — implement using `sync.RWMutex`.
  3. **REFACTOR** — none.

  Run: `go test ./internal/wizard/... -v`

- [ ] **F.3 — `StepDefinition`, `Registry`, and `CourseCreationSteps` in `registry.go` + `catalog.go`**

  **Prompt for implementer:**
  Create `internal/wizard/registry.go`:

  ```go
  package wizard

  // StepDefinition describes one field-collection step in a wizard.
  type StepDefinition struct {
      Name     string           // field key stored in WizardSession.Fields
      Prompt   string           // question shown to the user
      Required bool             // empty value is rejected when Required=true
      Options  []string         // non-nil = picklist; value must be in this list
      Validate func(string) error // nil = no additional validation beyond Required + Options
  }

  // Registry maps wizard names to their ordered step sequences.
  type Registry struct {
      wizards map[string][]StepDefinition
  }

  func NewRegistry() *Registry {
      return &Registry{wizards: make(map[string][]StepDefinition)}
  }

  func (r *Registry) Register(name string, steps []StepDefinition) { r.wizards[name] = steps }
  func (r *Registry) Steps(name string) ([]StepDefinition, bool) {
      steps, ok := r.wizards[name]
      return steps, ok
  }
  ```

  Create `internal/wizard/catalog.go` with the Phase 1.1 Create a Course sequence (SCACRSE).
  Required fields first; optional after. Field order matches Banner SCACRSE top-to-bottom.

  ```go
  package wizard

  var CourseCreationSteps = []StepDefinition{
      {Name: "subject_code",    Prompt: "What is the subject code? (e.g. COMP, MATH, ENGL)", Required: true, Validate: ValidateSubjectCode},
      {Name: "course_number",   Prompt: "What is the course number? (e.g. 3850, 101H)",      Required: true, Validate: ValidateCourseNumber},
      {Name: "short_title",     Prompt: "What is the short course title? (max 30 characters)", Required: true, Validate: ValidateShortTitle},
      {Name: "long_title",      Prompt: "What is the full course title? (max 100 characters, or 'skip')", Required: false, Validate: ValidateLongTitle},
      {Name: "credit_hours",    Prompt: "How many credit hours? (e.g. 3, 1.5)",              Required: true, Validate: ValidateCreditHours},
      {Name: "course_level",    Prompt: "What is the course level?",                          Required: true, Options: []string{"UG", "GR", "CE"}},
      {Name: "schedule_type",   Prompt: "What is the schedule type?",                         Required: true, Options: []string{"Lecture", "Lab", "Seminar", "Online", "Hybrid"}},
      {Name: "grading_mode",    Prompt: "What is the grading mode?",                          Required: true, Options: []string{"Standard", "Pass-Fail", "Audit"}},
      {Name: "college",         Prompt: "What is the college code? (e.g. AS, BU, ED)",        Required: true, Validate: ValidateCollegeCode},
      {Name: "department",      Prompt: "What is the department code? (e.g. COMP, MATH)",     Required: true, Validate: ValidateDeptCode},
      {Name: "prerequisites",   Prompt: "Any prerequisites? (describe or type 'none')",        Required: false},
      {Name: "corequisites",    Prompt: "Any co-requisites? (describe or type 'none')",        Required: false},
      {Name: "effective_term",  Prompt: "What is the effective term? (YYYYTT format, e.g. 202510 = Spring 2025)", Required: true, Validate: ValidateTermCode},
      {Name: "status",          Prompt: "What is the initial course status?",                 Required: true, Options: []string{"Active", "Inactive", "Pending"}},
  }

  // DefaultRegistry returns a registry pre-loaded with Phase 1 catalog wizards.
  func DefaultRegistry() *Registry {
      r := NewRegistry()
      r.Register("course_creation", CourseCreationSteps)
      return r
  }
  ```

  TDD in `internal/wizard/registry_test.go`:
  1. **RED**:
     ```go
     func TestDefaultRegistry_CourseCreation_StepsInOrder(t *testing.T) {
         r := DefaultRegistry()
         steps, ok := r.Steps("course_creation")
         require.True(t, ok)
         assert.Equal(t, "subject_code", steps[0].Name, "first step must be subject_code")
         assert.Equal(t, "status", steps[len(steps)-1].Name, "last step must be status")
         assert.True(t, steps[0].Required)
     }
     func TestDefaultRegistry_CourseLevel_IsPicklist(t *testing.T) {
         r := DefaultRegistry()
         steps, _ := r.Steps("course_creation")
         level := findStep(t, steps, "course_level")
         assert.ElementsMatch(t, []string{"UG", "GR", "CE"}, level.Options)
         assert.Nil(t, level.Validate)
     }
     func TestDefaultRegistry_UnknownWizard_ReturnsFalse(t *testing.T) {
         r := DefaultRegistry()
         _, ok := r.Steps("nonexistent_wizard")
         assert.False(t, ok)
     }
     func findStep(t *testing.T, steps []StepDefinition, name string) StepDefinition {
         t.Helper()
         for _, s := range steps {
             if s.Name == name { return s }
         }
         t.Fatalf("step %q not found in sequence", name)
         return StepDefinition{}
     }
     ```
  2. **GREEN** — the catalog/registry code above makes these pass.
  3. **REFACTOR** — none.

  Run: `go test ./internal/wizard/... -v`

- [ ] **F.4 — Field validators in `internal/wizard/validate.go`**

  **Prompt for implementer:**
  Create `internal/wizard/validate.go`. Each validator is `func(string) error`.
  Use only `regexp`, `strconv`, `strings`, `fmt` from stdlib.

  | Validator | Rule |
  |---|---|
  | `ValidateSubjectCode` | 2–4 uppercase alpha characters only (A-Z) |
  | `ValidateCourseNumber` | 1–6 alphanumeric characters (letters and digits allowed) |
  | `ValidateShortTitle` | Non-empty, max 30 characters |
  | `ValidateLongTitle` | Max 100 characters; "skip" or "" → valid (optional field) |
  | `ValidateCreditHours` | Parseable float64, > 0, ≤ 12, at most 2 decimal places |
  | `ValidateCollegeCode` | 2–6 uppercase alphanumeric characters |
  | `ValidateDeptCode` | 2–6 uppercase alphanumeric characters |
  | `ValidateTermCode` | Exactly 6 digits; first 4 = year 2000–2099; last 2 = 10, 20, or 30 |

  TDD in `internal/wizard/validate_test.go`:
  1. **RED** — table-driven tests for each validator:
     ```go
     func TestValidateSubjectCode(t *testing.T) {
         cases := []struct{ in string; wantErr bool }{
             {"COMP", false}, {"CS", false}, {"MATH", false},
             {"", true},      // empty
             {"C", true},     // too short
             {"COMPS", true}, // too long
             {"comp", true},  // lowercase
             {"COM1", true},  // digit in subject code
         }
         for _, c := range cases {
             err := ValidateSubjectCode(c.in)
             if c.wantErr { assert.Error(t, err, c.in) } else { assert.NoError(t, err, c.in) }
         }
     }
     func TestValidateCreditHours(t *testing.T) {
         cases := []struct{ in string; wantErr bool }{
             {"3", false}, {"1.5", false}, {"0.5", false}, {"12", false},
             {"0", true},     // zero not allowed
             {"0.0", true},   // zero
             {"-1", true},    // negative
             {"13", true},    // exceeds 12
             {"1.123", true}, // more than 2 decimal places
             {"abc", true},   // not a number
         }
         // ...
     }
     func TestValidateTermCode(t *testing.T) {
         cases := []struct{ in string; wantErr bool }{
             {"202510", false}, // Spring 2025
             {"202520", false}, // Summer 2025
             {"202530", false}, // Fall 2025
             {"20251", true},   // 5 digits
             {"2025100", true}, // 7 digits
             {"202540", true},  // invalid term suffix (40 not allowed)
             {"199910", true},  // year < 2000
             {"210010", true},  // year > 2099
             {"abcdef", true},  // non-numeric
         }
         // ...
     }
     // Also test ValidateShortTitle, ValidateLongTitle, ValidateCourseNumber,
     // ValidateCollegeCode, ValidateDeptCode — at least 3 valid + 2 invalid cases each.
     ```
  2. **GREEN** — implement each validator. Extract shared `isUpperAlpha(s string) bool` and
     `isUpperAlphanumeric(s string) bool` helpers; both are used by 2+ validators.
  3. **REFACTOR** — check for duplicated regex patterns; consolidate if >1 validator shares the same underlying pattern.

  Run: `go test ./internal/wizard/... -v`

- [ ] **F.5 — `WizardEngine` in `internal/wizard/engine.go`**

  **Prompt for implementer:**
  Create `internal/wizard/engine.go`. The engine is the core: it processes one turn,
  applies the right behavior based on what's in the request, and returns a `WizardResponse`.

  ```go
  package wizard

  import (
      "context"
      "errors"
      "fmt"
  )

  var (
      ErrUnknownWizard   = errors.New("unknown wizard")
      ErrSessionNotFound = errors.New("session not found")
  )

  // LookupFn is called when the user sends a Question instead of a Value.
  // Returns the citesearch answer as plain text.
  type LookupFn func(ctx context.Context, question string) (string, error)

  // WizardEngine processes wizard turns: init, field submission, escape hatch, review.
  type WizardEngine struct {
      store    SessionStore
      registry *Registry
      lookup   LookupFn
  }

  func NewWizardEngine(store SessionStore, registry *Registry, lookup LookupFn) *WizardEngine {
      return &WizardEngine{store: store, registry: registry, lookup: lookup}
  }
  ```

  **`Handle` behavior — implement in this order:**

  **Case 1 — Init** (`req.Step == "" && req.Value == "" && req.Question == ""`):
  - Look up wizard in registry; if not found → return `ErrUnknownWizard`
  - Create `WizardSession{SessionID: req.SessionID, Wizard: wizard, CurrentStep: steps[0].Name, Fields: map[string]string{}}`
  - Store session
  - Return `WizardResponse{NextStep: steps[0].Name, Prompt: steps[0].Prompt, Options: steps[0].Options, Session: session}`

  **Case 2 — Escape hatch** (`req.Question != ""`):
  - Load session from store; if not found → return `ErrSessionNotFound`
  - Find current step prompt from registry (look up session.CurrentStep)
  - Call `e.lookup(ctx, req.Question)`; if error → propagate
  - Return `WizardResponse{Answer: answer, ResumeStep: req.Step, Prompt: "Got it — back to: " + currentStepPrompt, Session: session}`

  **Case 3 — Review action** (`session.CurrentStep == "review"`):
  - Load session; if not found → return `ErrSessionNotFound`
  - `req.Value == "confirm"` → mark `session.Complete = true`, store, return review response with `Complete: true`
  - `req.Value == "restart"` → delete session, return `WizardResponse{Prompt: "Session cleared. Send a new wizard to start over."}`
  - `req.Value` starts with `"edit:"` → extract field name, find its step def, set `session.CurrentStep = fieldName`, store, return that step's prompt

  **Case 4 — Field submission** (`req.Value != ""`):
  - Load session; if not found → return `ErrSessionNotFound`
  - Get wizard steps from registry (use session.Wizard, not req.Wizard)
  - Find the step def matching `req.Step`
  - Validate: if `step.Options != nil`, check `req.Value` is in the list; else if `step.Validate != nil`, call it
  - If invalid → `valid := false; return WizardResponse{Valid: &valid, Prompt: errMsg + "\n\n" + step.Prompt, Options: step.Options, Session: session}`
  - If valid → store `session.Fields[step.Name] = req.Value`
  - Advance to next step: find index of current step + 1
  - If no more steps: check all `Required` steps are collected
    - Missing required fields → find first missing → set as next step, prompt it
    - All required satisfied → set `session.CurrentStep = "review"`, build `WizardReview{Fields: session.Fields, Message: "Here's what I have. Does everything look correct?\nReply 'confirm', 'restart', or 'edit:<field_name>' to change a specific field."}`, return with `valid := true`
  - If more steps exist → set `session.CurrentStep = nextStep.Name`, store, return `WizardResponse{Valid: &true, NextStep: nextStep.Name, Prompt: nextStep.Prompt, Options: nextStep.Options, Session: session}`

  TDD in `internal/wizard/engine_test.go` — write ALL tests before implementing `Handle`:
  ```go
  func newTestEngine(t *testing.T) (*WizardEngine, *MemorySessionStore) {
      t.Helper()
      store := NewMemorySessionStore()
      lookup := func(_ context.Context, q string) (string, error) {
          return "lookup answer: " + q, nil
      }
      return NewWizardEngine(store, DefaultRegistry(), lookup), store
  }

  func TestEngine_Init_ReturnsFirstPrompt(t *testing.T) {
      engine, _ := newTestEngine(t)
      resp, err := engine.Handle(context.Background(), WizardRequest{SessionID: "s1", Wizard: "course_creation"})
      require.NoError(t, err)
      assert.Equal(t, "subject_code", resp.NextStep)
      assert.Contains(t, resp.Prompt, "subject code")
      assert.Equal(t, "s1", resp.Session.SessionID)
  }

  func TestEngine_Init_UnknownWizard_ReturnsError(t *testing.T) {
      engine, _ := newTestEngine(t)
      _, err := engine.Handle(context.Background(), WizardRequest{SessionID: "s1", Wizard: "does_not_exist"})
      assert.ErrorIs(t, err, ErrUnknownWizard)
  }

  func TestEngine_SubmitValidValue_AdvancesToNextStep(t *testing.T) {
      engine, store := newTestEngine(t)
      engine.Handle(context.Background(), WizardRequest{SessionID: "s1", Wizard: "course_creation"})
      trueVal := true
      resp, err := engine.Handle(context.Background(), WizardRequest{SessionID: "s1", Step: "subject_code", Value: "COMP"})
      require.NoError(t, err)
      assert.Equal(t, &trueVal, resp.Valid)
      assert.Equal(t, "course_number", resp.NextStep)
      sess, _ := store.Get("s1")
      assert.Equal(t, "COMP", sess.Fields["subject_code"])
  }

  func TestEngine_SubmitInvalidValue_RepromptsSameStep(t *testing.T) {
      engine, _ := newTestEngine(t)
      engine.Handle(context.Background(), WizardRequest{SessionID: "s1", Wizard: "course_creation"})
      falseVal := false
      resp, err := engine.Handle(context.Background(), WizardRequest{SessionID: "s1", Step: "subject_code", Value: "comp"})
      require.NoError(t, err)
      assert.Equal(t, &falseVal, resp.Valid)
      assert.Contains(t, resp.Prompt, "subject code")
  }

  func TestEngine_InvalidPicklistValue_RepromptWithOptions(t *testing.T) {
      engine, _ := newTestEngine(t)
      engine.Handle(context.Background(), WizardRequest{SessionID: "s1", Wizard: "course_creation"})
      advanceTo(t, engine, "s1", "course_level")
      resp, _ := engine.Handle(context.Background(), WizardRequest{SessionID: "s1", Step: "course_level", Value: "GRADUATE"})
      falseVal := false
      assert.Equal(t, &falseVal, resp.Valid)
      assert.ElementsMatch(t, []string{"UG", "GR", "CE"}, resp.Options)
  }

  func TestEngine_EscapeHatch_ReturnsAnswerAndResumesStep(t *testing.T) {
      engine, _ := newTestEngine(t)
      engine.Handle(context.Background(), WizardRequest{SessionID: "s1", Wizard: "course_creation"})
      resp, err := engine.Handle(context.Background(), WizardRequest{
          SessionID: "s1", Step: "subject_code", Question: "What format should the subject code be?",
      })
      require.NoError(t, err)
      assert.Contains(t, resp.Answer, "lookup answer")
      assert.Equal(t, "subject_code", resp.ResumeStep)
      assert.Contains(t, resp.Prompt, "subject code")
  }

  func TestEngine_AllRequiredFields_TransitionsToReview(t *testing.T) {
      engine, _ := newTestEngine(t)
      resp := driveToReview(t, engine, "s1")
      assert.NotNil(t, resp.Review)
      assert.Equal(t, "review", resp.Session.CurrentStep)
      assert.Equal(t, "COMP", resp.Review.Fields["subject_code"])
  }

  func TestEngine_ReviewConfirm_SetsComplete(t *testing.T) {
      engine, store := newTestEngine(t)
      driveToReview(t, engine, "s1")
      _, err := engine.Handle(context.Background(), WizardRequest{SessionID: "s1", Step: "review", Value: "confirm"})
      require.NoError(t, err)
      sess, _ := store.Get("s1")
      assert.True(t, sess.Complete)
  }

  func TestEngine_ReviewRestart_ClearsSession(t *testing.T) {
      engine, store := newTestEngine(t)
      driveToReview(t, engine, "s1")
      _, err := engine.Handle(context.Background(), WizardRequest{SessionID: "s1", Step: "review", Value: "restart"})
      require.NoError(t, err)
      _, ok := store.Get("s1")
      assert.False(t, ok, "session must be deleted on restart")
  }

  func TestEngine_ReviewEdit_JumpsToField(t *testing.T) {
      engine, _ := newTestEngine(t)
      driveToReview(t, engine, "s1")
      resp, err := engine.Handle(context.Background(), WizardRequest{SessionID: "s1", Step: "review", Value: "edit:subject_code"})
      require.NoError(t, err)
      assert.Contains(t, resp.Prompt, "subject code")
      assert.Equal(t, "subject_code", resp.Session.CurrentStep)
  }

  func TestEngine_SessionNotFound_ReturnsError(t *testing.T) {
      engine, _ := newTestEngine(t)
      _, err := engine.Handle(context.Background(), WizardRequest{SessionID: "ghost", Step: "subject_code", Value: "COMP"})
      assert.ErrorIs(t, err, ErrSessionNotFound)
  }

  // advanceTo submits valid synthetic values for all steps up to (but not including) targetStep.
  func advanceTo(t *testing.T, engine *WizardEngine, sessionID, targetStep string) {
      t.Helper()
      validValues := map[string]string{
          "subject_code": "COMP", "course_number": "3850", "short_title": "Software Eng",
          "long_title": "skip", "credit_hours": "3", "course_level": "UG",
          "schedule_type": "Lecture", "grading_mode": "Standard",
          "college": "AS", "department": "COMP",
          "prerequisites": "none", "corequisites": "none",
          "effective_term": "202510", "status": "Active",
      }
      steps, _ := DefaultRegistry().Steps("course_creation")
      for _, step := range steps {
          if step.Name == targetStep { return }
          engine.Handle(context.Background(), WizardRequest{SessionID: sessionID, Step: step.Name, Value: validValues[step.Name]})
      }
  }

  // driveToReview drives all steps with valid synthetic values and returns the review response.
  func driveToReview(t *testing.T, engine *WizardEngine, sessionID string) WizardResponse {
      t.Helper()
      engine.Handle(context.Background(), WizardRequest{SessionID: sessionID, Wizard: "course_creation"})
      validValues := map[string]string{
          "subject_code": "COMP", "course_number": "3850", "short_title": "Software Eng",
          "long_title": "skip", "credit_hours": "3", "course_level": "UG",
          "schedule_type": "Lecture", "grading_mode": "Standard",
          "college": "AS", "department": "COMP",
          "prerequisites": "none", "corequisites": "none",
          "effective_term": "202510", "status": "Active",
      }
      steps, _ := DefaultRegistry().Steps("course_creation")
      var last WizardResponse
      for _, step := range steps {
          resp, err := engine.Handle(context.Background(), WizardRequest{SessionID: sessionID, Step: step.Name, Value: validValues[step.Name]})
          require.NoError(t, err)
          last = resp
      }
      return last
  }
  ```

  Run: `go test ./internal/wizard/... -v`

- [ ] **F.6 — `WizardRunner` interface + `/student/wizard` handler in `api/handlers.go`**

  **Prompt for implementer:**
  `api/handlers.go` already defines `AdapterClient` as an interface so tests can mock it.
  Apply the same pattern for the wizard engine.

  **Step 1 — Add `WizardRunner` interface and update `NewChatHandler`:**
  ```go
  // WizardRunner is the interface the /student/wizard handler depends on.
  // The concrete *wizard.WizardEngine satisfies this interface.
  type WizardRunner interface {
      Handle(ctx context.Context, req wizardpkg.WizardRequest) (wizardpkg.WizardResponse, error)
  }
  ```
  Import `citesearch/internal/wizard` as `wizardpkg` to avoid collision with the local `wizard` identifier.

  Update `NewChatHandler` signature:
  ```go
  func NewChatHandler(client AdapterClient, runner WizardRunner) *ChatHandler {
      h := &ChatHandler{mux: http.NewServeMux()}
      h.mux.HandleFunc("/chat/ask", askHandler(client))
      h.mux.HandleFunc("/chat/sentiment", sentimentHandler(sentiment.NewAnalyzer(sentiment.DefaultConfig())))
      h.mux.HandleFunc("/chat/intent", intentHandler(intent.NewClassifier(intent.DefaultIntentConfig())))
      h.mux.HandleFunc("/student/wizard", wizardHandler(runner))
      h.mux.HandleFunc("/health", healthHandler)
      return h
  }
  ```

  **Step 2 — Implement `wizardHandler`:**
  ```go
  func wizardHandler(runner WizardRunner) http.HandlerFunc {
      return func(w http.ResponseWriter, r *http.Request) {
          if r.Method != http.MethodPost {
              writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
              return
          }
          var req wizardpkg.WizardRequest
          if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
              writeJSONError(w, http.StatusBadRequest, "invalid JSON")
              return
          }
          if req.SessionID == "" {
              writeJSONError(w, http.StatusBadRequest, "session_id is required")
              return
          }
          resp, err := runner.Handle(r.Context(), req)
          if err != nil {
              switch {
              case errors.Is(err, wizardpkg.ErrUnknownWizard):
                  writeJSONError(w, http.StatusBadRequest, "unknown wizard")
              case errors.Is(err, wizardpkg.ErrSessionNotFound):
                  writeJSONError(w, http.StatusNotFound, "session not found")
              default:
                  writeJSONError(w, http.StatusInternalServerError, "internal server error")
              }
              return
          }
          w.Header().Set("Content-Type", "application/json")
          json.NewEncoder(w).Encode(resp)
      }
  }
  ```

  **Step 3 — Update `handlers_test.go`:**
  All existing calls `NewChatHandler(mockClient)` must become `NewChatHandler(mockClient, noopRunner())`.
  Add a no-op runner at the top of the test file:
  ```go
  type noopWizardRunner struct{}
  func (n *noopWizardRunner) Handle(_ context.Context, _ wizardpkg.WizardRequest) (wizardpkg.WizardResponse, error) {
      return wizardpkg.WizardResponse{}, nil
  }
  func noopRunner() WizardRunner { return &noopWizardRunner{} }
  ```

  TDD in `api/handlers_test.go`:
  1. **RED** — add wizard handler tests before implementing:
     ```go
     func TestWizardHandler_MethodNotAllowed(t *testing.T) {
         h := NewChatHandler(&mockAdapterClient{}, noopRunner())
         req := httptest.NewRequest(http.MethodGet, "/student/wizard", nil)
         w := httptest.NewRecorder()
         h.ServeHTTP(w, req)
         assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
     }
     func TestWizardHandler_MissingSessionID_Returns400(t *testing.T) {
         h := NewChatHandler(&mockAdapterClient{}, noopRunner())
         body := `{"wizard":"course_creation"}`
         req := httptest.NewRequest(http.MethodPost, "/student/wizard", strings.NewReader(body))
         req.Header.Set("Content-Type", "application/json")
         w := httptest.NewRecorder()
         h.ServeHTTP(w, req)
         assert.Equal(t, http.StatusBadRequest, w.Code)
     }
     func TestWizardHandler_UnknownWizard_Returns400(t *testing.T) {
         mockRunner := &mockWizardRunner{handleFn: func(_ context.Context, _ wizardpkg.WizardRequest) (wizardpkg.WizardResponse, error) {
             return wizardpkg.WizardResponse{}, wizardpkg.ErrUnknownWizard
         }}
         h := NewChatHandler(&mockAdapterClient{}, mockRunner)
         body := `{"session_id":"s1","wizard":"bad_wizard"}`
         req := httptest.NewRequest(http.MethodPost, "/student/wizard", strings.NewReader(body))
         req.Header.Set("Content-Type", "application/json")
         w := httptest.NewRecorder()
         h.ServeHTTP(w, req)
         assert.Equal(t, http.StatusBadRequest, w.Code)
     }
     func TestWizardHandler_SessionNotFound_Returns404(t *testing.T) {
         // mockRunner returns ErrSessionNotFound → expect 404
     }
     func TestWizardHandler_Init_ReturnsFirstPrompt(t *testing.T) {
         // Use real WizardEngine with DefaultRegistry and noopLookup
         store := wizardpkg.NewMemorySessionStore()
         lookup := func(_ context.Context, q string) (string, error) { return "ok", nil }
         engine := wizardpkg.NewWizardEngine(store, wizardpkg.DefaultRegistry(), lookup)
         h := NewChatHandler(&mockAdapterClient{}, engine)
         body := `{"session_id":"s1","wizard":"course_creation"}`
         req := httptest.NewRequest(http.MethodPost, "/student/wizard", strings.NewReader(body))
         req.Header.Set("Content-Type", "application/json")
         w := httptest.NewRecorder()
         h.ServeHTTP(w, req)
         assert.Equal(t, http.StatusOK, w.Code)
         var resp wizardpkg.WizardResponse
         json.Unmarshal(w.Body.Bytes(), &resp)
         assert.Equal(t, "subject_code", resp.NextStep)
     }
     ```
  Also add `mockWizardRunner` struct to `handlers_test.go` (mirrors `mockAdapterClient` pattern):
  ```go
  type mockWizardRunner struct {
      handleFn func(ctx context.Context, req wizardpkg.WizardRequest) (wizardpkg.WizardResponse, error)
  }
  func (m *mockWizardRunner) Handle(ctx context.Context, req wizardpkg.WizardRequest) (wizardpkg.WizardResponse, error) {
      if m.handleFn != nil { return m.handleFn(ctx, req) }
      return wizardpkg.WizardResponse{}, nil
  }
  ```
  2. **GREEN** — implement `wizardHandler`, update `NewChatHandler`, fix all call sites.
  3. **REFACTOR** — check that all pre-existing `/chat/*` tests remain green with the `noopRunner()` change.

  Run: `go test ./... -v`

- [ ] **F.7 — Wire WizardEngine in `cmd/server/main.go`**

  **Prompt for implementer:**
  In `cmd/server/main.go` (or wherever `NewChatHandler` is called in production wiring),
  construct the wizard engine and pass it to the handler.

  ```go
  // After creating AdapterClient ac:
  lookupFn := func(ctx context.Context, q string) (string, error) {
      resp, err := ac.AskBannerGuide(ctx, q, "student")
      if err != nil { return "", err }
      return resp.Answer, nil
  }
  engine := wizard.NewWizardEngine(wizard.NewMemorySessionStore(), wizard.DefaultRegistry(), lookupFn)
  handler := api.NewChatHandler(ac, engine)
  ```

  TDD: No new tests needed — the handler tests in F.6 cover this. Manually verify that
  `go build ./...` succeeds after wiring (catch any import cycle or missing interface method).

  Run: `go build ./...` then `go test ./... -v`

- [ ] **F.8 — Update `CLAUDE.md` with `/student/wizard` documentation**

  **Prompt for implementer:**
  In `CLAUDE.md`, under "Backend API":
  ```
  POST /student/wizard  { session_id (required), wizard?, step?, value?, question? }
  Returns wizard.WizardResponse: { valid?, next_step?, prompt, options?, answer?, resume_step?, review?, session }
  Init: send session_id + wizard (step/value/question all empty) → receives first prompt
  Submit: send step + value → validated and advanced; or step + question → escape hatch lookup
  Review: next_step="review"; send value="confirm"|"restart"|"edit:<field>" to resolve
  Escape hatch: sends question to AskBannerGuide(student) → returns answer + resume_step
  ```

  Under "This repo":
  ```
  internal/wizard    → wizard engine: MemorySessionStore, Registry, StepDefinition, WizardEngine
  ```

  Under "Non-negotiable rules":
  ```
  wizardHandler maps ErrUnknownWizard → 400, ErrSessionNotFound → 404; never leaks engine details
  NewChatHandler(client AdapterClient, runner WizardRunner) — both parameters required
  ```

---

### Phase F Acceptance Criteria

- [ ] `go test ./... -v` passes with 0 failures
- [ ] `POST /student/wizard {session_id:"s1", wizard:"course_creation"}` → 200, `next_step:"subject_code"`
- [ ] `POST /student/wizard {session_id:"s1", step:"subject_code", value:"comp"}` → 200, `valid:false`, reprompt visible
- [ ] `POST /student/wizard {session_id:"s1", step:"subject_code", value:"COMP"}` → 200, `valid:true`, `next_step:"course_number"`
- [ ] `POST /student/wizard {session_id:"s1", step:"subject_code", question:"What format?"}` → 200, `answer` non-empty, `resume_step:"subject_code"`
- [ ] `POST /student/wizard {session_id:"s1", wizard:"nonexistent"}` → 400
- [ ] `POST /student/wizard {session_id:"ghost", step:"subject_code", value:"COMP"}` → 404
- [ ] All field validators tested: ≥3 valid inputs + ≥2 invalid inputs per validator
- [ ] `driveToReview` helper drives all 14 CourseCreation steps and arrives at review node
- [ ] `go build ./...` succeeds (no import cycles)
- [ ] CLAUDE.md documents the `/student/wizard` endpoint

---

## Phase G — Agents 11 & 12: Wizard Conductor and Test Harness

**Goal:** Document two new Claude agents for the Banner Student wizard system in `wiki/CLAUDE_AGENTS.md`.
No Go code changes. These agents operate against the `/student/wizard` adapter endpoint built in Phase F.

### Tasks

- [ ] **G.1 — Document Agent 11 (Banner Student Wizard Conductor) in `wiki/CLAUDE_AGENTS.md`**

  **Prompt for implementer:**
  Append Agent 11 to `wiki/CLAUDE_AGENTS.md` before the Tool Reference section.
  Agent 11 is the Botpress-facing wizard conductor: it receives a user message, identifies
  which wizard to launch (or continues the active session), and calls `POST /student/wizard`
  once per turn to drive the field-collection conversation.

  Agent 11 must document all of the following:

  **Tool:** `wizard_step` wrapping `POST /student/wizard`
  - Inputs: `session_id`, `wizard` (init only), `step`, `value`, `question`
  - Output: `WizardResponse` — the agent reads `next_step`, `prompt`, `options`, `answer`, `review`

  **Wizard trigger phrases** (these launch a specific wizard):
  | Phrase | Wizard |
  |---|---|
  | "create a course", "new course", "add a course", "SCACRSE" | `course_creation` |
  | (Phase 2–4 wizards added here as they are built) | |

  **System prompt must cover:**
  - On first user message: detect wizard intent from trigger phrase → init session → present first prompt
  - On subsequent messages: detect question vs field answer, populate either `question` or `value`
  - Question detection (client-side, before calling wizard_step):
    - Ends with `?` → use `question` field
    - Starts with "what", "how", "why", "when", "where", "can", "is", "are" → use `question` field
    - Otherwise → use `value` field
  - For picklist fields (response includes `options`): present as Quick Reply buttons in Botpress
  - For review node (`next_step == "review"`): show the review summary; accept "confirm", "restart", or "edit <field>"
  - If response is 404 (session not found): tell the user their session expired and offer to restart
  - Never answer Banner questions from prior training knowledge — always route through `wizard_step`

  **Session management:**
  - Generate `session_id = uuid()` once at conversation start
  - Reuse same `session_id` for every turn in the conversation

  **Implementation:** Python using Anthropic SDK tool-use loop, model `claude-haiku-4-5`.

  **Example session:** Show a full course_creation wizard from "I want to create a course"
  through subject code, course number, one escape hatch question, picklist selection,
  review display, and final confirmation.

- [ ] **G.2 — Document Agent 12 (Wizard Test Harness Agent) in `wiki/CLAUDE_AGENTS.md`**

  **Prompt for implementer:**
  Append Agent 12 to `wiki/CLAUDE_AGENTS.md` after Agent 11, before the Tool Reference section.
  Agent 12 is a developer-facing QA agent: no human in the loop. Given a wizard name, it
  drives the full golden path with valid synthetic inputs, then retests each step with a
  deliberate invalid input to confirm all validation rules fire correctly.

  Agent 12 must document all of the following:

  **Tool:** `wizard_step` (same tool as Agent 11, shared `run_tool` dispatcher)

  **System prompt must specify:**
  - Accept a wizard name and the step sequence as context
  - Golden path pass: submit a known-valid value for each step in sequence; assert `valid:true` each time
  - Error path pass: for each step that has a `Validate` function or `Options` list, submit one
    known-invalid value; assert `valid:false` and an error message in `prompt`
  - Skip optional steps (Required=false) in error path — they don't have hard validation
  - Output a Markdown table after each pass:
    ```
    | Step | Input | Expected | valid | prompt_contains | PASS/FAIL |
    |------|-------|----------|-------|-----------------|-----------|
    ```
  - Final summary: total steps tested, pass count, fail count, any unexpected valid:true on invalid input (critical failure)

  **Known test values** (embed in system prompt context for `course_creation`):
  | Step | Valid input | Invalid input | Why invalid |
  |---|---|---|---|
  | subject_code | COMP | comp | lowercase |
  | course_number | 3850 | (empty) | empty not allowed |
  | short_title | Software Engineering | (31+ chars) | exceeds max length |
  | credit_hours | 3 | 0 | zero not allowed |
  | course_level | UG | GRADUATE | not in options list |
  | effective_term | 202510 | 202540 | invalid term suffix |
  | (etc. for all steps) | | | |

  **Implementation:** Python using Anthropic SDK. Model `claude-haiku-4-5`. Not interactive —
  runs to completion and returns the full test report as a string.

  **Example output:** Show the Markdown table for course_creation with golden path + error path
  results, followed by a summary line like "14/14 golden path PASS, 8/8 error path PASS".

  **Use case note:** Run this agent as a pre-merge check whenever a new wizard step sequence
  is added to `internal/wizard/catalog.go`.

- [ ] **G.3 — Update TOC and tables in `wiki/CLAUDE_AGENTS.md`**

  **Prompt for implementer:**
  1. Add to Table of Contents (after item 11 — Agent 10):
     ```
     12. [Agent 11: Banner Student Wizard Conductor](#agent-11-banner-student-wizard-conductor)
     13. [Agent 12: Wizard Test Harness Agent](#agent-12-wizard-test-harness-agent)
     ```
  2. Add to Tool Reference table:
     ```
     | `wizard_step` | POST | `/student/wizard` | 11, 12 |
     ```
  3. Add to Cost Control table (under Implementation Notes):
     ```
     | Banner Student Wizard Conductor (Agent 11) | `claude-haiku-4-5` | < $0.002 per turn |
     | Wizard Test Harness (Agent 12) | `claude-haiku-4-5` | < $0.01 per wizard run |
     ```
  4. Add to Priority Recommendation section:
     ```
     **Wizard system (after Phase F is deployed):**
     - **Agent 11 — Wizard Conductor**: The Botpress integration point for all wizard sessions
     - **Agent 12 — Test Harness**: Run before deploying any new wizard step sequence
     ```
  5. Update the existing `agents/` project structure in Implementation Notes to include:
     ```
     ├── wizard_conductor.py  # Agent 11
     ├── wizard_harness.py    # Agent 12
     ```

---

### Phase G Acceptance Criteria

- [ ] Agent 11 fully documented: tool schema, system prompt, Python implementation, full example session
- [ ] Agent 12 fully documented: tool schema, system prompt with test values table, example report output
- [ ] TOC updated to 13 items
- [ ] Tool Reference table includes `wizard_step`
- [ ] Cost Control table includes Agents 11 and 12
- [ ] Priority section references wizard agents
- [ ] `agents/` project structure in Implementation Notes updated

---

## Phase H — AI Query Rewriting (TDD)

**Goal:** Port Phase 5A query rewriting from the Java AI-ROADMAP to Go. Before routing user
questions to citesearch, use an LLM to expand ambiguous queries with Banner-specific synonyms
and expanded acronyms. Improves retrieval for terse questions ("GL changes", "9.3.37 notes").
Also applied to the wizard escape hatch so mid-step clarifying questions retrieve better answers.

**Disabled by default** — set `ENABLE_QUERY_REWRITE=true` to activate. Falls back silently
to the original question on any error or when disabled.

**Package structure (new):**
```
internal/rewrite/
├── rewriter.go      → Rewriter interface + NoopRewriter + LLMRewriter + MockRewriter
└── rewriter_test.go
```

**Handler change:** `NewChatHandler` gains a third parameter: `rewriter rewritepkg.Rewriter`.

**Wizard change:** `LookupFn` gains a `step string` parameter so the rewriter can use the
current wizard step as context when expanding escape hatch questions.

---

### Tasks

- [ ] **H.1 — Define `Rewriter` interface and `NoopRewriter` in `internal/rewrite/rewriter.go`**

  **Prompt for implementer:**
  Create `internal/rewrite/rewriter.go` with types and the no-op implementation only.
  Do not add LLM logic yet.

  ```go
  package rewrite

  import "context"

  type RewriteRequest struct {
      Question string
      Intent   string // e.g. BannerRelease, SopQuery — informs vocabulary
      Context  string // optional: wizard step name for escape hatch queries
  }

  type RewriteResult struct {
      Original  string
      Rewritten string
      Changed   bool
  }

  type Rewriter interface {
      Rewrite(ctx context.Context, req RewriteRequest) (RewriteResult, error)
  }

  type NoopRewriter struct{}

  func (n *NoopRewriter) Rewrite(_ context.Context, req RewriteRequest) (RewriteResult, error) {
      return RewriteResult{Original: req.Question, Rewritten: req.Question, Changed: false}, nil
  }

  // MockRewriter is exported for use in other packages' tests.
  type MockRewriter struct {
      RewriteFn func(context.Context, RewriteRequest) (RewriteResult, error)
  }
  func (m *MockRewriter) Rewrite(ctx context.Context, req RewriteRequest) (RewriteResult, error) {
      if m.RewriteFn != nil { return m.RewriteFn(ctx, req) }
      return RewriteResult{Original: req.Question, Rewritten: req.Question}, nil
  }
  ```

  TDD in `internal/rewrite/rewriter_test.go`:
  1. **RED**:
     ```go
     func TestNoopRewriter_ReturnsOriginalUnchanged(t *testing.T) {
         r := &NoopRewriter{}
         result, err := r.Rewrite(context.Background(), RewriteRequest{Question: "What changed?", Intent: "BannerRelease"})
         require.NoError(t, err)
         assert.Equal(t, "What changed?", result.Rewritten)
         assert.False(t, result.Changed)
     }
     func TestMockRewriter_CallsRewriteFn(t *testing.T) {
         r := &MockRewriter{RewriteFn: func(_ context.Context, req RewriteRequest) (RewriteResult, error) {
             return RewriteResult{Original: req.Question, Rewritten: "expanded: " + req.Question, Changed: true}, nil
         }}
         result, err := r.Rewrite(context.Background(), RewriteRequest{Question: "GL changes"})
         require.NoError(t, err)
         assert.True(t, result.Changed)
     }
     ```
  2. **GREEN** — types above make these pass immediately.
  3. **REFACTOR** — none.

  Run: `go test ./internal/rewrite/... -v`

- [ ] **H.2 — Implement `LLMRewriter` with httptest-driven TDD**

  **Prompt for implementer:**
  Add `LLMRewriter` to `internal/rewrite/rewriter.go`. Uses raw `net/http` with Anthropic
  Messages API format (same pattern as `internal/adapter/client.go`).

  ```go
  type LLMRewriter struct {
      endpoint string
      apiKey   string
      model    string
      client   *http.Client
  }

  // NewLLMRewriter reads env vars. Returns *NoopRewriter when ENABLE_QUERY_REWRITE != "true".
  func NewLLMRewriter() Rewriter {
      if os.Getenv("ENABLE_QUERY_REWRITE") != "true" {
          return &NoopRewriter{}
      }
      return &LLMRewriter{
          endpoint: firstNonEmpty(os.Getenv("REWRITE_ENDPOINT"), "https://api.anthropic.com/v1/messages"),
          apiKey:   os.Getenv("REWRITE_API_KEY"),
          model:    firstNonEmpty(os.Getenv("REWRITE_MODEL"), "claude-haiku-4-5-20251001"),
          client:   &http.Client{Timeout: 5 * time.Second},
      }
  }

  func firstNonEmpty(a, b string) string { if a != "" { return a }; return b }
  ```

  **System prompt for the LLM:**
  ```
  You are a query expansion assistant for an Ellucian Banner ERP knowledge base.
  Given a user question and its intent, rewrite it to be more specific and retrievable.
  - Expand acronyms: GL→General Ledger, AR→Accounts Receivable, AP→Accounts Payable
  - Add Banner ERP context if the query is too terse
  - Keep the rewrite under 2 sentences
  - If the question is already clear, return it unchanged
  - Respond with ONLY the rewritten question. No explanation.
  ```

  **On any error** (HTTP, parse, timeout): return original question, `Changed: false`, `err: nil`.

  TDD (add to `internal/rewrite/rewriter_test.go`):
  ```go
  func TestLLMRewriter_ExpandsQuestion(t *testing.T) {
      srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          json.NewEncoder(w).Encode(map[string]any{
              "content": []map[string]any{{"type":"text","text":"What changed in the Banner Finance General Ledger module?"}},
          })
      }))
      defer srv.Close()
      r := &LLMRewriter{endpoint: srv.URL, apiKey: "test", model: "test", client: &http.Client{}}
      result, err := r.Rewrite(context.Background(), RewriteRequest{Question: "GL changes", Intent: "BannerFinance"})
      require.NoError(t, err)
      assert.True(t, result.Changed)
      assert.Contains(t, result.Rewritten, "General Ledger")
      assert.Equal(t, "GL changes", result.Original)
  }
  func TestLLMRewriter_FallsBackSilentlyOnHTTPError(t *testing.T) {
      srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          w.WriteHeader(http.StatusInternalServerError)
      }))
      defer srv.Close()
      r := &LLMRewriter{endpoint: srv.URL, apiKey: "test", model: "test", client: &http.Client{}}
      result, err := r.Rewrite(context.Background(), RewriteRequest{Question: "What changed?"})
      require.NoError(t, err, "LLMRewriter must not propagate errors")
      assert.Equal(t, "What changed?", result.Rewritten)
      assert.False(t, result.Changed)
  }
  func TestLLMRewriter_SameTextIsNotChanged(t *testing.T) {
      srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          json.NewEncoder(w).Encode(map[string]any{
              "content": []map[string]any{{"type":"text","text":"What changed?"}},
          })
      }))
      defer srv.Close()
      r := &LLMRewriter{endpoint: srv.URL, apiKey: "test", model: "test", client: &http.Client{}}
      result, _ := r.Rewrite(context.Background(), RewriteRequest{Question: "What changed?"})
      assert.False(t, result.Changed)
  }
  func TestNewLLMRewriter_ReturnsNoopWhenDisabled(t *testing.T) {
      t.Setenv("ENABLE_QUERY_REWRITE", "")
      r := NewLLMRewriter()
      _, ok := r.(*NoopRewriter)
      assert.True(t, ok)
  }
  ```

  Run: `go test ./internal/rewrite/... -v`

- [ ] **H.3 — Wire `Rewriter` into `/chat/ask` handler**

  **Prompt for implementer:**
  In `api/handlers.go`:
  1. Import `citesearch/internal/rewrite` as `rewritepkg`.
  2. Add `rewriter rewritepkg.Rewriter` field to `ChatHandler`.
  3. Update `NewChatHandler` to 3 parameters: `(client AdapterClient, runner WizardRunner, rewriter rewritepkg.Rewriter)`.
  4. In `askHandler`, after detecting intent but before backend routing:
     ```go
     rewriteResult, _ := rewriter.Rewrite(r.Context(), rewritepkg.RewriteRequest{
         Question: req.Message,
         Intent:   string(detectedIntent),
     })
     effectiveQuestion := rewriteResult.Rewritten
     ```
     Replace all uses of `req.Message` in backend routing calls with `effectiveQuestion`.
  5. Update all `NewChatHandler(mockClient, noopRunner())` call sites in tests to:
     `NewChatHandler(mockClient, noopRunner(), &rewritepkg.NoopRewriter{})`.

  Add test:
  ```go
  func TestChatAsk_RewriterReceivesIntentAndQuestion(t *testing.T) {
      var capturedReq rewritepkg.RewriteRequest
      mockRewriter := &rewritepkg.MockRewriter{
          RewriteFn: func(_ context.Context, req rewritepkg.RewriteRequest) (rewritepkg.RewriteResult, error) {
              capturedReq = req
              return rewritepkg.RewriteResult{Original: req.Question, Rewritten: "expanded: " + req.Question, Changed: true}, nil
          },
      }
      mockClient := &mockAdapterClient{
          bannerAskFn: func(_ context.Context, q string, _ adapter.AskOptions) (adapter.AdapterResponse, error) {
              assert.Contains(t, q, "expanded:")
              return adapter.AdapterResponse{Answer: "ok", RetrievalCount: 1, Sources: []adapter.Source{{Score: 0.033}}}, nil
          },
      }
      h := NewChatHandler(mockClient, noopRunner(), mockRewriter)
      body := `{"message":"GL changes","source":"banner"}`
      req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
      req.Header.Set("Content-Type", "application/json")
      w := httptest.NewRecorder()
      h.ServeHTTP(w, req)
      assert.Equal(t, http.StatusOK, w.Code)
      assert.Equal(t, "GL changes", capturedReq.Question)
  }
  ```

  Run: `go test ./... -v`

- [ ] **H.4 — Update wizard `LookupFn` to pass step name**

  **Prompt for implementer:**
  In `internal/wizard/engine.go`, update `LookupFn`:
  ```go
  // LookupFn is called for escape hatch questions. step is the current wizard step name.
  type LookupFn func(ctx context.Context, question string, step string) (string, error)
  ```
  In the escape hatch case, pass `session.CurrentStep` as the step argument.

  Update `newTestEngine` in `internal/wizard/engine_test.go`:
  ```go
  lookup := func(_ context.Context, q string, _ string) (string, error) {
      return "lookup answer: " + q, nil
  }
  ```

  Add one new test:
  ```go
  func TestEngine_EscapeHatch_PassesStepToLookup(t *testing.T) {
      var capturedStep string
      store := NewMemorySessionStore()
      lookup := func(_ context.Context, q string, step string) (string, error) {
          capturedStep = step
          return "answer", nil
      }
      engine := NewWizardEngine(store, DefaultRegistry(), lookup)
      engine.Handle(context.Background(), WizardRequest{SessionID: "s1", Wizard: "course_creation"})
      engine.Handle(context.Background(), WizardRequest{
          SessionID: "s1", Step: "subject_code", Question: "What format?",
      })
      assert.Equal(t, "subject_code", capturedStep)
  }
  ```

  Update `cmd/server/main.go` `lookupFn` to accept `step string` and pass it to the rewriter:
  ```go
  lookupFn := func(ctx context.Context, q string, step string) (string, error) {
      rewriteResult, _ := r.Rewrite(ctx, rewrite.RewriteRequest{
          Question: q, Intent: "BannerUsage", Context: step,
      })
      resp, err := ac.AskBannerGuide(ctx, rewriteResult.Rewritten, "student")
      if err != nil { return "", err }
      return resp.Answer, nil
  }
  ```

  Run: `go test ./... -v`

- [ ] **H.5 — Wire `NewLLMRewriter` in `cmd/server/main.go` and update `CLAUDE.md`**

  **Prompt for implementer:**
  In `cmd/server/main.go`:
  ```go
  r := rewrite.NewLLMRewriter()
  engine := wizard.NewWizardEngine(wizard.NewMemorySessionStore(), wizard.DefaultRegistry(), lookupFn)
  handler := api.NewChatHandler(ac, engine, r)
  ```
  In `CLAUDE.md`:
  - Add `internal/rewrite` to repo structure
  - Update `NewChatHandler` to 3-parameter form
  - Add `LookupFn: func(ctx, question, step string) (string, error)`
  - Env vars: `ENABLE_QUERY_REWRITE`, `REWRITE_ENDPOINT`, `REWRITE_API_KEY`, `REWRITE_MODEL`

  Run: `go build ./...` then `go test ./... -v`

---

### Phase H Acceptance Criteria

- [ ] `go test ./... -v` passes with 0 failures
- [ ] `ENABLE_QUERY_REWRITE=""` → `NewLLMRewriter()` returns `*NoopRewriter`
- [ ] `LLMRewriter` on HTTP 500 → returns original question, `err == nil`
- [ ] `/chat/ask` passes the rewritten question to the backend when `MockRewriter` is wired
- [ ] Wizard escape hatch passes step name to `LookupFn`
- [ ] `go build ./...` succeeds

---

## Phase I — Answer Grounding Verification (TDD)

**Goal:** Port Phase 5B from the Java AI-ROADMAP. After citesearch returns an answer, optionally
call Claude to verify the answer is supported by the retrieved source chunks. Returns a grounding
verdict in `/chat/ask` response. Disabled by default (`ENABLE_GROUNDING=true` to activate).

**Package structure (new):**
```
internal/ground/
├── checker.go      → GroundingChecker interface + NoopChecker + LLMChecker + MockChecker
└── checker_test.go
```

**Response change:** `/chat/ask` gains an optional `grounding` field (omitted when Noop):
```json
{ "answer":"...", "confidence":0.033, "escalate":false,
  "grounding":{"verdict":"supported","note":""} }
```

**Handler change:** `NewChatHandler` gains a fourth parameter: `checker groundpkg.GroundingChecker`.

---

### Tasks

- [ ] **I.1 — Define `GroundingChecker` interface and types**

  **Prompt for implementer:**
  Create `internal/ground/checker.go`:

  ```go
  package ground

  import "context"

  type Verdict string
  const (
      VerdictSupported   Verdict = "supported"
      VerdictPartial     Verdict = "partial"
      VerdictUnsupported Verdict = "unsupported"
      VerdictSkipped     Verdict = "skipped"
  )

  type GroundingRequest struct {
      Question string
      Answer   string
      Sources  []string // text of source chunks in retrieval order
  }

  type GroundingResult struct {
      Verdict Verdict
      Note    string // explanation for Partial/Unsupported; empty otherwise
  }

  type GroundingChecker interface {
      Check(ctx context.Context, req GroundingRequest) (GroundingResult, error)
  }

  type NoopChecker struct{}
  func (n *NoopChecker) Check(_ context.Context, _ GroundingRequest) (GroundingResult, error) {
      return GroundingResult{Verdict: VerdictSkipped}, nil
  }

  type MockChecker struct {
      CheckFn func(context.Context, GroundingRequest) (GroundingResult, error)
  }
  func (m *MockChecker) Check(ctx context.Context, req GroundingRequest) (GroundingResult, error) {
      if m.CheckFn != nil { return m.CheckFn(ctx, req) }
      return GroundingResult{Verdict: VerdictSkipped}, nil
  }
  ```

  TDD in `internal/ground/checker_test.go`:
  1. **RED**:
     ```go
     func TestNoopChecker_ReturnsSkipped(t *testing.T) {
         c := &NoopChecker{}
         result, err := c.Check(context.Background(), GroundingRequest{Question:"q",Answer:"a"})
         require.NoError(t, err)
         assert.Equal(t, VerdictSkipped, result.Verdict)
         assert.Empty(t, result.Note)
     }
     ```
  2. **GREEN** — types make this pass.

  Run: `go test ./internal/ground/... -v`

- [ ] **I.2 — Implement `LLMChecker` with httptest TDD**

  **Prompt for implementer:**
  Add `LLMChecker` to `internal/ground/checker.go`. Same raw `net/http` pattern.
  `NewLLMChecker()` reads `ENABLE_GROUNDING`, `GROUNDING_ENDPOINT`, `GROUNDING_API_KEY`,
  `GROUNDING_MODEL`. Returns `*NoopChecker` when disabled.

  **System prompt:**
  ```
  You are a grounding verifier for a Banner ERP knowledge base chatbot.
  Given a question, an answer, and source document chunks, determine if the answer is supported.
  Respond with exactly ONE of:
  - supported
  - partial:<short reason>
  - unsupported:<short reason>
  Do not add any other text.
  ```

  Parse the response: split on ":" — first token is the verdict, remainder is the note.
  On any error: return `VerdictSkipped`, `err: nil`.

  TDD:
  ```go
  func TestLLMChecker_Supported(t *testing.T) { /* server returns "supported" → VerdictSupported, empty note */ }
  func TestLLMChecker_Partial(t *testing.T) { /* "partial:missing details" → VerdictPartial, note="missing details" */ }
  func TestLLMChecker_Unsupported(t *testing.T) { /* "unsupported:contradicts source" → VerdictUnsupported */ }
  func TestLLMChecker_FallsBackOnError(t *testing.T) { /* 500 → VerdictSkipped, err==nil */ }
  func TestNewLLMChecker_NoopWhenDisabled(t *testing.T) { /* ENABLE_GROUNDING="" → *NoopChecker */ }
  ```

  Run: `go test ./internal/ground/... -v`

- [ ] **I.3 — Wire `GroundingChecker` into `/chat/ask` and update response type**

  **Prompt for implementer:**
  In `api/handlers.go`:
  1. Add `GroundingInfo` and update the ask response struct:
     ```go
     type GroundingInfo struct {
         Verdict string `json:"verdict"`
         Note    string `json:"note,omitempty"`
     }
     // Add to ChatAskResponse (or whatever struct the handler marshals):
     Grounding *GroundingInfo `json:"grounding,omitempty"`
     ```
  2. Update `NewChatHandler` to 4 parameters:
     `(client AdapterClient, runner WizardRunner, rewriter rewritepkg.Rewriter, checker groundpkg.GroundingChecker)`
  3. In `askHandler`, after getting a non-escalated `AdapterResponse`:
     ```go
     groundResult, _ := checker.Check(r.Context(), groundpkg.GroundingRequest{
         Question: effectiveQuestion,
         Answer:   resp.Answer,
         Sources:  extractSourceTexts(resp.Sources), // []string of source text
     })
     if groundResult.Verdict != groundpkg.VerdictSkipped {
         chatResp.Grounding = &GroundingInfo{Verdict: string(groundResult.Verdict), Note: groundResult.Note}
     }
     ```
  4. Update all `NewChatHandler` call sites to include `&ground.NoopChecker{}`.

  Add tests:
  ```go
  func TestChatAsk_GroundingIncluded_WhenPartial(t *testing.T) {
      // MockChecker returns VerdictPartial → resp.grounding.verdict == "partial"
  }
  func TestChatAsk_GroundingOmitted_WhenSkipped(t *testing.T) {
      // NoopChecker → "grounding" key absent from JSON response
  }
  ```

  Run: `go test ./... -v`

- [ ] **I.4 — Update `CLAUDE.md`**

  **Prompt for implementer:**
  - Add `internal/ground` to repo structure
  - Update `NewChatHandler` to 4-parameter form
  - Note: `grounding` field omitted from `/chat/ask` response when `ENABLE_GROUNDING != "true"`
  - Env vars: `ENABLE_GROUNDING`, `GROUNDING_ENDPOINT`, `GROUNDING_API_KEY`, `GROUNDING_MODEL`

---

### Phase I Acceptance Criteria

- [ ] `go test ./... -v` passes with 0 failures
- [ ] `ENABLE_GROUNDING=""` → `NewLLMChecker()` returns `*NoopChecker`
- [ ] `LLMChecker` on HTTP 500 → `VerdictSkipped`, `err == nil`
- [ ] `/chat/ask` response includes `grounding` when checker returns Partial/Unsupported
- [ ] `/chat/ask` response omits `grounding` field when `NoopChecker` is wired
- [ ] `go build ./...` succeeds

---

## Phase J — Ask Log & Analytics (TDD)

**Goal:** Port Phase 5E from the Java AI-ROADMAP. Log every `/chat/ask` invocation to NDJSON.
Expose `GET /analytics/summary` for cost and quality trend queries.
Disabled by default (`ASK_LOG_PATH` env var empty = no logging).

**Package structure (new):**
```
internal/analytics/
├── entry.go        → AskLogEntry struct
├── logger.go       → AskLogger interface + NdjsonLogger + NoopLogger + MockLogger
└── logger_test.go
```

**Handler change:** `NewChatHandler` gains a fifth parameter: `logger analytpkg.AskLogger`.

---

### Tasks

- [ ] **J.1 — Define `AskLogEntry` and `AskLogger` in `internal/analytics/`**

  **Prompt for implementer:**
  Create `internal/analytics/entry.go`:
  ```go
  package analytics

  import "time"

  type AskLogEntry struct {
      Timestamp  time.Time `json:"timestamp"`
      SessionID  string    `json:"session_id"`
      Intent     string    `json:"intent"`
      Source     string    `json:"source"`
      Confidence float64   `json:"confidence"`
      Escalated  bool      `json:"escalated"`
      Grounding  string    `json:"grounding,omitempty"`
      DurationMs int64     `json:"duration_ms"`
  }
  ```

  Create `internal/analytics/logger.go` with `AskLogger` interface, `NoopLogger`,
  `NdjsonLogger` (append one JSON line per entry), and `MockLogger` (exported for tests):
  ```go
  type AskLogger interface { Log(ctx context.Context, entry AskLogEntry) error }

  type NoopLogger struct{}
  func (n *NoopLogger) Log(_ context.Context, _ AskLogEntry) error { return nil }

  type NdjsonLogger struct { path string; mu sync.Mutex }
  func NewNdjsonLogger(path string) *NdjsonLogger { return &NdjsonLogger{path: path} }

  func NewDefaultLogger() AskLogger {
      if p := os.Getenv("ASK_LOG_PATH"); p != "" { return NewNdjsonLogger(p) }
      return &NoopLogger{}
  }

  type MockLogger struct { LogFn func(context.Context, AskLogEntry) error }
  func (m *MockLogger) Log(ctx context.Context, e AskLogEntry) error {
      if m.LogFn != nil { return m.LogFn(ctx, e) }
      return nil
  }
  ```

  `NdjsonLogger.Log`: open with `O_APPEND|O_CREATE|O_WRONLY`, write `json.Marshal(entry)+"\n"`, close.

  TDD in `internal/analytics/logger_test.go`:
  ```go
  func TestNdjsonLogger_WritesEntry(t *testing.T) { /* temp file, write entry, assert JSON content */ }
  func TestNdjsonLogger_AppendsMultiple(t *testing.T) { /* write 2, assert 2 lines */ }
  func TestNoopLogger_NoError(t *testing.T) { /* always nil */ }
  func TestNewDefaultLogger_ReturnsNoopWhenNoPath(t *testing.T) { /* ASK_LOG_PATH="" → *NoopLogger */ }
  func TestNewDefaultLogger_ReturnsNdjsonWhenPathSet(t *testing.T) { /* path set → *NdjsonLogger */ }
  ```

  Run: `go test ./internal/analytics/... -v`

- [ ] **J.2 — Wire `AskLogger` into `/chat/ask` — fire-and-forget goroutine**

  **Prompt for implementer:**
  Update `NewChatHandler` to 5 parameters. In `askHandler`, after sending the response:
  ```go
  start := time.Now()
  // ... existing code ...
  go func() {
      _ = logger.Log(r.Context(), analytpkg.AskLogEntry{
          Timestamp: time.Now(), SessionID: req.SessionID,
          Intent: string(detectedIntent), Source: source,
          Confidence: resp.Confidence, Escalated: resp.Escalate,
          Grounding: string(groundResult.Verdict),
          DurationMs: time.Since(start).Milliseconds(),
      })
  }()
  ```
  Logging must never block the response.

  Update all `NewChatHandler` call sites: add `&analytpkg.NoopLogger{}`.

  Add test:
  ```go
  func TestChatAsk_LogsEntry_Async(t *testing.T) {
      var logged analytpkg.AskLogEntry
      done := make(chan struct{})
      mockLogger := &analytpkg.MockLogger{LogFn: func(_ context.Context, e analytpkg.AskLogEntry) error {
          logged = e; close(done); return nil
      }}
      h := NewChatHandler(mockClient, noopRunner(), &rewritepkg.NoopRewriter{}, &ground.NoopChecker{}, mockLogger)
      // POST /chat/ask ...
      select { case <-done: case <-time.After(100*time.Millisecond): t.Fatal("log never called") }
      assert.Equal(t, "banner", logged.Source)
  }
  ```

  Run: `go test ./... -v`

- [ ] **J.3 — Implement `GET /analytics/summary` endpoint**

  **Prompt for implementer:**
  Register route `/analytics/summary` in `NewChatHandler`. Handler reads `ASK_LOG_PATH`,
  parses the NDJSON line-by-line, and returns:
  ```json
  {
    "total_asks": 100, "escalation_rate": 0.12, "avg_confidence": 0.028,
    "by_intent": {"BannerRelease":40}, "by_source": {"banner":58},
    "by_grounding": {"supported":60,"partial":20,"unsupported":5,"skipped":15}
  }
  ```
  If file missing or `ASK_LOG_PATH` empty: return `{"total_asks":0}` with 200.

  TDD:
  ```go
  func TestAnalyticsSummary_EmptyPath_Returns200(t *testing.T) { /* total_asks:0 */ }
  func TestAnalyticsSummary_WithData_ReturnsCorrectCounts(t *testing.T) {
      // write 3 entries to temp file, set ASK_LOG_PATH, GET /analytics/summary
      // assert total_asks=3, escalation_rate=0.333, by_intent.BannerRelease=2
  }
  ```

  Run: `go test ./... -v`

- [ ] **J.4 — Update `CLAUDE.md`**

  **Prompt for implementer:**
  - Add `internal/analytics` to repo structure
  - Add `GET /analytics/summary` to the endpoint table
  - Update `NewChatHandler` to 5-parameter form
  - Env var: `ASK_LOG_PATH` (empty = disabled)

---

### Phase J Acceptance Criteria

- [ ] `go test ./... -v` passes with 0 failures
- [ ] `ASK_LOG_PATH=""` → `NewDefaultLogger()` returns `*NoopLogger`
- [ ] Two `Log()` calls append two valid JSON lines to the NDJSON file
- [ ] `GET /analytics/summary` with empty path returns `{"total_asks":0}`
- [ ] `GET /analytics/summary` with 3 entries returns correct counts
- [ ] Logging goroutine does not block `/chat/ask` response
- [ ] `go build ./...` succeeds

---

## Phase K — Feedback Loop (TDD)

**Goal:** Port Phase 5F from the Java AI-ROADMAP. Add `POST /chat/feedback` for thumbs up/down
on chatbot answers. Store entries in NDJSON. Powers Agents 15 and 16 (analytics + feedback analyst).
`FEEDBACK_LOG_PATH` env var (empty = disabled).

**Package additions:** `FeedbackEntry` and `FeedbackLogger` in `internal/analytics/feedback.go`.

**Handler change:** `NewChatHandler` gains a sixth parameter: `fb analytpkg.FeedbackLogger`.

---

### Tasks

- [ ] **K.1 — Define `FeedbackEntry` and `FeedbackLogger` in `internal/analytics/feedback.go`**

  **Prompt for implementer:**
  Create `internal/analytics/feedback.go`:
  ```go
  package analytics

  type FeedbackEntry struct {
      Timestamp time.Time `json:"timestamp"`
      SessionID string    `json:"session_id"`
      MessageID string    `json:"message_id"`
      Helpful   bool      `json:"helpful"`
      Intent    string    `json:"intent,omitempty"`
      Source    string    `json:"source,omitempty"`
      Comment   string    `json:"comment,omitempty"`
  }

  type FeedbackLogger interface { LogFeedback(ctx context.Context, entry FeedbackEntry) error }

  type NoopFeedbackLogger struct{}
  func (n *NoopFeedbackLogger) LogFeedback(_ context.Context, _ FeedbackEntry) error { return nil }

  type NdjsonFeedbackLogger struct { path string; mu sync.Mutex }
  func NewNdjsonFeedbackLogger(path string) *NdjsonFeedbackLogger { return &NdjsonFeedbackLogger{path: path} }
  func NewDefaultFeedbackLogger() FeedbackLogger {
      if p := os.Getenv("FEEDBACK_LOG_PATH"); p != "" { return NewNdjsonFeedbackLogger(p) }
      return &NoopFeedbackLogger{}
  }

  type MockFeedbackLogger struct { LogFn func(context.Context, FeedbackEntry) error }
  func (m *MockFeedbackLogger) LogFeedback(ctx context.Context, e FeedbackEntry) error {
      if m.LogFn != nil { return m.LogFn(ctx, e) }
      return nil
  }
  ```
  Implement `NdjsonFeedbackLogger.LogFeedback` (same append pattern as `NdjsonLogger.Log`).

  TDD in `internal/analytics/feedback_test.go`:
  ```go
  func TestNdjsonFeedbackLogger_WritesEntry(t *testing.T) { /* temp file, write, assert JSON */ }
  func TestNoopFeedbackLogger_NoError(t *testing.T) {}
  func TestNewDefaultFeedbackLogger_NoopWhenEmpty(t *testing.T) {}
  ```

  Run: `go test ./internal/analytics/... -v`

- [ ] **K.2 — Add `POST /chat/feedback` handler**

  **Prompt for implementer:**
  Update `NewChatHandler` to 6 parameters. Register `/chat/feedback`.

  ```go
  type feedbackRequest struct {
      SessionID string `json:"session_id"`
      MessageID string `json:"message_id"`
      Helpful   bool   `json:"helpful"`
      Intent    string `json:"intent,omitempty"`
      Source    string `json:"source,omitempty"`
      Comment   string `json:"comment,omitempty"`
  }
  ```
  Validate `session_id` present (else 400). Write `FeedbackEntry`. Return `{"ok":true}`.

  Update all `NewChatHandler` call sites: add `&analytpkg.NoopFeedbackLogger{}`.

  TDD:
  ```go
  func TestFeedbackHandler_Helpful_Returns200(t *testing.T) {
      // MockFeedbackLogger captures entry; assert session_id and helpful==true
  }
  func TestFeedbackHandler_MissingSessionID_Returns400(t *testing.T) {}
  func TestFeedbackHandler_MethodNotAllowed(t *testing.T) { /* GET → 405 */ }
  ```

  Run: `go test ./... -v`

- [ ] **K.3 — Update `CLAUDE.md`**

  **Prompt for implementer:**
  - Add `POST /chat/feedback  {session_id,message_id,helpful,intent?,source?,comment?}` to endpoint table
  - Update `NewChatHandler` to 6-parameter form
  - Env var: `FEEDBACK_LOG_PATH` (empty = disabled)

---

### Phase K Acceptance Criteria

- [ ] `go test ./... -v` passes with 0 failures
- [ ] `POST /chat/feedback {session_id:"s1",helpful:true}` → 200 `{"ok":true}`, entry written
- [ ] `POST /chat/feedback {}` → 400 (missing session_id)
- [ ] `FEEDBACK_LOG_PATH=""` → `NewDefaultFeedbackLogger()` returns `*NoopFeedbackLogger`
- [ ] `go build ./...` succeeds

---

## Phase L — PDF Workflow Documentation & Tooling

**Goal:** Eliminate the operational blind spots in the ingestion pipeline. Today, operators have no
pre-flight validation, no time estimate, and no safe partial re-ingest path. A wrong folder name
silently produces unchunkable metadata; a scanned PDF produces garbage chunks that pollute retrieval.
Phase L closes these gaps through documentation, one new backend endpoint, and a new agent.

**No adapter changes.** This phase is documentation-first. The one coding task (L.3) touches the
citesearch backend only, not the adapter.

### Identified Pain Points

| # | Pain Point | Impact |
|---|---|---|
| 1 | Metadata is entirely path-derived with no validation | Wrong module/version silently indexed |
| 2 | No dry-run or preflight mode | 20-min ingest fails midway with no preview |
| 3 | Scanned PDF detection absent | Garbage chunks degrade retrieval quality |
| 4 | Finance user guide gap undocumented | API returns 400; no clear acquisition plan |
| 5 | Chunking strategy selection is implicit | Operators don't know why Finance guide might chunk badly |
| 6 | Re-ingest safety undocumented | `overwrite=true` silently deletes SOPs too |
| 7 | Student user guide exists in two folders | Confusing: `use/` vs dated folder |
| 8 | No ingestion time estimate | Large user guide ingests block operators for 60–90 min |
| 9 | No single naming/folder convention reference | Operators have to read Go source to understand rules |
| 10 | Orphan chunks after CHUNK_SIZE change | No cleanup path documented |

---

### Tasks

- [x] **L.1 — Create `wiki/INGEST.md`**

  **Status:** Done by 2026-04-23 brainstorm session.

  Created `wiki/INGEST.md` — the canonical operator reference for the ingest pipeline. Covers:
  - Canonical folder structure with module → path mapping table
  - File naming conventions for release notes, user guides, and SOPs
  - How metadata is extracted from file paths (module, version, year, source_type)
  - Supported file types and their processing paths
  - Chunking strategy selection per source_type (with heading regex documentation)
  - Source type tagging rules
  - Pre-ingest checklist (10 items)
  - Running an ingest (curl commands for all collections)
  - Re-ingest safety (three scenarios: same file, changed CHUNK_SIZE, failed midway)
  - Finance user guide gap — current status table + acquisition steps
  - Ingestion time estimates (table + calculation method)
  - Troubleshooting bad PDFs (6 symptom/fix entries)
  - How to add a new document collection (step-by-step checklist)

- [ ] **L.2 — Add `POST /banner/ingest` dry-run mode**

  **Prompt for implementer:**
  Add `dry_run` boolean to the ingest request body. When `dry_run=true`:
  1. Walk the `docs_path` and collect all files
  2. For each file, call `parseMetadata()` and detect source type
  3. For PDF files, open them and count pages using `pdf.Open()` — do NOT embed
  4. Estimate chunks: `ceil(pages * 500 / 400)` (rough average 400 chars/page)
  5. Return a preflight report — do NOT call Azure OpenAI or upload anything

  Response shape:
  ```json
  {
    "dry_run": true,
    "files": [
      {
        "path": "data/docs/banner/general/releases/2026/february/Banner_General_Release_Notes_9.3.37.2.pdf",
        "source_type": "banner",
        "module": "General",
        "version": "9.3.37.2",
        "year": "2026",
        "pages": 28,
        "estimated_chunks": 70,
        "estimated_seconds": 35,
        "warnings": []
      }
    ],
    "totals": {
      "files": 1,
      "pages": 28,
      "estimated_chunks": 70,
      "estimated_minutes": 1
    }
  }
  ```

  Warnings to include per file:
  - `"module not detected — check folder name"` if `meta.module == ""`
  - `"version not detected — check filename"` if `meta.version == ""` and source_type is `banner` (not user guide)
  - `"year not detected — check folder path"` if `meta.year == ""` and source_type is `banner`
  - `"SOP naming mismatch — file will be skipped"` if file is in `/sop/` but doesn't match SOP filename convention
  - `"no pages extracted — may be scanned PDF"` if PDF opens but returns 0 text pages

  TDD sequence:
  1. **RED** — write tests in `internal/ingest/ingest_test.go`:
     - `TestDryRun_ReturnsFileList_NoEmbedCalls`
     - `TestDryRun_DetectsModuleFromPath`
     - `TestDryRun_WarnsMissingModule`
     - `TestDryRun_WarnsSopNamingMismatch`
  2. **GREEN** — implement `dryRunReport()` function and wire into the handler
  3. **REFACTOR** — extract `estimateChunks()` helper (also useful for L.3)

  Run: `go test ./internal/ingest/... -v`

- [ ] **L.3 — Document Agent 17 (PDF Preflight Auditor) in `wiki/CLAUDE_AGENTS.md`**

  **Prompt for implementer:**
  Append Agent 17 to `wiki/CLAUDE_AGENTS.md`. See the Agent 17 design added in this phase.

  Agent 17 drives the dry-run endpoint (L.2) and cross-references with the live index to report:
  - Files on disk that are not yet indexed
  - Files with metadata warnings
  - Estimated time for a full ingest of pending files
  - Files that appear to be scanned (0 pages extracted)

  The agent concludes with a ranked action list: which files to ingest first and in what order.

  Add to TOC, Tool Reference table, Cost Control table, and Priority Recommendation section.

- [ ] **L.4 — Update `wiki/INTERNALS.md` PDF extraction limitations**

  **Prompt for implementer:**
  In `wiki/INTERNALS.md` § Known Limitations #1 (PDF Text Extraction Quality), expand with:
  - A concrete detection heuristic (file size per page check: <30KB/page = likely scanned)
  - Steps to use Azure AI Document Intelligence Layout API as a pre-processing step
  - A note that `dry_run=true` (Phase L.2) will surface scanned PDFs before a live ingest

- [ ] **L.5 — Add `wiki/INGEST.md` link to `wiki/RUNBOOK.md`**

  **Prompt for implementer:**
  In `wiki/RUNBOOK.md`, add a pointer to `wiki/INGEST.md` in the "One-time setup" section:

  ```markdown
  ### 4. Ingest documents (first time only)
  See [INGEST.md](INGEST.md) for the full reference — folder conventions, naming rules,
  pre-ingest checklist, and time estimates.
  ```

  Also add a short "Document ingestion" entry to the troubleshooting table at the bottom
  (if one exists) linking to INGEST.md § Troubleshooting Bad PDFs.

---

### Phase L Acceptance Criteria

- [x] `wiki/INGEST.md` exists with all sections listed in L.1
- [ ] `POST /banner/ingest` with `dry_run=true` returns a file list with metadata and warnings
- [ ] Dry-run does not call Azure OpenAI or upload anything
- [x] `wiki/CLAUDE_AGENTS.md` documents Agent 17 (PDF Preflight Auditor)
- [x] `wiki/INTERNALS.md` Known Limitations #1 includes scanned PDF detection heuristic
- [x] `wiki/RUNBOOK.md` links to `wiki/INGEST.md`
- [x] `wiki/INGEST.md` is referenced from the RUNBOOK

---

## Phase M — Upload-Based Ingest (No Filesystem Access Required)

**Goal:** Allow operators to ingest PDFs without SSH or filesystem access to the server.
Today, all three ingest entry points require the PDF to already be on the server's disk or in
Azure Blob Storage. In cloud deployments (Fly.io, remote Docker) this blocks any ad-hoc document
update — adding a new Finance release note requires a full redeploy.

**Core design principle:** The upload handler synthesizes the canonical folder path from the
supplied metadata (`module`, `source_type`, `year`), writes the file there, then calls the
existing `ingest.Run()` function. The ingest pipeline sees no difference between a manually
placed file and an uploaded one — `parseMetadata()` still works from the path.

**No adapter changes.** `POST /banner/upload` and `POST /banner/upload/from-url` are added to
the citesearch backend only (Gin server, `internal/api/handlers.go`).

---

### Context: existing ingest paths

| Path | Endpoint | What it requires |
|---|---|---|
| Folder-based | `POST /banner/ingest` | Files already in `data/docs/` on server disk |
| Azure Blob sync | `POST /banner/blob/sync` | PDFs uploaded to Azure Blob Container separately |
| Direct upload | `POST /banner/upload` **(Phase M.2)** | Nothing — HTTP multipart form from anywhere |
| URL-based | `POST /banner/upload/from-url` **(Phase M.4)** | A downloadable HTTPS URL |

**Blob sync limitation:** Always ingests all of `data/docs/banner/` after download, not a
single targeted file. The upload endpoint is targeted — one file, one ingest call.

---

### Tasks

- [x] **M.1 — Update `wiki/INGEST.md` with Upload Workflow section**

  **Status:** Done by 2026-04-23 session.

  Added "Upload Workflow — Ingest Without Filesystem Access" section to `wiki/INGEST.md`:
  - Three-path comparison table (folder, blob sync, upload, URL)
  - Path synthesis rules (how upload metadata → canonical local path)
  - Full `POST /banner/upload` spec (fields, response, validation rules, curl examples)
  - Full `POST /banner/upload/from-url` spec (fields, security constraints, curl example)
  - Azure Blob as durable store design (blob path mirrors local path, re-sync from Blob)
  - Upload workflow pre-ingest checklist
  - Existing blob sync endpoints (for reference)

- [ ] **M.2 — TDD: `POST /banner/upload` multipart handler**

  **Prompt for implementer:**
  Add `BannerUpload` handler in `internal/api/handlers.go`. Wire it in `internal/api/router.go`.

  **Path synthesis rules** (implement as `synthesizeLocalPath(module, sourceType, year, filename string) string`):
  ```go
  switch sourceType {
  case "banner":
      if year != "" {
          return filepath.Join("data/docs/banner", strings.ToLower(module), "releases", year, filename)
      }
      return filepath.Join("data/docs/banner", strings.ToLower(module), "releases", filename)
  case "banner_user_guide":
      return filepath.Join("data/docs/banner", strings.ToLower(module), "use", filename)
  case "sop":
      return filepath.Join("data/docs/sop", filename)
  }
  ```

  **Handler logic:**
  1. `r.ParseMultipartForm(100 << 20)` — 100 MB max
  2. Read `source_type`, `module`, `version`, `year` from form fields
  3. Validate: `source_type` required; `module` required for non-SOP; extension allowed
  4. Synthesize destination path via `synthesizeLocalPath()`
  5. `os.MkdirAll(filepath.Dir(destPath), 0755)`
  6. Stream file to `destPath`
  7. Call `ingest.Run(h.cfg, filepath.Dir(destPath), false, 10, 0, 0)` — ingest the containing folder
  8. Return `ingest.Result` + `stored_path` + `blob_stored:false`
  9. If `store_in_blob=true` and blob client configured: upload to Blob at mirror path after local write

  **TDD sequence:**
  1. **RED** — `internal/api/handlers_test.go` (or a new `upload_test.go`):
     - `TestBannerUpload_MissingSourceType_Returns400`
     - `TestBannerUpload_MissingModule_ForBanner_Returns400`
     - `TestBannerUpload_SynthesizesCorrectPath_Banner`
     - `TestBannerUpload_SynthesizesCorrectPath_UserGuide`
     - `TestBannerUpload_SynthesizesCorrectPath_Sop`
     - `TestBannerUpload_FileTooLarge_Returns400`
     - `TestBannerUpload_UnsupportedExtension_Returns400`
  2. **GREEN** — implement `BannerUpload` + `synthesizeLocalPath`
  3. **REFACTOR** — extract `synthesizeLocalPath` to `internal/ingest/path.go` so it can be
     unit tested in isolation and reused by the dry-run handler (Phase L.2)

  Run: `go test ./internal/api/... -v`

- [ ] **M.3 — TDD: `store_in_blob=true` mirrors upload to Azure Blob**

  **Prompt for implementer:**
  Extend `BannerUpload` to conditionally upload the saved file to Azure Blob Storage.
  The Blob path must mirror the local path (strip the `data/docs/` prefix).

  ```go
  // blobPath strips the "data/docs/" prefix from the local path.
  // "data/docs/banner/finance/releases/2026/foo.pdf" → "banner/finance/releases/2026/foo.pdf"
  func blobPath(localPath string) string {
      return strings.TrimPrefix(localPath, "data/docs/")
  }
  ```

  Add `UploadBlob(ctx, blobName, filePath string) error` method to `azure.BlobClient`.

  TDD:
  - `TestBlobPath_StripsPrefixCorrectly` — unit test for `blobPath()`
  - `TestBannerUpload_StoredInBlob_WhenFlagTrue` — httptest with a mock blob client
  - `TestBannerUpload_BlobSkipped_WhenFlagFalse` — assert blob client never called

  Run: `go test ./internal/api/... -v` and `go test ./internal/azure/... -v`

- [ ] **M.4 — TDD: `POST /banner/upload/from-url` with SSRF protection**

  **Prompt for implementer:**
  Add `BannerUploadFromURL` handler in `internal/api/handlers.go`.

  **SSRF protection:**
  ```go
  var defaultURLAllowlist = []string{"ellucian.com", "customercare.ellucian.com"}

  func isAllowedURL(rawURL, allowlistEnv string) error {
      u, err := url.Parse(rawURL)
      if err != nil { return fmt.Errorf("invalid URL") }
      if u.Scheme != "https" { return fmt.Errorf("only HTTPS URLs are allowed") }
      allowlist := defaultURLAllowlist
      if allowlistEnv == "*" { return nil }
      if allowlistEnv != "" { allowlist = strings.Split(allowlistEnv, ",") }
      for _, allowed := range allowlist {
          if strings.HasSuffix(u.Hostname(), allowed) { return nil }
      }
      return fmt.Errorf("URL hostname %q is not in the allowed list", u.Hostname())
  }
  ```

  Handler logic:
  1. Decode JSON body: `url`, `source_type`, `module`, `version`, `year`, `store_in_blob`
  2. Validate URL via `isAllowedURL(url, os.Getenv("UPLOAD_URL_ALLOWLIST"))`
  3. HTTP GET with 60s timeout and 100 MB size limit
  4. Extract filename from URL path; apply version to filename if `version` provided
  5. Synthesize local path, write file, ingest (same as M.2)
  6. If `store_in_blob=true`: upload to Blob

  TDD:
  - `TestBannerUploadFromURL_HTTPURLRejected_Returns400`
  - `TestBannerUploadFromURL_DisallowedHostname_Returns400`
  - `TestBannerUploadFromURL_AllowlistWildcard_AllowsAnyHTTPS`
  - `TestBannerUploadFromURL_DownloadsAndIngests_Success` (httptest server for download)
  - `TestIsAllowedURL_*` — table-driven unit tests for `isAllowedURL`

  Run: `go test ./internal/api/... -v`

- [ ] **M.5 — Document Agent 18 (Document Upload Coordinator) in `wiki/CLAUDE_AGENTS.md`**

  **Prompt for implementer:**
  Append Agent 18 to `wiki/CLAUDE_AGENTS.md`. See the Agent 18 design added in this phase.

  Agent 18 is a conversational upload agent: it asks the operator what they want to add,
  determines the correct metadata (module, source_type, version, year) from context,
  drives the upload, and verifies the result with a test query.

  Add to TOC, Tool Reference table, Cost Control table, and Priority Recommendation.

- [ ] **M.6 — Update `CLAUDE.md` with new upload endpoints**

  **Prompt for implementer:**
  In `CLAUDE.md` § Backend API, add:
  ```
  POST /banner/upload          { file (multipart), source_type, module, version?, year?, store_in_blob? }
  POST /banner/upload/from-url { url, source_type, module, version?, year?, store_in_blob? }
  Returns ingest.Result + stored_path + blob_stored
  NOTE: module must match a known module name; user_guide sources never set version/year
  SSRF protection: UPLOAD_URL_ALLOWLIST env var (default: ellucian.com domains only)
  ```

  Add env var documentation:
  ```
  UPLOAD_URL_ALLOWLIST   comma-separated HTTPS hostnames allowed for from-url ingest (default: ellucian.com)
  MAX_UPLOAD_SIZE_MB     max multipart upload file size in MB (default: 100)
  ```

---

### Phase M Acceptance Criteria

- [x] `wiki/INGEST.md` § Upload Workflow covers both new endpoints
- [ ] `POST /banner/upload` multipart form → file saved to synthesized path → ingested
- [ ] `POST /banner/upload` with missing `source_type` → 400
- [ ] `POST /banner/upload` with banner source + missing `module` → 400
- [ ] `POST /banner/upload` synthesizes correct path for banner / user_guide / sop
- [ ] `POST /banner/upload/from-url` with HTTP URL → 400
- [ ] `POST /banner/upload/from-url` with disallowed hostname → 400
- [ ] `POST /banner/upload/from-url` with `UPLOAD_URL_ALLOWLIST=*` → allowed for any HTTPS
- [ ] `store_in_blob=true` with configured Blob client → file mirrored in Blob at `blobPath(localPath)`
- [ ] `go test ./... -v` passes
- [ ] `go build ./...` succeeds
- [ ] `wiki/CLAUDE_AGENTS.md` documents Agent 18

---
