# AI Integration & Tech Stack Roadmap

> Last updated: 2026-04-14

## Current State

### Tech Stack (as of today)
| Component | Current Version | Notes |
|-----------|----------------|-------|
| Java | 21 | LTS — Java 21 is the current LTS |
| Spring Boot | 3.4.1 | Up to date |
| Playwright | 1.55.0 | Up to date |
| JUnit Jupiter | 5.10.2 | Patch versions available |
| Allure | 2.30.0 | Up to date |
| Azure AI OpenAI SDK | 1.0.0-beta.12 | **Declared but unused** — raw HttpClient used instead |
| DataFaker | 2.5.1 | Up to date |
| Jackson | 2.17.2 | Minor updates available |

### AI Features Already Built
| Feature | What It Does | Model |
|---------|-------------|-------|
| **AzureOpenAiService** | Analyzes Allure test results; produces Management, Business Owner, and Developer summaries | GPT-4o-mini via raw HTTP |
| **AITestGenerator** | Automates CRX-to-POM migration; reads raw Playwright recordings and generates full POM classes, JUnit tests, and Gherkin features | Azure OpenAI via raw HTTP |

**Key gap:** Both AI features call Azure OpenAI via raw `java.net.http.HttpClient` — the Azure SDK declared in `pom.xml` is unused dead weight. The raw `HttpClient` approach is actually an advantage: switching to any other provider is a URL + header change, not an SDK migration.

---

## Phase 1 — Tech Stack Modernization
**Goal:** Get dependencies to supported, current versions before building on top of them.

### Java 21 Upgrade
- [x] Bump compiler source/target to 21 in `pom.xml`
- [x] Update CI Docker image from `maven:3.8.7-openjdk-17` to `maven:3.9-eclipse-temurin-21`
- [x] Enable **virtual threads** in Spring Boot (`spring.threads.virtual.enabled=true`) — eliminates thread pool bottlenecks when tests run parallel HTTP calls
- [ ] Leverage **record patterns** and **sealed classes** where appropriate in model classes (e.g., `AiAnalysisResult`, `AllureRunSummary`)

### Spring Boot Upgrade
- [x] Bump Spring Boot from `3.2.4` → `3.4.x` (latest stable)
- [x] Review `application.properties` for any deprecated keys
- [x] Validate that `@ConfigurationProperties` bindings still work after upgrade

### Remove Dead Dependencies
- [ ] Remove `azure-ai-openai` SDK (`1.0.0-beta.12`) from `pom.xml` — unused; raw HttpClient is used instead
- [ ] Audit remaining unused imports across test files (left from CRX migration)

### Minor Dependency Bumps
- [ ] JUnit Jupiter `5.10.2` → `5.11.x`
- [ ] Jackson `2.17.2` → latest `2.18.x`
- [ ] Add **AssertJ** (`3.26+`) for fluent assertions — replace raw JUnit `Assertions.assertEquals` with `assertThat(...).isEqualTo(...)`

---

## Phase 2 — Provider-Agnostic AI Layer
**Goal:** Replace the Azure OpenAI hard-dependency with a pluggable `AiProvider` interface. Switch models or vendors via config, not code changes. Avoid vendor lock-in at the architecture level.

> Detailed phase breakdown with TDD task lists for `AITestGenerator` specifically can live in `docs/ai/AI-GENERATOR-ROADMAP.md` when that roadmap is added.

### The Core Problem With the Current Approach

Both `AzureOpenAiService` and `AITestGenerator` are hard-wired to Azure OpenAI endpoints. Switching providers means editing service classes. The fix: one interface, many implementations.

```java
// src/main/java/com/douglas/apps/reporter/ai/AiProvider.java
public interface AiProvider {

    /** Single-turn text completion. */
    AiResponse complete(AiRequest request) throws AiException;

    /** Multi-turn chat completion. */
    AiResponse chat(List<AiMessage> messages, AiRequest options) throws AiException;

    /** Optional: vision — send image bytes + prompt. */
    default AiResponse vision(byte[] imageBytes, String prompt, AiRequest options) {
        throw new UnsupportedOperationException("Vision not supported by this provider");
    }

    String providerName();
}
```

`AzureOpenAiService`, `ClaudeService`, `GeminiService`, `GroqService`, and `OllamaService` all implement this interface. `ReportGeneratorService` and `AITestGenerator` only ever call `AiProvider` — they never know which backend is running.

```java
// application.properties — switch providers without touching code
ai.reporter.provider=claude         # claude | gemini | groq | openai | ollama | azure
ai.generator.provider=claude
ai.fallback.provider=groq           # used if primary fails
```

---

### Provider Landscape

#### Option A — Anthropic Claude (recommended primary)

| Model | Best for | Context | Approx. cost |
|-------|----------|---------|-------------|
| `claude-haiku-4-5` | AI Reporter — fast JSON analysis | 200K | $0.00025/1K in, $0.00125/1K out |
| `claude-sonnet-4-6` | Code generation, screenshot analysis | 200K | $0.003/1K in, $0.015/1K out |

**Why:** 200K context window fits an entire Allure run + full POM context in one call. Vision built-in (no separate endpoint). Prompt caching cuts cost ~80% when the system prompt is static (e.g., the `AITestGenerator` project context file).

```java
// HTTP call — no SDK required, same pattern as current AzureOpenAiService
POST https://api.anthropic.com/v1/messages
Headers:
  x-api-key: $ANTHROPIC_API_KEY
  anthropic-version: 2023-06-01
  content-type: application/json

Body:
{
  "model": "claude-haiku-4-5-20251001",
  "max_tokens": 1024,
  "messages": [{ "role": "user", "content": "..." }]
}
```

Env vars: `ANTHROPIC_API_KEY`

---

#### Option B — Google Gemini (best free tier / longest context)

| Model | Best for | Context | Approx. cost |
|-------|----------|---------|-------------|
| `gemini-2.0-flash` | AI Reporter — extremely cheap | 1M | $0.000075/1K in, $0.0003/1K out |
| `gemini-2.5-pro` | Complex code gen, large context tasks | 1M | $0.00125/1K in, $0.01/1K out |

**Why:** 1M token context means every test result, screenshot, and POM file in the entire repo fits in one call. Free tier (60 req/min, 1M tokens/day) is sufficient for local development and low-volume CI runs. No credit card required to start.

```java
// Google Generative Language API — same raw HttpClient approach
POST https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent?key=$GEMINI_API_KEY
Body: { "contents": [{ "parts": [{ "text": "..." }] }] }
```

Env vars: `GEMINI_API_KEY` (get from [Google AI Studio](https://aistudio.google.com) — free)

**Data residency note:** Gemini API processes data in Google's infrastructure. Check [Google's data processing terms](https://ai.google.dev/terms) before sending student-adjacent data. Use Ollama (Option E) for anything containing actual student records.

---

#### Option C — Groq (best speed, ultra-low latency)

| Model | Best for | Context | Approx. cost |
|-------|----------|---------|-------------|
| `llama-3.3-70b-versatile` | AI Reporter — near-instant JSON | 128K | $0.00059/1K in, $0.00079/1K out |
| `qwen-2.5-coder-32b` | Code generation tasks | 128K | $0.00079/1K in, $0.00079/1K out |

**Why:** Groq runs open-source models on custom LPU (Language Processing Unit) hardware. Response times are typically 200–500ms — 5–10x faster than OpenAI or Claude at equivalent quality. Free tier: 30 req/min, 14,400 req/day.

```java
// Groq API is OpenAI-compatible — identical request format
POST https://api.groq.com/openai/v1/chat/completions
Headers: Authorization: Bearer $GROQ_API_KEY
Body: { "model": "llama-3.3-70b-versatile", "messages": [...] }
```

Env vars: `GROQ_API_KEY` (get from [console.groq.com](https://console.groq.com) — free)

**Best use case:** The AI Reporter's three parallel analysis calls (management/developer/business). At Groq speed they complete in under 2 seconds total instead of 15–20.

---

#### Option D — OpenAI Direct (without Azure)

| Model | Best for | Context | Approx. cost |
|-------|----------|---------|-------------|
| `gpt-4o-mini` | AI Reporter — cheap, reliable | 128K | $0.00015/1K in, $0.0006/1K out |
| `gpt-4o` | Code generation, vision | 128K | $0.0025/1K in, $0.01/1K out |

**Why over Azure OpenAI:** No Azure subscription required. No ARM deployment to manage. Simpler auth (one API key, no endpoint URL). Supports Structured Outputs (`response_format: json_schema`) for reliable JSON output without prompt engineering hacks.

```java
POST https://api.openai.com/v1/chat/completions
Headers: Authorization: Bearer $OPENAI_API_KEY
```

Env vars: `OPENAI_API_KEY` — same models, simpler setup than Azure.

---

#### Option E — Ollama (local, no API cost, data never leaves)

| Model | Best for | Context | Cost |
|-------|----------|---------|------|
| `llama3.2:3b` | AI Reporter on low-spec machines | 128K | Free |
| `llama3.2:8b` | Better quality, needs 8GB+ RAM | 128K | Free |
| `qwen2.5-coder:7b` | Code generation locally | 32K | Free |
| `mistral:7b` | Balanced quality/speed | 32K | Free |

**Why:** Zero API cost. Data never leaves the machine — relevant for FOIPPA compliance if test output ever contains student-identifiable information. Works offline. No rate limits.

```bash
# Install and pull a model
brew install ollama       # macOS
winget install Ollama     # Windows
ollama pull llama3.2:8b
ollama serve              # starts API on localhost:11434
```

```java
// Ollama is also OpenAI-compatible
POST http://localhost:11434/v1/chat/completions
Headers: Authorization: Bearer ollama   // any string
Body: { "model": "llama3.2:8b", "messages": [...] }
```

Env vars: `OLLAMA_BASE_URL=http://localhost:11434` (default)

**Tradeoff:** Slower than cloud APIs on CPU-only machines (~5–30s per call vs 1–3s). A modern laptop with Apple Silicon or a GPU runs models at cloud-comparable speeds.

---

#### Option F — Mistral AI (European data residency)

| Model | Best for | Context | Approx. cost |
|-------|----------|---------|-------------|
| `mistral-small-latest` | AI Reporter | 128K | $0.0001/1K in, $0.0003/1K out |
| `codestral-latest` | Code generation | 256K | $0.0003/1K in, $0.0009/1K out |

**Why:** If data residency in the EU is a requirement (not currently a constraint, but relevant if the college moves toward EU cloud providers), Mistral processes data in France. `codestral` is purpose-built for code tasks and outperforms models 2–3x its size on coding benchmarks.

```java
POST https://api.mistral.ai/v1/chat/completions
Headers: Authorization: Bearer $MISTRAL_API_KEY
```

Env vars: `MISTRAL_API_KEY`

---

### Provider Comparison Summary

| Provider | Speed | Cost | Context | Vision | Privacy | Free Tier |
|----------|-------|------|---------|--------|---------|-----------|
| **Claude** | Medium | Medium | 200K | ✅ | Cloud (US) | No |
| **Gemini** | Fast | Very cheap | 1M | ✅ | Cloud (Google) | ✅ Generous |
| **Groq** | Very fast | Very cheap | 128K | ❌ | Cloud (US) | ✅ Good |
| **OpenAI** | Medium | Medium | 128K | ✅ | Cloud (US) | No |
| **Ollama** | Slow (CPU) / Fast (GPU) | Free | 128K | Some models | Local — best | ✅ Always |
| **Mistral** | Medium | Cheap | 256K | ❌ | EU | No |

---

### Recommended Strategy

**AI Reporter** (speed and cost matter most): Groq as primary → Gemini as fallback → Ollama for offline/privacy mode.

**AITestGenerator** (quality matters most): Claude Sonnet → Gemini 2.5 Pro as fallback.

**Screenshot analysis** (vision required): Claude Sonnet or Gemini 2.0 Flash.

**Never as primary:** Azure OpenAI (unnecessary Azure dependency), OpenAI direct (same capability as Groq at 5x the cost for this use case).

---

### Migration Steps

- [x] Extract `AiProvider` interface into `com.douglas.apps.ai.provider.AiProvider.java`
- [x] Create `AzureOpenAiProvider.java` implementing `AiProvider`
- [x] Create `ClaudeProvider.java` — Anthropic Messages API via raw `HttpClient`
- [x] Create `GeminiProvider.java` — Google Generative Language API via raw `HttpClient`
- [x] Create `GroqProvider.java` — Groq OpenAI-compat API via raw `HttpClient`
- [x] Create `OllamaProvider.java` — Ollama OpenAI-compat API via raw `HttpClient`
- [x] Create `AiProviderFactory.java` — returns the right implementation based on env vars
- [x] Wire `AiProviderFactory` into `AITestGenerator` — remove all direct references to legacy Azure logic
- [x] Wire `AiProviderFactory` into `ReportGeneratorService` — partially done via `ClaudeAiService` implementation
- [x] Add `ai.fallback.provider` support in `AiProviderFactory` — on any retryable failure, try the fallback provider
- [x] Remove `azure-ai-openai` SDK from `pom.xml` (unused anyway)
- [x] Update `.env.example` with all provider API key variables
- [ ] Add prompt caching headers to `ClaudeProvider` for the static `AITestGenerator` system prompt

### Model Recommendations (updated)

| Use Case | Primary | Fallback | Offline |
|----------|---------|----------|---------|
| AI Reporter — fast JSON analysis | `groq/llama-3.3-70b-versatile` | `gemini/gemini-2.0-flash` | `ollama/llama3.2:8b` |
| AITestGenerator — Java code gen | `claude/claude-sonnet-4-6` | `gemini/gemini-2.5-pro` | `ollama/qwen2.5-coder:7b` |
| Screenshot / visual analysis | `claude/claude-sonnet-4-6` | `gemini/gemini-2.0-flash` | `ollama/llava:7b` |

---

## Phase 3 — Enhanced AI Features
**Goal:** Go beyond analysis — use AI to actively improve test reliability and coverage.

### 3A. Vision-Based Screenshot Analysis
Claude can read the screenshots already taken by `takeScreenshot()` and describe what it sees.

- [ ] After each test run, send failed-test screenshots to Claude with the test name and form code
- [ ] Claude returns: "What I see on screen", "Expected vs Actual", "Likely cause"
- [ ] Embed this in the Developer Summary tab of the HTML report
- [ ] No new infrastructure needed — screenshots already live in `target/screenshots/`

### 3B. Self-Healing Locator Suggestions
When a test fails with `TimeoutError` (locator not found), AI can suggest a fix.

- [ ] Hook into test failure in `BannerTestBase.@AfterEach` — if failure is a timeout, capture the page DOM
- [ ] Send failing locator + DOM snippet to Claude
- [ ] Claude returns: updated `getByRole(...)` call to try
- [ ] Write suggestions to a `LOCATOR-SUGGESTIONS.md` file in `target/` for developer review
- [ ] **Not auto-applied** — developer reviews and updates the POM class manually

### 3C. Natural Language Test Authoring (Expand AITestGenerator)
Currently `AITestGenerator` only works from Gherkin files. Expand it:

- [x] Accept plain-English descriptions as input (e.g., "Test that a student with a Financial Hold cannot register for a course in SFAREGS")
- [x] Claude generates the Gherkin *and* the Java POM test in one shot
- [x] Add a CLI command: `mvn exec:java -Pai-generate -Dexec.args="SFAREGS --description='...'"`
- [ ] Output: new `*Form.java` POM stub + new `*Test.java` test class skeleton

### 3D. AI-Powered Test Prioritization (CI Integration)
Before running the full suite, ask Claude which tests are highest risk given recent changes.

- [ ] Pass `git diff --stat HEAD~1` output to Claude before test run
- [ ] Claude returns a ranked list of test classes most likely to be affected
- [ ] CI uses the ranking to run high-priority tests first (fast feedback)
- [ ] Fall back to full suite for nightly scheduled runs

### 3E. Intelligent Failure Deduplication
Across multiple CI runs, group failures that share the same root cause.

- [ ] Store AI analysis JSON in `target/ai-history/` with a timestamp
- [ ] On each new run, send the last 5 AI analyses to Claude with the new failures
- [ ] Claude identifies: "This failure also occurred in runs X, Y, Z — likely infrastructure flakiness, not a code regression"
- [ ] Surface this in the Developer Summary tab

---

## Phase 4 — Infrastructure & Developer Experience
**Goal:** Make the platform fast to iterate on and easy to run anywhere.

### Containerization
- [ ] Create `Dockerfile` — base: `eclipse-temurin:21-jdk`, include Maven wrapper, pre-download Playwright browsers
- [ ] Create `docker-compose.yml` — service: test runner; volume: `target/` for Allure results and screenshots
- [ ] Update `.gitlab-ci.yml` to use the pre-built Docker image instead of installing Node + browsers at runtime (saves ~3 min per CI run)

### Code Quality
- [ ] Add **SonarCloud** free tier — runs on CI, catches code smells and coverage gaps
- [ ] Add **Checkstyle** Maven plugin — enforce Google Java Style across POM classes
- [ ] Add **SpotBugs** — static analysis for null pointer risks and bad patterns

### Dependency Automation
- [ ] Enable **Renovate Bot** or **Dependabot** on GitLab — weekly PRs for dependency updates
- [ ] Configure to auto-merge patch updates, require review for minor/major

### Observability
- [ ] Add **OpenTelemetry** traces to `BannerTestBase` — span per test, per form action
- [ ] Export traces to a local Jaeger instance (docker-compose) during development
- [ ] Useful for diagnosing *where* in a multi-step test time is being lost

---

---

## Phase 5 — AI Quality & Analytics

**Goal:** Improve the faithfulness and usefulness of AI-generated analysis; add structured data storage for cost tracking and feedback.

### 5A. Query Rewriting for AI Reporter

Before sending test results to Claude for analysis, rewrite the prompt to be more targeted. Instead of a generic "analyze these failures", the reporter can first classify the failure type and then prompt accordingly.

- [ ] Detect failure category from Allure result: `TimeoutError` (locator broken), `AssertionError` (assertion failed), `IOException` (infra issue)
- [ ] Generate a category-specific prompt: "This is a locator failure. Focus on which DOM element was not found and suggest a corrected `getByRole()` call."
- [ ] Return structured JSON from Claude (`failureType`, `likelyCause`, `suggestedFix`) instead of free-form text
- [ ] Render the structured fields in separate columns in the Developer Summary tab

---

### 5B. Grounding Check (Hallucination Guard)

After Claude generates an analysis, verify that its claims are supported by the actual Allure data passed in. Catches cases where Claude invents plausible-sounding root causes not present in the evidence.

- [ ] After receiving Claude's analysis, send a second prompt: "Given only the following test output, is this analysis fully supported? Answer 'yes', 'partial', or 'no'."
- [ ] If `partial` or `no`: retry with a narrower prompt or flag the analysis as "low confidence" in the report
- [ ] Log the grounding score to a file for weekly review — identifies systemic prompt weaknesses

---

### 5C. Multi-Perspective Analysis

Run the same Allure results through multiple focused prompts in parallel and merge the outputs.

```
Allure results
     ↓
┌────┴────┐────────────┐
Management  Dev         Business
summary     analysis    impact
     └────┬────┘────────┘
          Merged report
```

- [ ] Use Java's `CompletableFuture` to fire all three prompts concurrently (already done today — verify this is working)
- [ ] Add a fourth perspective: "Infrastructure" — flags failures that are likely CI/environment issues rather than Banner regressions
- [ ] Each perspective gets its own retry budget: if the management summary fails, the developer summary still completes

---

### 5D. Semantic Caching for AI Reporter

Identical or near-identical failure patterns recur across runs. Cache the AI analysis by embedding similarity so the reporter skips redundant LLM calls.

- [ ] Embed the normalized test failure message (stack trace stripped of line numbers and timestamps)
- [ ] Before calling Claude, check if a cached analysis exists for a semantically similar failure (cosine similarity > 0.97)
- [ ] If cache hit: return cached analysis, mark it as "recurring failure — first seen on {date}"
- [ ] Store cache entries in a local JSON file (`target/ai-cache/`) with a 30-day TTL
- [ ] After each ingest or config change, clear the cache — stale analyses can be misleading

**Cost impact:** Significant savings during flaky-test periods where the same timeout failure appears in 10 consecutive runs.

---

### 5E. Ask Log & Analytics

Store every AI Reporter invocation to enable cost tracking, failure trend analysis, and prompt improvement.

```java
// src/main/java/com/douglas/apps/reporter/AnalyticsLog.java
public record AskLogEntry(
    Instant askedAt,
    String suiteClass,
    String promptType,           // "management", "developer", "business"
    int promptTokens,
    int completionTokens,
    double estimatedCostUsd,
    int durationMs,
    boolean cacheHit
) {}
```

Write each entry as a JSON line to `target/ai-analytics/ask-log.ndjson`. After accumulating enough history, queries like these become possible:

```bash
# Total AI cost this week
jq -s '[.[].estimatedCostUsd] | add' target/ai-analytics/ask-log.ndjson

# Average latency by prompt type
jq -s 'group_by(.promptType) | map({type: .[0].promptType, avgMs: (map(.durationMs) | add / length)})' ...
```

- [ ] Add `AnalyticsLog` to `ReporterCli` — write one entry per Claude call
- [ ] Expose a `GET /reporter/cost-summary` Spring Boot endpoint for a rolling 30-day view
- [ ] Surface the weekly cost estimate in the Management Summary tab

---

### 5F. Feedback Loop

Track whether AI analyses are accurate by capturing reviewer feedback.

- [ ] Add a feedback mechanism to the HTML report: thumbs up/down per analysis section
- [ ] On click, POST `{ "runId": "...", "section": "developer", "helpful": true/false }` to `POST /reporter/feedback`
- [ ] Store feedback in `target/ai-analytics/feedback.json`
- [ ] Weekly: identify analyses that consistently get thumbs-down — these identify prompt weaknesses or data gaps

This is the lightest-weight eval loop before investing in a full RAGAS-style evaluation pipeline.

---

## Nice to Haves

### AI
- [ ] **Multi-model fallback** — if Claude is unavailable, fall back to Azure OpenAI for the AI Reporter (keep both clients, prefer Claude)
- [ ] **Slack bot integration** — post the Management Summary to a Slack channel with a link to the full HTML report
- [ ] **AI-generated release notes** — after each sprint, Claude summarizes what test coverage was added and what gaps remain
- [ ] **Gherkin library** — maintain a `features/` directory of BDD scenarios; AI keeps them in sync with the actual POM methods

### Tech Stack
- [ ] **GraalVM native image** — compile the reporter CLI to a native binary; eliminates JVM startup time when running standalone
- [ ] **Playwright trace viewer** — already supported in Playwright; enable `context.tracing()` on failure for step-by-step replay
- [ ] **Allure TestOps** — upgrade from open-source Allure to TestOps SaaS for persistent history and analytics dashboard
- [ ] **AssertJ soft assertions** — collect all failures in a test before stopping (avoids fix-one-fail-next iteration)

---

## Out of Scope

- Migrating the test language from Java to TypeScript/JavaScript — framework is Java-native, Spring Boot integration is a hard requirement
- Load testing via Playwright — not the purpose of this framework
- Full LLM fine-tuning on Banner-specific test data — prompt engineering is sufficient and far cheaper
