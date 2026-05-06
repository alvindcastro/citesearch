# Claude Agents for citesearch

Brainstorm of Claude agent designs that use citesearch as the backend knowledge system.
Each agent wraps the existing HTTP API as tools — no changes to the Go backend required.

---

## Table of Contents

1. [Overview & Prerequisites](#overview--prerequisites)
2. [Agent 1: Banner Ask Agent](#agent-1-banner-ask-agent)
3. [Agent 2: SOP Navigator Agent](#agent-2-sop-navigator-agent)
4. [Agent 3: Upgrade Impact Analyzer Agent](#agent-3-upgrade-impact-analyzer-agent)
5. [Agent 4: Conversational RAG Agent](#agent-4-conversational-rag-agent)
6. [Agent 5: Document Ingestion Orchestrator Agent](#agent-5-document-ingestion-orchestrator-agent)
7. [Agent 6: SOP Gap Analyzer Agent](#agent-6-sop-gap-analyzer-agent)
8. [Agent 7: Index Health & Diagnostics Agent](#agent-7-index-health--diagnostics-agent)
9. [Agent 8: Internal Banner Chatbot Agent](#agent-8-internal-banner-chatbot-agent)
10. [Agent 9: Confidence Calibration Agent](#agent-9-confidence-calibration-agent)
11. [Agent 10: Banner User Guide Navigation Agent](#agent-10-banner-user-guide-navigation-agent)
12. [Agent 11: Banner Student Wizard Conductor](#agent-11-banner-student-wizard-conductor)
13. [Agent 12: Wizard Test Harness Agent](#agent-12-wizard-test-harness-agent)
14. [Agent 13: Query Rewriting Agent](#agent-13-query-rewriting-agent)
15. [Agent 14: Answer Grounding Verifier Agent](#agent-14-answer-grounding-verifier-agent)
16. [Agent 15: Analytics Advisor Agent](#agent-15-analytics-advisor-agent)
17. [Agent 16: Feedback Pattern Analyst Agent](#agent-16-feedback-pattern-analyst-agent)
18. [Agent 17: PDF Ingest Preflight Auditor Agent](#agent-17-pdf-ingest-preflight-auditor-agent)
19. [Agent 18: Document Upload Coordinator Agent](#agent-18-document-upload-coordinator-agent)
20. [Agent 19: Upload Chunking Wizard Agent](#agent-19-upload-chunking-wizard-agent)
21. [Tool Reference: API-to-Tool Mapping](#tool-reference-api-to-tool-mapping)
22. [Implementation Notes](#implementation-notes)
23. [Priority Recommendation](#priority-recommendation)

---

## Overview & Prerequisites

All agents below use the [Anthropic Python SDK](https://github.com/anthropics/anthropic-sdk-python)
with tool use. Each tool is a thin wrapper around a citesearch HTTP endpoint.

**Runtime requirements:**
- citesearch running (Docker or local) at a known base URL
- `ANTHROPIC_API_KEY` set in environment
- `OMNIVORE_BASE_URL` pointing to the running backend (e.g. `http://localhost:8000`)
- `OMNIVORE_API_KEY` if API key auth is enabled on the backend

**Recommended model:** `claude-opus-4-6` for multi-step reasoning agents; `claude-haiku-4-5` for
simple single-turn Q&A to minimize cost.

**Shared tool executor pattern:**

```python
import anthropic
import requests
import json
import os

BASE_URL = os.environ["OMNIVORE_BASE_URL"]
API_KEY = os.environ.get("OMNIVORE_API_KEY", "")

HEADERS = {"Authorization": f"Bearer {API_KEY}", "Content-Type": "application/json"}

client = anthropic.Anthropic()

def call_api(method: str, path: str, body: dict | None = None) -> dict:
    url = f"{BASE_URL}{path}"
    if method == "GET":
        resp = requests.get(url, headers=HEADERS)
    else:
        resp = requests.post(url, json=body, headers=HEADERS)
    resp.raise_for_status()
    return resp.json()

def run_tool(tool_name: str, tool_input: dict) -> str:
    """Dispatch tool call to the correct API endpoint."""
    dispatch = {
        "banner_ask": lambda i: call_api("POST", "/banner/ask", i),
        "sop_ask": lambda i: call_api("POST", "/sop/ask", i),
        "sop_list": lambda i: call_api("GET", "/sop"),
        "banner_summarize": lambda i: call_api("POST", f"/banner/summarize/{i.pop('topic', 'full')}", i),
        "banner_ingest": lambda i: call_api("POST", "/banner/ingest", i),
        "sop_ingest": lambda i: call_api("POST", "/sop/ingest", i),
        "index_health": lambda i: call_api("GET", "/health"),
        "index_stats": lambda i: call_api("GET", "/index/stats"),
        "analytics_summary": lambda i: call_api("GET", "/analytics/summary"),
        "feedback_report": lambda i: call_api("GET", "/analytics/feedback"),
        "feedback_submit": lambda i: call_api("POST", "/chat/feedback", i),
    }
    result = dispatch[tool_name](tool_input)
    return json.dumps(result)
```

---

## Agent 1: Banner Ask Agent

**Purpose:** Single-turn Q&A about Ellucian Banner ERP release notes. Routes to the right module
automatically. Returns a grounded answer with source citations.

**Use case:** IT staff paste a question into Slack/Teams/CLI and get an answer citing the exact
release note page.

**Type:** Simple tool-use agent (1–2 tool calls max).

### Tools

```python
BANNER_ASK_TOOLS = [
    {
        "name": "banner_ask",
        "description": (
            "Search Banner ERP release notes and answer a question using indexed documents. "
            "Use module_filter to scope by Banner module (Finance, Student, HR, General, etc.). "
            "Use version_filter to scope to a specific version like '9.3.37.2'. "
            "Returns answer text plus source chunks with page numbers and confidence scores."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {"type": "string", "description": "The question to answer"},
                "module_filter": {"type": "string", "description": "Banner module name, e.g. Finance, Student, HR, General"},
                "version_filter": {"type": "string", "description": "Specific Banner version, e.g. 9.3.37.2"},
                "top_k": {"type": "integer", "description": "Number of source chunks to retrieve", "default": 5},
                "mode": {"type": "string", "enum": ["local", "web", "hybrid", "auto"], "default": "auto"},
            },
            "required": ["question"],
        },
    }
]
```

### System Prompt

```python
BANNER_ASK_SYSTEM = """You are a Banner ERP upgrade assistant for IT staff.
Answer questions about Ellucian Banner release notes using the banner_ask tool.

Rules:
- Always use the tool before answering. Never answer from prior knowledge alone.
- If the user mentions a module (Finance, Student, HR, General, Accounts Receivable, etc.), set module_filter.
- If the user mentions a version number, set version_filter.
- After tool call: quote the top_score from the response as your confidence level.
- If retrieval_count == 0, say "No relevant documents found" and suggest checking the index.
- Note: Azure AI Search scores for this index are typically 0.01–0.05 — a score of 0.033 is a valid answer, not low confidence.
- Format sources as: [Document Title, page N, module, version]
- Keep answers concise. Lead with the direct answer, then list sources."""
```

### Implementation

```python
def banner_ask_agent(question: str) -> str:
    messages = [{"role": "user", "content": question}]

    while True:
        response = client.messages.create(
            model="claude-haiku-4-5-20251001",
            max_tokens=1024,
            system=BANNER_ASK_SYSTEM,
            tools=BANNER_ASK_TOOLS,
            messages=messages,
        )

        if response.stop_reason == "end_turn":
            return next(b.text for b in response.content if b.type == "text")

        # Process tool calls
        tool_results = []
        for block in response.content:
            if block.type == "tool_use":
                result = run_tool(block.name, dict(block.input))
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": block.id,
                    "content": result,
                })

        messages.append({"role": "assistant", "content": response.content})
        messages.append({"role": "user", "content": tool_results})
```

### Example

```
User: What database changes are required for Banner Finance 9.3.22?

Agent: [calls banner_ask(question="database changes required", module_filter="Finance", version_filter="9.3.22")]

Answer: Banner Finance 9.3.22 requires the following database changes:
1. Run the FINUPGRADE.sql script before restarting application services
2. Three new index columns on FGBTRND table (see DBA checklist, page 4)
Confidence: 0.73

Sources:
- Banner Finance Release Notes 9.3.22, page 4 (Finance, 9.3.22)
- Banner Finance Release Notes 9.3.22, page 12 (Finance, 9.3.22)
```

---

## Agent 2: SOP Navigator Agent

**Purpose:** Browse and query Standard Operating Procedures. Can list available SOPs, answer
procedural questions, and walk through step-by-step procedures.

**Use case:** Functional analysts who need to follow a procedure without opening DOCX files.

**Type:** Multi-tool agent (list → ask flow).

### Tools

```python
SOP_TOOLS = [
    {
        "name": "sop_list",
        "description": "List all indexed Standard Operating Procedures (SOPs) with their IDs and titles.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "sop_ask",
        "description": (
            "Answer a question using indexed SOP documents. "
            "Use this after sop_list if the user wants a specific procedure, "
            "or directly if the question is clear."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {"type": "string", "description": "The procedural question to answer"},
                "top_k": {"type": "integer", "default": 5},
            },
            "required": ["question"],
        },
    },
]
```

### System Prompt

```python
SOP_SYSTEM = """You are a procedure guide for IT operations staff.
You help users find and follow Standard Operating Procedures (SOPs).

Workflow:
1. If the user asks "what SOPs do you have?" or similar → call sop_list first.
2. For procedural questions → call sop_ask directly.
3. For step-by-step walkthroughs → call sop_ask, then present steps in numbered format.

Rules:
- Present procedures as numbered steps, not prose.
- Always cite the SOP number and section (e.g. "SOP122, Section 4.1").
- If retrieval_count == 0, warn the user no documents were found. Scores of 0.01–0.05 are normal for this index and do NOT indicate low quality.
- Never invent steps. If steps are missing from the retrieved context, say so."""
```

### Example

```
User: Walk me through the Banner upgrade smoke test

Agent: [calls sop_ask(question="Banner upgrade smoke test steps")]

Here are the steps from SOP122 - Smoke Test and Sanity Test Post Banner Upgrade:

**Pre-test:**
1. Verify all Banner application servers have restarted successfully
2. Confirm database connections are active

**Smoke Test:**
3. Log into Banner Admin Pages with test account
4. Navigate to each module listed in Appendix A
5. Confirm page loads without errors

[continues with retrieved steps...]

Source: SOP122, Section 3 — Smoke Test Procedure (confidence: 0.81)
```

---

## Agent 3: Upgrade Impact Analyzer Agent

**Purpose:** Produce a structured upgrade impact report for a specific Banner version. Runs
multiple summarization calls in parallel and synthesizes a single report with risk assessment.

**Use case:** IT manager preparing for a Banner upgrade needs a briefing document in minutes,
not hours of manual PDF reading.

**Type:** Multi-step parallel tool-call agent. Highest value, most complex.

### Tools

```python
UPGRADE_TOOLS = [
    {
        "name": "banner_summarize",
        "description": (
            "Summarize a specific aspect of Banner release changes. "
            "topic options: 'changes' (all changes), 'breaking' (breaking changes only), "
            "'actions' (required action items), 'compatibility' (compatibility notes), "
            "'full' (complete summary with all sections)."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "filename": {"type": "string", "description": "Banner release note filename to summarize"},
                "topic": {
                    "type": "string",
                    "enum": ["changes", "breaking", "actions", "compatibility", "full"],
                    "description": "Which aspect to summarize"
                },
                "banner_module": {"type": "string", "description": "e.g. Finance, Student, General"},
                "banner_version": {"type": "string", "description": "e.g. 9.3.37.2"},
                "top_k": {"type": "integer", "default": 10},
            },
            "required": ["filename", "topic"],
        },
    },
    {
        "name": "sop_ask",
        "description": "Check if existing SOPs cover a specific Banner change or procedure.",
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {"type": "string"},
                "top_k": {"type": "integer", "default": 3},
            },
            "required": ["question"],
        },
    },
]
```

### System Prompt

```python
UPGRADE_ANALYZER_SYSTEM = """You are a Banner ERP upgrade impact analyzer.
When given a Banner version and module, produce a structured upgrade impact report.

Report structure:
1. **Breaking Changes** — anything that will break existing functionality
2. **Required Actions** — steps that MUST be completed (DBA, functional, sysadmin)
3. **Compatibility Notes** — dependencies, version requirements, integration impacts
4. **SOP Coverage** — which existing SOPs apply; flag gaps where no SOP exists
5. **Risk Assessment** — Overall risk level (Low/Medium/High) with justification

Process:
1. Call banner_summarize for topic='breaking', then 'actions', then 'compatibility' in sequence
   (or all in parallel if you can - prefer parallel calls)
2. Call sop_ask to check if SOPs exist for the major action items
3. Synthesize all results into the structured report above
4. Assign a risk level based on number of breaking changes and uncovered SOP gaps

Output format: Markdown with clear section headers."""
```

### Example Interaction

```
User: Analyze upgrade impact for Banner General 9.3.37.2

Agent: Let me pull all the relevant information in parallel...
[calls banner_summarize(topic="breaking", filename="Banner_General_Release_Notes_9.3.37.2.pdf", banner_version="9.3.37.2")]
[calls banner_summarize(topic="actions", filename="Banner_General_Release_Notes_9.3.37.2.pdf", banner_version="9.3.37.2")]
[calls banner_summarize(topic="compatibility", filename="Banner_General_Release_Notes_9.3.37.2.pdf", banner_version="9.3.37.2")]

[then calls sop_ask for each major action item found]

# Banner General 9.3.37.2 — Upgrade Impact Report

## Breaking Changes
- Login page authentication flow changed; custom SSO configs must be updated...

## Required Actions
- [ ] DBA: Run GENUPGRADE.sql before service restart
- [ ] Sysadmin: Update JBoss configuration (see page 7)
...

## SOP Coverage
- DBA upgrade script: **SOP154 applies** (verified)
- JBoss reconfiguration: **No SOP found** ⚠ — recommend creating one

## Risk Assessment: **MEDIUM**
2 breaking changes with clear remediation steps; 1 SOP gap identified.
```

---

## Agent 4: Conversational RAG Agent

**Purpose:** Multi-turn conversation about Banner and SOP content. Remembers context across
turns so users can ask follow-up questions naturally.

**Use case:** IT staff having an extended planning session about an upcoming upgrade.

**Type:** Stateful multi-turn agent with full tool access.

### Tools

Combines all tools: `banner_ask`, `sop_ask`, `sop_list`, `banner_summarize`.

### System Prompt

```python
CONVERSATIONAL_SYSTEM = """You are a Banner ERP knowledge assistant for IT staff.
You can answer questions about Banner release notes and Standard Operating Procedures.

You have access to:
- banner_ask: search Banner release notes
- sop_ask: search Standard Operating Procedures
- sop_list: list all available SOPs
- banner_summarize: get structured summaries of release changes

Behavior:
- Use conversation history to understand follow-up questions (e.g., "What about the Finance module?"
  refers to the version discussed earlier)
- When you retrieve information, state the source and confidence
- Maintain a mental model of what the user is trying to accomplish and proactively surface relevant info
- If a question spans both Banner and SOP domains, call both tools
- Escalate explicitly: say "I recommend verifying this with the source document" when retrieval_count == 0 (Azure scores of 0.01–0.05 are normal — do not escalate on score alone)"""
```

### Implementation

```python
def conversational_agent():
    """Interactive conversation loop."""
    messages = []
    print("Banner & SOP Assistant (type 'quit' to exit)\n")

    while True:
        user_input = input("You: ").strip()
        if user_input.lower() == "quit":
            break

        messages.append({"role": "user", "content": user_input})

        while True:
            response = client.messages.create(
                model="claude-sonnet-4-6",
                max_tokens=2048,
                system=CONVERSATIONAL_SYSTEM,
                tools=[*BANNER_ASK_TOOLS, *SOP_TOOLS, *UPGRADE_TOOLS],
                messages=messages,
            )

            if response.stop_reason == "end_turn":
                answer = next(b.text for b in response.content if b.type == "text")
                print(f"\nAssistant: {answer}\n")
                messages.append({"role": "assistant", "content": response.content})
                break

            # Process tool calls, append results, continue loop
            tool_results = []
            for block in response.content:
                if block.type == "tool_use":
                    result = run_tool(block.name, dict(block.input))
                    tool_results.append({
                        "type": "tool_result",
                        "tool_use_id": block.id,
                        "content": result,
                    })
            messages.append({"role": "assistant", "content": response.content})
            messages.append({"role": "user", "content": tool_results})
```

### Example Session

```
You: What changed in Banner General 9.3.37.2?
Assistant: [calls banner_summarize(topic="changes"...)]
           Here are the main changes in Banner General 9.3.37.2: [lists changes]

You: Which of those need DBA work?
Assistant: [understands "those" refers to 9.3.37.2 from context]
           [calls banner_ask(question="DBA database changes", version_filter="9.3.37.2")]
           The changes requiring DBA involvement are...

You: Is there an SOP for that?
Assistant: [calls sop_ask(question="database upgrade procedure DBA Banner")]
           Yes, SOP154 covers database-related upgrade steps. Section 3.2 specifically addresses...
```

---

## Agent 5: Document Ingestion Orchestrator Agent

**Purpose:** Safely orchestrate document ingestion with pre-flight checks and post-ingestion
validation. Prevents accidental duplicate ingestion and confirms index health afterward.

**Use case:** IT admin drops new Banner PDFs into `data/docs/banner/` and wants to verify
everything ingested cleanly.

**Type:** Sequential workflow agent with guard rails.

### Tools

```python
INGESTION_TOOLS = [
    {
        "name": "index_health",
        "description": "Check if the RAG backend is running and healthy.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "index_stats",
        "description": "Get current index statistics: document count, chunk count, index name.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "banner_ingest",
        "description": (
            "Trigger ingestion of Banner PDF documents from data/docs/banner/. "
            "WARNING: This is slow and costs Azure OpenAI tokens. "
            "Only call after confirming with the user."
        ),
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "sop_ingest",
        "description": "Trigger ingestion of SOP DOCX documents from data/docs/sop/.",
        "input_schema": {"type": "object", "properties": {}},
    },
]
```

### System Prompt

```python
INGESTION_SYSTEM = """You are an ingestion orchestrator for the citesearch knowledge system.
Your job is to safely manage document ingestion with validation at each step.

Safe ingestion workflow:
1. Call index_health to confirm the backend is running
2. Call index_stats to record the BEFORE chunk count
3. Tell the user what you found and confirm they want to proceed with ingestion
4. If confirmed: call the appropriate ingest tool (banner_ingest or sop_ingest)
5. After ingestion: call index_stats again to verify chunk count increased
6. Report the delta: how many new chunks were added

Important guards:
- Never call ingest without first checking health
- Always report before/after chunk counts
- If health check fails, stop and report the error — don't attempt ingestion
- If chunk count does NOT increase after ingestion, flag this as a potential problem
- Ingestion is idempotent (deterministic chunk IDs) but still costs embedding tokens"""
```

### Example

```
User: Ingest the new Banner Finance release notes I just added

Agent: Let me check the system before we start...
[calls index_health → {"status": "ok", "model": "gpt-4o-mini"}]
[calls index_stats → {"chunks": 1247, "documents": 12, "index": "banner-upgrade-knowledge"}]

Current state:
- System: healthy
- Index: 1,247 chunks across 12 documents

Ready to ingest from data/docs/banner/. This will embed all PDFs and may cost Azure tokens
for any new documents. Shall I proceed? (yes/no)

User: yes

Agent: [calls banner_ingest]

Ingestion complete. Let me verify...
[calls index_stats → {"chunks": 1389, "documents": 14, "index": "banner-upgrade-knowledge"}]

Result:
- Added 142 new chunks
- Added 2 new documents
- Index now contains 1,389 chunks across 14 documents
```

---

## Agent 6: SOP Gap Analyzer Agent

**Purpose:** Cross-reference Banner release note action items against existing SOPs to identify
gaps — procedures that are required by a Banner upgrade but have no corresponding SOP.

**Use case:** Before a Banner upgrade, confirm that every required step has a documented procedure
that staff can follow.

**Type:** Multi-step analysis agent.

### Tools

Combines: `banner_summarize` (topic=actions/breaking) + `sop_ask` + `sop_list`.

### System Prompt

```python
GAP_ANALYZER_SYSTEM = """You are a documentation gap analyzer for Banner ERP upgrades.
Your job is to identify which Banner upgrade action items lack SOP coverage.

Process:
1. Call banner_summarize(topic="actions") to get all required action items for a release
2. Call banner_summarize(topic="breaking") to get breaking changes that may need new SOPs
3. Call sop_list to understand what SOPs currently exist
4. For each major action item, call sop_ask to check if a matching SOP exists
5. Compile a gap report

Output format:
## Action Items with SOP Coverage
- [action]: **Covered by SOP123** ✓

## Gaps — No SOP Found
- [action]: **No SOP found** ⚠
  Suggested SOP title: "Procedure — [action name]"
  Suggested sections: [outline the SOP structure based on the action item details]

## Recommendations
[Prioritized list of SOPs to create, with effort estimates]"""
```

### Example Output

```
## Banner General 9.3.37.2 — SOP Gap Analysis

## Action Items with SOP Coverage
- Post-upgrade smoke test: **Covered by SOP122** ✓
- Axiom service restart: **Covered by SOP154** ✓

## Gaps — No SOP Found
- JBoss session timeout reconfiguration: **No SOP found** ⚠
  Suggested SOP title: "Procedure — JBoss Session Timeout Update Post-Banner-Upgrade"
  Suggested sections:
  1. Prerequisites (JBoss admin access, Banner admin access)
  2. Backup current configuration
  3. Update timeout value in standalone.xml
  4. Restart JBoss service
  5. Verify Banner login session behavior

## Recommendations
1. Create JBoss configuration SOP (HIGH priority — blocking for upgrade)
2. Consider adding LDAP verification steps to SOP122
```

---

## Agent 7: Index Health & Diagnostics Agent

**Purpose:** Diagnose RAG system health issues. When retrieval quality degrades or the system
returns unexpected answers, this agent investigates and recommends fixes.

**Use case:** A user reports "the system isn't finding anything about Finance" — run this agent
to diagnose why.

**Type:** Diagnostic investigation agent.

### Tools

```python
DIAGNOSTIC_TOOLS = [
    {
        "name": "index_health",
        "description": "Check backend health and model configuration.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "index_stats",
        "description": "Get chunk counts and index metadata.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "banner_ask",
        "description": "Test a query against the index to check retrieval quality.",
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {"type": "string"},
                "module_filter": {"type": "string"},
                "version_filter": {"type": "string"},
                "top_k": {"type": "integer", "default": 5},
                "mode": {"type": "string", "default": "local"},
            },
            "required": ["question"],
        },
    },
]
```

### System Prompt

```python
DIAGNOSTIC_SYSTEM = """You are a RAG system diagnostician. When given a complaint about
retrieval quality, investigate systematically and provide a root cause analysis.

Diagnostic checklist:
1. index_health — is the backend up? are model deployments configured?
2. index_stats — are there chunks in the index at all? expected range: 500–5000
3. Run a broad test query (no filters) — does it return results?
4. Run the same query with the user's filters — does filtering break it?
5. Check top_score: if < 0.010 on all results, likely a vector embedding mismatch

Common failure modes:
- Empty index (0 chunks): ingestion hasn't been run or failed silently
- Low top_score (<0.010): embedding model mismatch or query is too narrow
- Good chunks but wrong module: source_type or module metadata not set during ingestion
- Tavily fallback in local mode: web search being used when it shouldn't

For each finding, provide:
- What you found
- Why it's a problem
- How to fix it (specific command or configuration change)"""
```

---

## Agent 8: Internal Banner Chatbot Agent

**Purpose:** Internal-user chatbot for IT staff, functional analysts, and Banner admins.
Wraps the `/chat/*` adapter endpoints. Answers questions about Banner ERP modules,
release notes, upgrade changes, and operational SOPs. Escalates when confidence is low.

**Use case:** Deployed as a Botpress webhook target — internal users ask in natural language
and get grounded answers citing release notes or SOP documents directly.

**Type:** Multi-tool agent with escalation logic. Uses the simplified three-source routing
(`banner`, `finance`, `sop`).

### Tools

```python
INTERNAL_CHATBOT_TOOLS = [
    {
        "name": "chat_intent",
        "description": (
            "Classify the user's message into one of: BannerRelease, BannerFinance, "
            "SopQuery, BannerAdmin, General. "
            "Returns intent name and confidence score (0–1). "
            "Use this first to decide which source to query."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "message": {"type": "string", "description": "The user's question"},
            },
            "required": ["message"],
        },
    },
    {
        "name": "chat_sentiment",
        "description": (
            "Detect the emotional tone of the user's message. "
            "Returns sentiment (Positive, Neutral, Frustrated) and a score (0–1). "
            "Use this to decide whether to escalate regardless of answer confidence."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "message": {"type": "string"},
            },
            "required": ["message"],
        },
    },
    {
        "name": "chat_ask",
        "description": (
            "Answer the user's question using the Ask Banner knowledge base. "
            "Set source based on the intent: "
            "'finance' for BannerFinance, 'sop' for SopQuery, "
            "'banner' for everything else (BannerRelease, BannerAdmin, General). "
            "Returns answer, confidence (0–1), sources[], and escalate flag."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "message": {"type": "string", "description": "The user's question verbatim"},
                "session_id": {"type": "string", "description": "Unique session identifier"},
                "source": {
                    "type": "string",
                    "enum": ["banner", "finance", "sop"],
                    "description": "Routing source derived from intent",
                },
            },
            "required": ["message", "session_id", "source"],
        },
    },
]
```

### System Prompt

```python
INTERNAL_CHATBOT_SYSTEM = """You are Ask Banner, an internal knowledge assistant for
IT staff, functional analysts, and Banner administrators. You answer questions about
Ellucian Banner ERP — release notes, module changes, upgrade procedures, and SOPs.

Workflow for every user message:
1. Call chat_intent to classify the message and get a confidence score.
2. Call chat_sentiment to detect frustration.
3. Map intent to source:
   - BannerFinance → source=finance
   - SopQuery      → source=sop
   - BannerRelease, BannerAdmin, General → source=banner
4. Call chat_ask with the message, session_id, and source.
5. If resp.escalate == true OR sentiment == Frustrated:
   Reply: "I wasn't able to find a confident answer. Please check the source documents
   directly or escalate to the Banner team lead."
   Do not attempt to answer further.
6. Otherwise: deliver the answer concisely. Always cite the document title, page, and
   version if available. Technical accuracy matters more than brevity.

Rules:
- Never answer from prior training knowledge alone — always call chat_ask first.
- If the question is unrelated to Banner or SOPs, reply: "I can only help with Banner
  ERP questions and SOP procedures."
- Do not expose source URLs, API calls, or internal routing details.
"""
```

### Implementation

```python
import os
import anthropic

ADAPTER_BASE_URL = os.environ["ADAPTER_BASE_URL"]  # e.g. http://localhost:8080

ADAPTER_HEADERS = {"Content-Type": "application/json"}

client = anthropic.Anthropic()

def run_adapter_tool(tool_name: str, tool_input: dict) -> str:
    dispatch = {
        "chat_intent":    lambda i: requests.post(f"{ADAPTER_BASE_URL}/chat/intent", json=i, headers=ADAPTER_HEADERS).json(),
        "chat_sentiment": lambda i: requests.post(f"{ADAPTER_BASE_URL}/chat/sentiment", json=i, headers=ADAPTER_HEADERS).json(),
        "chat_ask":       lambda i: requests.post(f"{ADAPTER_BASE_URL}/chat/ask", json=i, headers=ADAPTER_HEADERS).json(),
    }
    return json.dumps(dispatch[tool_name](tool_input))

def internal_chatbot_agent(message: str, session_id: str) -> str:
    messages = [{"role": "user", "content": message}]

    while True:
        response = client.messages.create(
            model="claude-haiku-4-5-20251001",
            max_tokens=512,
            system=INTERNAL_CHATBOT_SYSTEM,
            tools=INTERNAL_CHATBOT_TOOLS,
            messages=messages,
        )

        if response.stop_reason == "end_turn":
            return next(b.text for b in response.content if b.type == "text")

        tool_results = []
        for block in response.content:
            if block.type == "tool_use":
                inp = dict(block.input)
                if block.name == "chat_ask":
                    inp["session_id"] = session_id
                result = run_adapter_tool(block.name, inp)
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": block.id,
                    "content": result,
                })

        messages.append({"role": "assistant", "content": response.content})
        messages.append({"role": "user", "content": tool_results})
```

### Example Session

```
User: What database changes are required for Banner General 9.3.37?

Agent: [calls chat_intent("What database changes are required for Banner General 9.3.37?")]
       → {intent: "BannerRelease", confidence: 0.90}
       [calls chat_sentiment("...")]
       → {sentiment: "Neutral", score: 0.05}
       [calls chat_ask(message="...", session_id="abc", source="banner")]
       → {answer: "Banner General 9.3.37 requires running GENUPGRADE.sql before...",
          confidence: 0.82, escalate: false}

Answer: Banner General 9.3.37 requires the following database changes before restarting:
1. Run GENUPGRADE.sql (DBA checklist, page 4)
2. Update three indexes on the GZRSTAT table
Confidence: 0.82 — Source: Banner General Release Notes 9.3.37, page 4
```

```
User: Is there an SOP for the post-upgrade smoke test?

Agent: [calls chat_intent → {intent: "SopQuery", confidence: 0.85}]
       [calls chat_ask(source="sop")]
       → {answer: "SOP122 covers the post-upgrade smoke test...", confidence: 0.81}

Answer: Yes — SOP122 (Smoke Test and Sanity Test Post Banner Upgrade) covers this.
Section 3 has the full checklist. (Source: SOP122, Section 3, confidence: 0.81)
```

### Escalation Example

```
User: THE INDEX IS BROKEN NOTHING RETURNS RESULTS THIS IS USELESS

Agent: [calls chat_sentiment → {sentiment: "Frustrated", score: 0.94}]
       [skips answering due to frustration]

Answer: I wasn't able to find a confident answer. Please check the source documents
directly or escalate to the Banner team lead.
```

---

## Agent 9: Confidence Calibration Agent

**Purpose:** Determine the correct escalate score floor for `internal/adapter/client.go` by
running a structured calibration protocol against the live backend. Re-run whenever the index
grows significantly (new Banner versions ingested) since score distributions shift with corpus size.

**Use case:** After a new ingestion, or whenever the escalate behavior seems wrong, run this agent
to get a data-driven threshold recommendation instead of guessing.

**Type:** Diagnostic protocol agent. Sequential tool calls with structured output.

### Tools

Reuses Agent 7's tools: `banner_ask` and `index_stats`.

```python
CALIBRATION_TOOLS = [
    {
        "name": "index_stats",
        "description": "Get current index chunk count and metadata.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "banner_ask",
        "description": "Run a test query and return sources with scores.",
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {"type": "string"},
                "module_filter": {"type": "string"},
                "top_k": {"type": "integer", "default": 3},
            },
            "required": ["question"],
        },
    },
]
```

### Calibration Query Sets

```python
# Known-good queries: these SHOULD return useful answers if the index is populated
KNOWN_GOOD = [
    {"question": "What changed in Banner General?", "module_filter": "General"},
    {"question": "What are the Banner General release notes?", "module_filter": "General"},
    {"question": "What is new in Banner 9.3.37.2?", "module_filter": "General"},
    {"question": "What support changes were made in Banner 8?", "module_filter": "General"},
    {"question": "What are the breaking changes in the Banner General release?", "module_filter": "General"},
]

# Known-boundary queries: these SHOULD have low or zero results
KNOWN_BOUNDARY = [
    {"question": "What changed in Banner Finance module?", "module_filter": "Finance"},
    {"question": "Banner Student 9.4.1 release notes", "module_filter": "Student"},
    {"question": "Banner HR payroll changes", "module_filter": "HR"},
    {"question": "What is the weather today?", "module_filter": "General"},
    {"question": "Who is the CEO of Ellucian?", "module_filter": "General"},
]
```

### System Prompt

```python
CALIBRATION_SYSTEM = """You are a confidence score calibration agent for an Azure AI Search-backed
RAG system. Your goal is to determine the correct escalate threshold for the adapter layer.

Calibration protocol:
1. Call index_stats to record the current index state (chunk count, index name).
2. Run each query in KNOWN_GOOD (passed as context) using banner_ask. Record:
   - retrieval_count
   - sources[0].score (or 0 if sources is empty)
   - Whether the answer text appears useful (not empty, not "no documents found")
3. Run each query in KNOWN_BOUNDARY. Record the same fields.
4. Analyze the data:
   - What is the MINIMUM score among known-good queries with useful answers?
   - What is the MAXIMUM score among known-boundary queries with useless/empty answers?
   - Is there a clean gap between those two groups?
5. Recommend a threshold:
   - If gap exists: set floor = midpoint of the gap, rounded down to nearest 0.005
   - If no gap (scores overlap): recommend threshold = 0.0 (use retrieval_count == 0 only)
   - Never recommend above 0.05 without strong justification

Output a Markdown calibration report:
## Calibration Report — [date] — [index_name]
### Index State
[chunk count, index name]
### Known-Good Query Results
| Query | module | retrieval_count | score | useful? |
### Known-Boundary Query Results
[same table]
### Score Distribution Analysis
[min good score, max boundary score, gap or overlap]
### Recommendation
**Threshold:** 0.0XX
**Rationale:** [1–2 sentences]
**Next calibration:** [recommend after N more documents ingested or N months]
"""
```

### Implementation

```python
def calibration_agent(module_filter: str = "General") -> str:
    context = f"""
Run the calibration protocol for module_filter='{module_filter}'.

KNOWN_GOOD queries:
{json.dumps(KNOWN_GOOD, indent=2)}

KNOWN_BOUNDARY queries:
{json.dumps(KNOWN_BOUNDARY, indent=2)}

Produce a full calibration report as described in your instructions.
"""
    messages = [{"role": "user", "content": context}]

    while True:
        response = client.messages.create(
            model="claude-haiku-4-5-20251001",
            max_tokens=2048,
            system=CALIBRATION_SYSTEM,
            tools=CALIBRATION_TOOLS,
            messages=messages,
        )

        if response.stop_reason == "end_turn":
            return next(b.text for b in response.content if b.type == "text")

        tool_results = []
        for block in response.content:
            if block.type == "tool_use":
                result = run_tool(block.name, dict(block.input))
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": block.id,
                    "content": result,
                })
        messages.append({"role": "assistant", "content": response.content})
        messages.append({"role": "user", "content": tool_results})
```

### Example Output

```
## Calibration Report — 2026-04-16 — banner-upgrade-knowledge

### Index State
- Chunks: 1,247  Index: banner-upgrade-knowledge

### Known-Good Query Results
| Query | module | retrieval_count | score | useful? |
|-------|--------|----------------|-------|---------|
| What changed in Banner General? | General | 3 | 0.033 | yes |
| Banner General release notes? | General | 5 | 0.029 | yes |
| What is new in 9.3.37.2? | General | 4 | 0.041 | yes |
| Support changes in Banner 8? | General | 2 | 0.027 | yes |
| Breaking changes in Banner General? | General | 3 | 0.035 | yes |

### Known-Boundary Query Results
| Query | module | retrieval_count | score | useful? |
|-------|--------|----------------|-------|---------|
| Banner Finance module? | Finance | 0 | 0.000 | no |
| Banner Student 9.4.1? | Student | 0 | 0.000 | no |
| Banner HR payroll? | HR | 0 | 0.000 | no |
| What is the weather today? | General | 1 | 0.004 | no |
| Who is the CEO of Ellucian? | General | 1 | 0.006 | no |

### Score Distribution Analysis
- Min good score: 0.027  Max boundary score: 0.006
- Clear gap exists: 0.006 → 0.027 (gap of 0.021)
- Gap midpoint: 0.016

### Recommendation
**Threshold:** 0.010
**Rationale:** Clean gap between useful answers (≥ 0.027) and noise (≤ 0.006).
Threshold of 0.010 sits safely in the middle with margin. Rounds down to nearest 0.005.
**Next calibration:** After 500+ new chunks ingested, or if escalate behavior seems off.
```

---

## Agent 10: Banner User Guide Navigation Agent

**Purpose:** Answer "how to use Banner" questions for IT staff and functional analysts.
Routes to the indexed Banner user guide PDFs (`source_type=banner_user_guide`) — not release
notes. Covers form navigation, field meanings, workflow steps, and UI lookups. No version or
year filter — user guides are not versioned.

**Use case:** An IT analyst asks "How do I run a journal entry in Banner Finance?" or
"Where is the student search form?" — this agent finds the answer in the Ellucian user guide
PDF rather than release notes or SOPs.

**Type:** Simple tool-use agent (1–2 tool calls). Uses the `user_guide` source routing
added in Phase E. Module auto-detected from question context.

**Available user guide modules (as of 2026-04-16):**
- `general` — Banner General - Use - Ellucian.pdf
- `student` — Banner Student - Use - Ellucian.pdf
- `finance` — PDFs TBD (route returns 0 results until ingested)

### Tools

```python
USER_GUIDE_TOOLS = [
    {
        "name": "user_guide_ask",
        "description": (
            "Search the Ellucian Banner user guide PDFs and answer a question about how to "
            "USE Banner ERP — navigation, forms, screens, fields, lookups, workflows. "
            "These are functional user guides, NOT release notes or SOPs. "
            "Set module to 'general' for Banner General forms, 'student' for student records, "
            "'finance' for Finance module. "
            "Do NOT set version_filter or year_filter — user guides carry no version metadata."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {"type": "string", "description": "The how-to question to answer"},
                "module": {
                    "type": "string",
                    "enum": ["general", "student", "finance"],
                    "description": "Banner module — determines which user guide to search",
                    "default": "general",
                },
                "top_k": {"type": "integer", "description": "Number of source chunks", "default": 5},
            },
            "required": ["question"],
        },
    }
]
```

### System Prompt

```python
USER_GUIDE_SYSTEM = """You are a Banner ERP navigation assistant for IT staff and functional analysts.
Answer questions about how to USE Ellucian Banner ERP — navigating forms, entering records,
running queries, and understanding field meanings. Use the user_guide_ask tool.

Rules:
- Always call user_guide_ask before answering. Never answer from prior knowledge alone.
- Detect module from the user's question:
    * "finance", "journal", "budget", "GL", "fund code" → module=finance
    * "student", "registration", "enrollment", "applicant" → module=student
    * Default (navigation, general forms, Banner menus) → module=general
- Do NOT set version_filter or year_filter. User guides have no version metadata.
- If retrieval_count == 0, say "I couldn't find that in the Banner user guide. The relevant
  module may not be indexed yet. Try checking the Ellucian documentation portal directly."
- Cite the section heading and page number from the source chunks.
- Keep answers procedural: numbered steps when describing a workflow.
- If the question is about release notes or upgrades, redirect: "That sounds like a release
  note question — try asking without 'how to' for release history."
- Note: Azure AI Search scores for this index are typically 0.01–0.05 — a score of 0.033
  is a valid answer, not low confidence."""
```

### Implementation

```python
def user_guide_agent(question: str, session_id: str = "default") -> str:
    """Answer how-to questions from the Banner user guide index."""

    def run_user_guide_tool(tool_input: dict) -> str:
        module = tool_input.get("module", "general")
        body = {
            "question": tool_input["question"],
            "source_type": "banner_user_guide",
            "top_k": tool_input.get("top_k", 5),
            # Deliberately no version_filter or year_filter
        }
        result = call_api("POST", f"/banner/{module}/ask", body)
        return json.dumps(result)

    messages = [{"role": "user", "content": question}]

    while True:
        response = client.messages.create(
            model="claude-haiku-4-5-20251001",
            max_tokens=1024,
            system=USER_GUIDE_SYSTEM,
            tools=USER_GUIDE_TOOLS,
            messages=messages,
        )

        if response.stop_reason == "end_turn":
            return next(b.text for b in response.content if b.type == "text")

        tool_results = []
        for block in response.content:
            if block.type == "tool_use":
                result = run_user_guide_tool(dict(block.input))
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": block.id,
                    "content": result,
                })

        messages.append({"role": "assistant", "content": response.content})
        messages.append({"role": "user", "content": tool_results})
```

### Example Session

```
User: How do I run a journal entry voucher in Banner Finance?

Agent: [calls user_guide_ask(question="How do I run a journal entry voucher?", module="finance")]
       → {answer: "To process a journal entry in Banner Finance, navigate to FGAJVCD...",
          retrieval_count: 4, sources[0].score: 0.038}

Answer: To process a journal entry voucher in Banner Finance:
1. Navigate to the Journal Voucher Entry form (FGAJVCD)
2. Enter the fund and org codes in the key block...
Source: Banner Finance - Use - Ellucian.pdf, Section "Journal Entry Processing", page 47
```

```
User: Where is the student name search in Banner?

Agent: [calls user_guide_ask(question="Where is the student name search?", module="student")]
       → {answer: "Use the Person Search (SOAIDEN) form to search for students by name...",
          retrieval_count: 3}

Answer: Use the Person Search form (SOAIDEN) to find students by name.
Enter the last name in the ID/Name Search field and press F7 to execute.
Source: Banner Student - Use - Ellucian.pdf, Section "Searching for Records", page 12
```

### Contrast with Other Agents

| Question type | Use agent | Source |
|---|---|---|
| "What changed in Banner 9.3.37?" | Agent 1 or Agent 8 | Release notes (`banner`) |
| "How to restart the Banner server" | Agent 8 (SopQuery) | SOPs (`sop`) |
| **"How do I enter a journal entry?"** | **Agent 10** | **User guide (`banner_user_guide`)** |
| "What new features came in Banner Finance?" | Agent 1 | Release notes (`banner`) |

---

## Agent 11: Banner Student Wizard Conductor

**Purpose:** Drive Banner Student guided workflow sessions for Botpress. Agent 11 is the
human-facing wizard conductor — it receives a user's message each turn, decides whether
the user is answering a prompt or asking a clarifying question, and calls `POST /student/wizard`
to advance (or look up) accordingly. All session state lives server-side in the adapter.
Botpress stays as a thin presentation layer.

**Use case:** A registrar staff member types "I want to create a new course" — Agent 11
identifies the wizard, generates a `session_id`, and guides the user step-by-step through all
14 SCACRSE fields, rendering picklists as Quick Reply buttons. If the user asks "what does
effective term mean?" mid-wizard, the agent sends it as a `question` (not a `value`), gets the
citesearch answer, and resumes the same step.

**Type:** Stateful multi-turn agent. One `session_id` per conversation, reused every turn.

### Tools

```python
WIZARD_TOOLS = [
    {
        "name": "wizard_step",
        "description": (
            "Advance or look up one turn in a Banner Student wizard session. "
            "On first call: set wizard to the target wizard name, leave step/value/question empty. "
            "To submit a field answer: set step (current step name) and value (user's answer). "
            "To handle a clarifying question mid-step: set step and question instead of value. "
            "Returns prompt, next_step, options (picklist), or answer (escape hatch). "
            "When next_step is 'review': display the review summary and await confirm/restart/edit."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "session_id": {"type": "string", "description": "Stable session ID for this conversation"},
                "wizard":     {"type": "string", "description": "Wizard name — required on init only (e.g. 'course_creation')"},
                "step":       {"type": "string", "description": "Current step being answered (empty on init)"},
                "value":      {"type": "string", "description": "User's field answer (mutually exclusive with question)"},
                "question":   {"type": "string", "description": "User's clarifying question — triggers citesearch lookup"},
            },
            "required": ["session_id"],
        },
    }
]
```

### Wizard Trigger Phrases

| User says | Launch wizard |
|---|---|
| "create a course", "new course", "add a course", "SCACRSE" | `course_creation` |
| *(Phase 2–4 wizards added as they are built)* | |

### System Prompt

```python
WIZARD_CONDUCTOR_SYSTEM = """You are a Banner Student operations assistant for IT staff,
curriculum coordinators, and registrar staff. You guide users step-by-step through
Banner Student workflows using structured wizards.

## Wizard session lifecycle

1. Detect which wizard the user wants from their opening message (see trigger phrases below).
2. Generate a UUID session_id once at conversation start. Reuse it every turn.
3. Init the session: call wizard_step with session_id + wizard only (no step/value/question).
4. Each subsequent turn: call wizard_step with session_id + step + either value OR question.
5. Continue until next_step == "review" — display the review summary and await user action.

## Wizard trigger phrases
- course_creation: "create a course", "new course", "add a course", "SCACRSE"
(additional wizards added here as they are built)

## Question vs. value detection (decide before calling wizard_step)
Populate the `question` field (not `value`) when the user's input:
  - Ends with "?"
  - Starts with: what, how, why, when, where, can, is, are, does, should
Otherwise: populate `value` with the user's literal input.

## Picklist fields
When the response includes `options`, the user must choose from that list.
Render options as Quick Reply buttons in Botpress. Reject free-text answers not in the list —
wizard_step will return valid:false with the options list again.

## Review node (next_step == "review")
Display the review summary from resp.review.fields in a readable table.
Present three choices:
  - "confirm" → session complete, show final summary card
  - "restart" → session cleared, offer to start over
  - "edit <field_name>" → jump back to that field (send as "edit:<field_name>")

## Escalation
- 404 response: session not found (expired or never created). Tell the user and offer to restart.
- Never answer Banner ERP questions from prior training knowledge. Use wizard_step for all workflow steps.
- If the user asks something unrelated to Banner Student operations, reply:
  "I can help with Banner Student workflows. Would you like to create a course or start another wizard?"
"""
```

### Implementation

```python
import os
import uuid
import anthropic
import requests
import json

ADAPTER_BASE_URL = os.environ["ADAPTER_BASE_URL"]  # e.g. http://localhost:8080

client = anthropic.Anthropic()

def run_wizard_tool(tool_input: dict) -> str:
    resp = requests.post(
        f"{ADAPTER_BASE_URL}/student/wizard",
        json=tool_input,
        headers={"Content-Type": "application/json"},
    )
    if resp.status_code == 404:
        return json.dumps({"error": "session_not_found"})
    resp.raise_for_status()
    return resp.text  # already JSON

def wizard_conductor_agent(user_message: str, session_id: str | None = None) -> tuple[str, str]:
    """
    Drive one turn of a wizard conversation.
    Returns (assistant_reply, session_id) — caller persists session_id across turns.
    """
    if session_id is None:
        session_id = str(uuid.uuid4())

    messages = [{"role": "user", "content": user_message}]

    # Inject session_id into context so the model always uses it
    system = WIZARD_CONDUCTOR_SYSTEM + f"\n\nCurrent session_id: {session_id}"

    while True:
        response = client.messages.create(
            model="claude-haiku-4-5-20251001",
            max_tokens=512,
            system=system,
            tools=WIZARD_TOOLS,
            messages=messages,
        )

        if response.stop_reason == "end_turn":
            reply = next(b.text for b in response.content if b.type == "text")
            return reply, session_id

        tool_results = []
        for block in response.content:
            if block.type == "tool_use":
                result = run_wizard_tool(dict(block.input))
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": block.id,
                    "content": result,
                })

        messages.append({"role": "assistant", "content": response.content})
        messages.append({"role": "user", "content": tool_results})
```

### Example Session

```
User: I want to create a new course.

Agent: [wizard_step(session_id="a1b2...", wizard="course_creation")]
       → {next_step:"subject_code", prompt:"What is the subject code? (e.g. COMP, MATH, ENGL)"}

Reply: Let's create a new course. First — what is the subject code? (e.g. COMP, MATH, ENGL)

---

User: COMP

Agent: [wizard_step(session_id="a1b2...", step:"subject_code", value:"COMP")]
       → {valid:true, next_step:"course_number", prompt:"What is the course number? (e.g. 3850, 101H)"}

Reply: Got it — COMP. What is the course number? (e.g. 3850, 101H)

---

User: what does course number mean exactly?

Agent: [wizard_step(session_id="a1b2...", step:"course_number", question:"what does course number mean?")]
       → {answer:"Course numbers in Banner are 1–6 alphanumeric characters...",
          resume_step:"course_number", prompt:"Got it — back to: What is the course number?"}

Reply: Course numbers in Banner are 1–6 alphanumeric characters identifying the course within a subject.
Back to: What is the course number for this course? (e.g. 3850, 101H)

---

User: 3850

Agent: [wizard_step(session_id:"a1b2...", step:"course_number", value:"3850")]
       → {valid:true, next_step:"short_title", ...}

[... steps continue ...]

---

User: UG   ← selected from Quick Reply button for course_level

Agent: [wizard_step(session_id:"a1b2...", step:"course_level", value:"UG")]
       → {valid:true, next_step:"schedule_type", options:["Lecture","Lab","Seminar","Online","Hybrid"], ...}

Reply: Course level set to Undergraduate. What is the schedule type?
[Quick Reply buttons: Lecture | Lab | Seminar | Online | Hybrid]

---

[After all steps collected:]

Agent: [wizard_step(session_id:"a1b2...", step:"status", value:"Active")]
       → {valid:true, next_step:"review", review:{fields:{subject_code:"COMP", ...}, message:"Here's what I have..."}}

Reply: Here's what I have for this course:
| Field | Value |
|---|---|
| Subject Code | COMP |
| Course Number | 3850 |
| Short Title | Software Engineering |
| Credit Hours | 3 |
| Course Level | UG |
| Schedule Type | Lecture |
| Grading Mode | Standard |
| College | AS |
| Department | COMP |
| Effective Term | 202510 |
| Status | Active |

Does everything look correct? Reply **confirm**, **restart**, or **edit <field>** to change a field.

---

User: confirm

Agent: [wizard_step(session_id:"a1b2...", step:"review", value:"confirm")]
       → {session:{complete:true}, review:{fields:{...}}}

Reply: Course created successfully. Here is your summary — forward this to the Banner admin for entry in SCACRSE:
COMP 3850 — Software Engineering | UG | Lecture | 3 credits | Spring 2025 | Active
```

---

## Agent 12: Wizard Test Harness Agent

**Purpose:** Automated end-to-end validation of wizard step sequences. No human in the loop.
Given a wizard name and its step sequence, Agent 12 drives two complete passes:

1. **Golden path** — submits a known-valid value for every step; every step should return `valid:true`.
2. **Error path** — for each step with validation rules, submits one known-invalid value; every
   such step should return `valid:false` with a descriptive error message.

Produces a Markdown test report table. Use this as a pre-merge check whenever a new
wizard step sequence is added to `internal/wizard/catalog.go`.

**Use case:** A developer adds `modify_course` steps to `catalog.go` and registers the wizard.
Before merging, they run Agent 12 (`python agents/wizard_harness.py modify_course`) and verify
all cells in the report are PASS.

**Type:** Automated test agent. Not interactive. Runs to completion and returns the report.

### Tools

```python
# Reuses WIZARD_TOOLS from Agent 11 — same wizard_step tool, same dispatcher.
```

### Test Value Map for `course_creation`

Embed this in the system prompt as context so the agent drives the right inputs.

```python
COURSE_CREATION_TEST_CASES = [
    # step, valid_input, invalid_input, why_invalid
    ("subject_code",  "COMP",              "comp",          "lowercase not allowed"),
    ("course_number", "3850",              "",              "empty not allowed"),
    ("short_title",   "Software Eng",      "A" * 31,        "exceeds 30 char max"),
    ("long_title",    "skip",              "A" * 101,       "exceeds 100 char max"),
    ("credit_hours",  "3",                 "0",             "zero not allowed"),
    ("course_level",  "UG",               "GRADUATE",      "not in options list"),
    ("schedule_type", "Lecture",           "Workshop",      "not in options list"),
    ("grading_mode",  "Standard",          "Honors",        "not in options list"),
    ("college",       "AS",               "a",             "lowercase not allowed"),
    ("department",    "COMP",             "c",             "lowercase not allowed"),
    ("prerequisites", "none",             None,            None),  # optional, skip error path
    ("corequisites",  "none",             None,            None),  # optional, skip error path
    ("effective_term","202510",           "202540",        "invalid term suffix"),
    ("status",        "Active",           "Done",          "not in options list"),
]
```

### System Prompt

```python
WIZARD_HARNESS_SYSTEM = """You are an automated test harness for Banner Student wizard step sequences.
Your job is to validate a wizard's golden path and error path with no human interaction.

## Protocol

Given a wizard name and test case table (provided in user message), run two passes:

### Pass 1 — Golden Path
1. Init a fresh session: wizard_step(session_id=<new_uuid>, wizard=<wizard_name>)
2. For each step in the test table (in order): submit the valid_input value.
3. Assert resp.valid == true for each step. Record PASS or FAIL.
4. Continue until next_step == "review". Do NOT confirm — stop after reaching review.

### Pass 2 — Error Path
For each test case where invalid_input is not None:
1. Init a fresh session (new UUID each time, or reuse if session is still at the same step).
   Simpler: start a fresh session and advance to the target step using golden path values,
   then submit the invalid input at that step.
2. Assert resp.valid == false. Assert resp.prompt contains an error message (non-empty).
3. Record PASS or FAIL.

## Output format

### Golden Path Results
| Step | Input | valid | next_step | PASS/FAIL |
|---|---|---|---|---|
| subject_code | COMP | true | course_number | PASS |
...

### Error Path Results
| Step | Invalid Input | Why | valid | prompt_has_error | PASS/FAIL |
|---|---|---|---|---|---|
| subject_code | comp | lowercase | false | yes | PASS |
...

### Summary
- Golden path: X/Y steps PASS
- Error path: X/Y steps PASS
- CRITICAL FAILURES (valid:true on invalid input): [list or "none"]

CRITICAL: A valid:true response on a known-invalid input is a critical failure — flag it prominently.
A valid:false response on a known-valid input is also a critical failure.
"""
```

### Implementation

```python
import os
import uuid
import json
import anthropic
import requests

ADAPTER_BASE_URL = os.environ["ADAPTER_BASE_URL"]

client = anthropic.Anthropic()

def run_wizard_tool(tool_input: dict) -> str:
    resp = requests.post(
        f"{ADAPTER_BASE_URL}/student/wizard",
        json=tool_input,
        headers={"Content-Type": "application/json"},
    )
    resp.raise_for_status()
    return resp.text

def wizard_harness_agent(wizard_name: str, test_cases: list[tuple]) -> str:
    """Run golden + error path tests for a wizard. Returns Markdown test report."""
    context = f"""
Run the full test harness for wizard: '{wizard_name}'

Test cases:
{json.dumps([{"step": tc[0], "valid_input": tc[1], "invalid_input": tc[2], "why_invalid": tc[3]} for tc in test_cases], indent=2)}

Generate a new UUID for each session. Drive all steps in sequence.
Produce the full Markdown report as specified in your instructions.
"""
    messages = [{"role": "user", "content": context}]

    while True:
        response = client.messages.create(
            model="claude-haiku-4-5-20251001",
            max_tokens=4096,
            system=WIZARD_HARNESS_SYSTEM,
            tools=WIZARD_TOOLS,
            messages=messages,
        )

        if response.stop_reason == "end_turn":
            return next(b.text for b in response.content if b.type == "text")

        tool_results = []
        for block in response.content:
            if block.type == "tool_use":
                result = run_wizard_tool(dict(block.input))
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": block.id,
                    "content": result,
                })

        messages.append({"role": "assistant", "content": response.content})
        messages.append({"role": "user", "content": tool_results})

if __name__ == "__main__":
    report = wizard_harness_agent("course_creation", COURSE_CREATION_TEST_CASES)
    print(report)
```

### Example Output

```markdown
## Wizard Test Harness Report — course_creation — 2026-04-21

### Golden Path Results
| Step | Input | valid | next_step | PASS/FAIL |
|---|---|---|---|---|
| subject_code | COMP | true | course_number | PASS |
| course_number | 3850 | true | short_title | PASS |
| short_title | Software Eng | true | long_title | PASS |
| long_title | skip | true | credit_hours | PASS |
| credit_hours | 3 | true | course_level | PASS |
| course_level | UG | true | schedule_type | PASS |
| schedule_type | Lecture | true | grading_mode | PASS |
| grading_mode | Standard | true | college | PASS |
| college | AS | true | department | PASS |
| department | COMP | true | prerequisites | PASS |
| prerequisites | none | true | corequisites | PASS |
| corequisites | none | true | effective_term | PASS |
| effective_term | 202510 | true | status | PASS |
| status | Active | true | review | PASS |

### Error Path Results
| Step | Invalid Input | Why | valid | prompt_has_error | PASS/FAIL |
|---|---|---|---|---|---|
| subject_code | comp | lowercase | false | yes | PASS |
| course_number | (empty) | empty not allowed | false | yes | PASS |
| short_title | AAAA...(31) | exceeds 30 chars | false | yes | PASS |
| long_title | AAAA...(101) | exceeds 100 chars | false | yes | PASS |
| credit_hours | 0 | zero not allowed | false | yes | PASS |
| course_level | GRADUATE | not in options | false | yes | PASS |
| schedule_type | Workshop | not in options | false | yes | PASS |
| grading_mode | Honors | not in options | false | yes | PASS |
| college | a | lowercase | false | yes | PASS |
| department | c | lowercase | false | yes | PASS |
| effective_term | 202540 | invalid term suffix | false | yes | PASS |
| status | Done | not in options | false | yes | PASS |

### Summary
- Golden path: 14/14 PASS
- Error path: 12/12 PASS
- CRITICAL FAILURES: none
```

### Contrast with Other Agents

| Agent | Audience | Interaction mode | Output |
|---|---|---|---|
| Agent 11 — Wizard Conductor | Banner staff via Botpress | Human-in-the-loop, multi-turn | Conversational guided wizard |
| **Agent 12 — Test Harness** | **Developer pre-merge** | **Automated, no human** | **Markdown test report** |

---

## Agent 13: Query Rewriting Agent

**Purpose:** Expand terse or ambiguous Banner ERP questions before routing to citesearch.
Uses Claude to add synonyms, expand acronyms (GL→General Ledger, AR→Accounts Receivable),
and add module context. Improves retrieval for single-word or vague questions. Deployed as
a preprocessing step inside the Go adapter (Phase H) and available standalone for testing.

**Use case:** A staff member types "GL changes" — Agent 13 rewrites it to "What changed in
the Banner Finance General Ledger module in recent releases?" before the citesearch call.
Also improves wizard escape hatch lookups (e.g. "what is effective term?" at the wizard step).

**Type:** Single-turn transformation agent. One tool call, one structured response.

**Java AI-ROADMAP origin:** Phase 5A — Query Rewriting for AI Reporter (ported to Go adapter).

### Tools

```python
QUERY_REWRITER_TOOLS = [
    {
        "name": "query_expand",
        "description": (
            "Expand a terse Banner ERP question into a more specific, retrievable form. "
            "Provide the original question and the detected intent. "
            "Returns the rewritten question and a changed flag."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {"type": "string", "description": "Original user question"},
                "intent":   {"type": "string", "description": "Detected intent: BannerRelease, BannerFinance, SopQuery, BannerAdmin, BannerUsage, General"},
                "context":  {"type": "string", "description": "Optional wizard step name for escape hatch queries"},
            },
            "required": ["question", "intent"],
        },
    }
]
```

### System Prompt

```python
QUERY_REWRITER_SYSTEM = """You are a query expansion assistant for an Ellucian Banner ERP knowledge base.
When asked to expand a question, rewrite it to be more specific and retrievable.

Expansion rules by intent:
- BannerFinance: expand GL→General Ledger, AR→Accounts Receivable, AP→Accounts Payable, budget codes
- BannerRelease: add "release notes" or "changelog" context if missing
- SopQuery: add "procedure" or "step-by-step" context
- BannerAdmin: add "configuration" or "setup" context
- BannerUsage: add "Banner ERP form" or "navigate" context

General rules:
- Keep the rewrite under 2 sentences
- If the question is already specific, return it unchanged
- Respond with ONLY the rewritten question. No explanation, no prefix.
"""
```

### Implementation

```python
def run_query_rewriter_tool(tool_input: dict) -> str:
    """Standalone: call Claude directly to expand the question."""
    question = tool_input["question"]
    intent = tool_input.get("intent", "General")
    context = tool_input.get("context", "")

    context_note = f"\nWizard step context: {context}" if context else ""
    user_msg = f"Intent: {intent}{context_note}\nQuestion: {question}\n\nExpand this question:"

    response = client.messages.create(
        model="claude-haiku-4-5-20251001",
        max_tokens=256,
        system=QUERY_REWRITER_SYSTEM,
        messages=[{"role": "user", "content": user_msg}],
    )
    rewritten = next(b.text for b in response.content if b.type == "text").strip()
    changed = rewritten.lower() != question.lower()
    return json.dumps({"original": question, "rewritten": rewritten, "changed": changed})

def query_rewriter_agent(question: str, intent: str, context: str = "") -> dict:
    """Standalone invocation — returns {original, rewritten, changed}."""
    result = run_query_rewriter_tool({"question": question, "intent": intent, "context": context})
    return json.loads(result)
```

### Example

```
Input:  question="GL changes", intent="BannerFinance"
Output: {"original":"GL changes",
         "rewritten":"What changed in the Banner Finance General Ledger module in recent releases?",
         "changed":true}

Input:  question="What changed in Banner General 9.3.37.2?", intent="BannerRelease"
Output: {"original":"What changed in Banner General 9.3.37.2?",
         "rewritten":"What changed in Banner General 9.3.37.2?",
         "changed":false}
```

---

## Agent 14: Answer Grounding Verifier Agent

**Purpose:** Given a citesearch answer and its source document chunks, verify whether the
answer is fully supported by the sources. Catches hallucinated or overreaching answers before
they reach staff users. Deployed as a post-processing step in the Go adapter (Phase I) and
available standalone for manual auditing.

**Use case:** After Banner release note Q&A, the verifier flags answers that add details not
present in the retrieved chunks ("partial") or contradict the sources ("unsupported").

**Type:** Single-turn verification agent. Returns a structured verdict.

**Java AI-ROADMAP origin:** Phase 5B — Grounding Check / Hallucination Guard (ported to Go).

### Tools

```python
GROUNDING_TOOLS = [
    {
        "name": "grounding_check",
        "description": (
            "Verify whether an answer is supported by its source document chunks. "
            "Returns verdict: supported, partial, or unsupported with a short note."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {"type": "string"},
                "answer":   {"type": "string"},
                "sources":  {"type": "array", "items": {"type": "string"}, "description": "Source text chunks in retrieval order"},
            },
            "required": ["question", "answer", "sources"],
        },
    }
]
```

### System Prompt

```python
GROUNDING_SYSTEM = """You are a grounding verifier for a Banner ERP knowledge base chatbot.
Given a question, an answer, and source document chunks, determine if the answer is supported.

Respond with exactly ONE line — no other text:
- supported          (answer fully backed by sources)
- partial:<reason>   (answer partially backed; state concisely what is missing or added)
- unsupported:<reason> (answer contradicts or is entirely absent from sources)
"""
```

### Implementation

```python
def run_grounding_tool(tool_input: dict) -> str:
    question = tool_input["question"]
    answer   = tool_input["answer"]
    sources  = tool_input.get("sources", [])

    sources_text = "\n---\n".join(f"Source {i+1}: {s}" for i, s in enumerate(sources))
    user_msg = f"Question: {question}\n\nAnswer: {answer}\n\nSources:\n{sources_text}"

    response = client.messages.create(
        model="claude-haiku-4-5-20251001",
        max_tokens=128,
        system=GROUNDING_SYSTEM,
        messages=[{"role": "user", "content": user_msg}],
    )
    raw = next(b.text for b in response.content if b.type == "text").strip()
    parts = raw.split(":", 1)
    verdict = parts[0].strip()
    note    = parts[1].strip() if len(parts) > 1 else ""
    return json.dumps({"verdict": verdict, "note": note})

def grounding_agent(question: str, answer: str, sources: list[str]) -> dict:
    result = run_grounding_tool({"question": question, "answer": answer, "sources": sources})
    return json.loads(result)
```

### Example

```
Input:
  question = "What database changes are required for Banner General 9.3.37?"
  answer   = "Run GENUPGRADE.sql and update the GZRSTAT index. Also update Java to 21."
  sources  = ["Run GENUPGRADE.sql before restart. Update GZRSTAT index (page 4)."]

Output: {"verdict":"partial","note":"Java 21 requirement not mentioned in sources"}

---

Input:
  question = "What changed in Banner Finance?"
  answer   = "Banner Finance added a new accounts payable workflow."
  sources  = ["Banner Finance 9.3.37 updated the GL posting process."]

Output: {"verdict":"unsupported","note":"AP workflow not mentioned; sources discuss GL posting"}
```

---

## Agent 15: Analytics Advisor Agent

**Purpose:** Answer natural language questions about the `/chat/ask` usage log. Surfaces
escalation rates, cost trends, intent distribution, and grounding quality without requiring
manual jq queries. Reads from `GET /analytics/summary`.

**Use case:** A team lead asks "What's our escalation rate this week?" or "Which intent
escalates most often?" — Agent 15 queries the analytics endpoint and synthesizes a report.

**Type:** Multi-turn advisory agent. Calls analytics and optionally feedback endpoints.

**Java AI-ROADMAP origin:** Phase 5E — Ask Log & Analytics + Phase 5F — Feedback Loop (ported).

### Tools

```python
ANALYTICS_TOOLS = [
    {
        "name": "analytics_summary",
        "description": (
            "Get aggregate statistics from the ask log: total asks, escalation rate, "
            "avg confidence, breakdowns by intent and source, grounding distribution."
        ),
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "feedback_report",
        "description": "Get aggregate feedback statistics: helpful vs. unhelpful counts by intent and source.",
        "input_schema": {"type": "object", "properties": {}},
    },
]
```

### System Prompt

```python
ANALYTICS_ADVISOR_SYSTEM = """You are an analytics advisor for the Ask Banner chatbot.
Answer questions about usage quality using the analytics_summary and feedback_report tools.

Guidelines:
- Always call analytics_summary first before answering any usage question.
- Call feedback_report when the user asks about answer quality or thumbs-down patterns.
- Express escalation_rate as a percentage (e.g. 12%).
- Flag any intent with escalation_rate > 30% as needing attention.
- Note: Azure AI Search confidence scores are typically 0.01–0.05 for valid answers.
  avg_confidence below 0.01 indicates a retrieval problem, not a Claude problem.
- Present findings as bullet points. Lead with the most actionable insight.
"""
```

### Implementation

```python
ANALYTICS_ADAPTER_BASE_URL = os.environ["ADAPTER_BASE_URL"]

def run_analytics_tool(tool_name: str, _: dict) -> str:
    if tool_name == "analytics_summary":
        return json.dumps(call_api("GET", "/analytics/summary"))
    if tool_name == "feedback_report":
        return json.dumps(call_api("GET", "/analytics/feedback"))
    return json.dumps({"error": "unknown tool"})

def analytics_advisor_agent(question: str) -> str:
    messages = [{"role": "user", "content": question}]
    while True:
        response = client.messages.create(
            model="claude-haiku-4-5-20251001",
            max_tokens=1024,
            system=ANALYTICS_ADVISOR_SYSTEM,
            tools=ANALYTICS_TOOLS,
            messages=messages,
        )
        if response.stop_reason == "end_turn":
            return next(b.text for b in response.content if b.type == "text")
        tool_results = []
        for block in response.content:
            if block.type == "tool_use":
                result = run_analytics_tool(block.name, dict(block.input))
                tool_results.append({"type":"tool_result","tool_use_id":block.id,"content":result})
        messages.append({"role":"assistant","content":response.content})
        messages.append({"role":"user","content":tool_results})
```

### Example

```
User: What's our escalation rate and which intent is worst?

Agent: [calls analytics_summary]
       → {total_asks:150, escalation_rate:0.18, by_intent:{BannerRelease:60,SopQuery:40,BannerFinance:30,...}}

Answer:
- Overall escalation rate: **18%** across 150 asks
- Worst intent: **BannerFinance** — 43% escalation rate ⚠ (Finance documents may not be fully indexed)
- Best intent: **SopQuery** — 5% escalation rate ✓
- Avg confidence for non-escalated answers: 0.031 (healthy for this index)
- Recommendation: Run a diagnostic (Agent 7) on the Finance module index
```

---

## Agent 16: Feedback Pattern Analyst Agent

**Purpose:** Analyze accumulated thumbs-down feedback to identify which intents, sources, or
question patterns consistently produce unhelpful answers. Generates a prioritized list of
prompt or retrieval improvements. Designed to be run weekly by a team lead.

**Use case:** After a week of staff usage, run this agent to find out "why are Finance answers
getting thumbs down?" and get specific recommendations (e.g. "Finance index needs more release
note PDFs" or "BannerFinance intent keywords need refinement").

**Type:** Analytical report agent. Multi-tool, returns a Markdown report.

**Java AI-ROADMAP origin:** Phase 5F — Feedback Loop (ported to Go) + Phase 5B grounding patterns.

### Tools

```python
FEEDBACK_ANALYST_TOOLS = [
    {
        "name": "feedback_report",
        "description": "Get all feedback entries grouped by helpfulness, intent, and source.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "analytics_summary",
        "description": "Get overall usage stats to correlate with feedback patterns.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "banner_ask",
        "description": "Test a specific query to verify if retrieval is the root cause of thumbs-down.",
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {"type": "string"},
                "module_filter": {"type": "string"},
                "top_k": {"type": "integer", "default": 3},
            },
            "required": ["question"],
        },
    },
]
```

### System Prompt

```python
FEEDBACK_ANALYST_SYSTEM = """You are a feedback pattern analyst for the Ask Banner chatbot.
Your job is to identify WHY answers are receiving thumbs-down and recommend fixes.

Protocol:
1. Call feedback_report to get unhelpful answer patterns.
2. Call analytics_summary to correlate with escalation and confidence data.
3. Group thumbs-down by: intent, source, and any visible question pattern.
4. For the top 3 thumbs-down intents: call banner_ask with a representative question
   to verify if retrieval itself is the problem (low score, 0 results) or if it's a
   prompt/answer quality issue.
5. Produce a Markdown report:

## Feedback Analysis — [date]
### Thumbs-Down Distribution
[table: intent | count | % of total asks | likely cause]

### Root Cause Investigation
[For each tested query: question, retrieval_count, score, verdict: retrieval_gap or prompt_gap]

### Recommendations (prioritized)
1. [Highest impact fix]
2. ...

### Next Review
[Suggest when to re-run based on volume]
"""
```

### Implementation

```python
def feedback_analyst_agent() -> str:
    """Run the full feedback analysis. Returns a Markdown report string."""
    messages = [{"role": "user", "content": "Run a full feedback pattern analysis for this week."}]
    dispatch = {
        "feedback_report":  lambda i: call_api("GET", "/analytics/feedback"),
        "analytics_summary": lambda i: call_api("GET", "/analytics/summary"),
        "banner_ask":       lambda i: call_api("POST", "/banner/ask", i),
    }
    while True:
        response = client.messages.create(
            model="claude-sonnet-4-6",
            max_tokens=2048,
            system=FEEDBACK_ANALYST_SYSTEM,
            tools=FEEDBACK_ANALYST_TOOLS,
            messages=messages,
        )
        if response.stop_reason == "end_turn":
            return next(b.text for b in response.content if b.type == "text")
        tool_results = []
        for block in response.content:
            if block.type == "tool_use":
                result = json.dumps(dispatch[block.name](dict(block.input)))
                tool_results.append({"type":"tool_result","tool_use_id":block.id,"content":result})
        messages.append({"role":"assistant","content":response.content})
        messages.append({"role":"user","content":tool_results})
```

### Example Output

```markdown
## Feedback Analysis — 2026-04-21

### Thumbs-Down Distribution
| Intent | Thumbs-Down | % of Asks | Likely Cause |
|---|---|---|---|
| BannerFinance | 12 | 40% | Finance index may be incomplete |
| BannerUsage | 5 | 15% | User guide chunks too sparse |
| General | 2 | 5% | Vague catch-all questions |

### Root Cause Investigation
- "What are the Finance AR posting rules?" → retrieval_count=0, score=0 → **retrieval_gap**: Finance AR not indexed
- "How do I navigate Banner Finance forms?" → retrieval_count=2, score=0.008 → **retrieval_gap**: low score, answer thin
- "What changed in Banner Finance?" → retrieval_count=4, score=0.031 → **prompt_gap**: answer accurate but too terse

### Recommendations
1. **Ingest Finance AR release note PDFs** — root cause of 40% thumbs-down (Agent 5 task)
2. **Add more Finance user guide pages** — improves BannerUsage retrieval for Finance forms
3. **Improve BannerFinance answer verbosity** — scores are fine; prompt needs richer formatting

### Next Review
Re-run after Finance AR PDFs are ingested or after 50+ new feedback entries accumulate.
```

---

## Agent 17: PDF Ingest Preflight Auditor Agent

**Purpose:** Before triggering an expensive ingest run, validate that every document in
`data/docs/banner` or `data/docs/sop` is correctly placed, named, and extractable. Reports
metadata warnings, estimates ingestion time, and cross-references the live index to show which
files are likely new vs. already indexed.

**Use case:** An operator is about to ingest 15 new Banner release note PDFs. Instead of waiting
90 minutes only to find that half of them have wrong module tags, they run Agent 17 first.
The agent catches a misnamed folder, a scanned PDF, and two files missing version strings —
all before a single embedding call is made.

**Type:** One-shot report agent. Calls `ingest_dryrun` + `index_stats` + `debug_chunks`.
Returns a Markdown report.

**Requires:** Phase L.2 (dry-run mode) deployed on the backend.

**Current backend limitation:** Exact file-vs-index matching is not available yet. Agent 17 uses
the filename sample from `GET /debug/chunks` as a best-effort signal. If a filename is observed
there, mark it **likely indexed**; otherwise mark it **not observed** and treat it as a likely
candidate for ingest. This is advisory until a dedicated "list indexed files" endpoint exists.

**Recommended model:** `claude-haiku-4-5` — report generation from structured data, no reasoning required.

---

### Tools

```python
PREFLIGHT_TOOLS = [
    {
        "name": "ingest_dryrun",
        "description": (
            "Scan a docs folder for ingestable files and validate them without embedding or uploading. "
            "Returns per-file metadata (module, version, year, source_type, estimated chunks, warnings)."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "docs_path": {
                    "type": "string",
                    "description": "Path to the folder to scan, e.g. 'data/docs/banner'"
                },
            },
            "required": ["docs_path"],
        },
    },
    {
        "name": "index_stats",
        "description": "Get current index statistics: index name and total document count.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "debug_chunks",
        "description": (
            "Get a sample of indexed chunk metadata, including filenames, source types, modules, and versions. "
            "Use this as a best-effort signal for whether a file has already been seen in the index."
        ),
        "input_schema": {"type": "object", "properties": {}},
    },
]
```

### System Prompt

```python
PREFLIGHT_SYSTEM = """You are a PDF ingest preflight auditor for the citesearch Banner ERP knowledge base.

Your job is to assess the readiness of a document collection for ingestion and produce a clear,
actionable Markdown report before any embedding calls are made.

Protocol:
1. Call `ingest_dryrun` with the target docs_path to get the file list and metadata.
2. If `ingest_dryrun` returns zero files, produce a short empty-folder report and stop.
3. Call `index_stats` to get the current index size.
4. Call `debug_chunks` to get observed filenames from the live index.
5. Cross-reference rule: match by basename only.
   - If a file basename appears in `debug_chunks`, mark it `likely indexed`.
   - If it does not appear, mark it `not observed in debug sample`.
   - If `debug_chunks` is unavailable, mark status `index comparison unavailable` and continue.
6. Identify files WITH warnings (missing module, missing version, scanned PDF, SOP naming mismatch).
7. Produce the report in this exact structure:

## Preflight Report — [docs_path] — [date]

### Summary
- Files found: N
- Likely new / not observed: N
- Likely indexed: N
- Files with warnings: N
- Estimated ingest time (clean, likely-new files only): N minutes
- Index comparison: available | unavailable

### File Status Table
| File | Index Status | Module | Version | Year | Source Type | Chunks (est.) | Warnings |
|------|--------------|--------|---------|------|-------------|---------------|----------|

(One row per file. Index Status must be one of: `likely indexed`, `not observed`, `comparison unavailable`.
Warnings column: `✓ clean` or comma-separated warning codes.)

### Warning Details
(Only include this section if any file has warnings)
For each file with warnings, explain the warning and the exact fix required.

### Action Plan
1. [Fix this warning first — blocks ingest of specific file]
2. [Ingest these files — no warnings, estimated time X min]
3. [Lower priority — already indexed or optional]

Rules:
- A missing module always blocks ingest (chunks will be unfindable by module_filter).
- A missing version on a release note PDF is a warning but not a blocker.
- A missing version on a user_guide PDF is expected and not a warning.
- A missing year on a release note PDF is a warning and should usually be fixed before ingest.
- A scanned PDF (0 pages extracted) must be pre-processed before ingest — do not include in time estimate.
- SOP naming mismatches mean the file will be silently skipped — always flag as a blocker.
- If `debug_chunks` or `index_stats` fails, say so explicitly and produce a preflight-only report rather than guessing.
"""
```

### Implementation

```python
def run_preflight_tool(tool_name: str, tool_input: dict) -> str:
    if tool_name == "ingest_dryrun":
        return json.dumps(call_api("POST", "/banner/ingest", {**tool_input, "dry_run": True}))
    if tool_name == "index_stats":
        return json.dumps(call_api("GET", "/index/stats"))
    if tool_name == "debug_chunks":
        return json.dumps(call_api("GET", "/debug/chunks"))
    return json.dumps({"error": "unknown tool"})


def observed_filenames(debug_chunks: list[dict]) -> set[str]:
    """Best-effort filename inventory from the debug chunk sample."""
    out = set()
    for row in debug_chunks:
        name = row.get("filename")
        if name:
            out.add(name)
    return out


def preflight_agent(docs_path: str) -> str:
    """Run a full preflight audit for the given docs_path. Returns a Markdown report."""
    messages = [
        {
            "role": "user",
            "content": f"Run a preflight audit for: {docs_path}"
        }
    ]
    while True:
        response = client.messages.create(
            model="claude-haiku-4-5-20251001",
            max_tokens=2048,
            system=PREFLIGHT_SYSTEM,
            tools=PREFLIGHT_TOOLS,
            messages=messages,
        )
        if response.stop_reason == "end_turn":
            return next(b.text for b in response.content if b.type == "text")
        tool_results = []
        for block in response.content:
            if block.type == "tool_use":
                result = run_preflight_tool(block.name, dict(block.input))
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": block.id,
                    "content": result,
                })
        messages.append({"role": "assistant", "content": response.content})
        messages.append({"role": "user", "content": tool_results})
```

### Example Output

```markdown
## Preflight Report — data/docs/banner — 2026-04-23

### Summary
- Files found: 4
- Likely new / not observed: 2
- Likely indexed: 2
- Files with warnings: 1
- Estimated ingest time (clean, likely-new files only): ~12 minutes
- Index comparison: available (`/debug/chunks` filename sample)

### File Status Table
| File | Index Status | Module | Version | Year | Source Type | Chunks (est.) | Warnings |
|------|--------------|--------|---------|------|-------------|---------------|----------|
| Banner_General_Release_Notes_9.3.37.2_February_2026.pdf | likely indexed | General | 9.3.37.2 | 2026 | banner | 70 | ✓ clean |
| Banner_Finance_Release_Notes_9.3.22.pdf | not observed | Finance | 9.3.22 | — | banner | 55 | ⚠ year not detected |
| Banner General - Use - Ellucian.pdf | likely indexed | General | — | — | banner_user_guide | 900 | ✓ clean (user guide — no version expected) |
| SOP154_Procedure_Axiom.docx | not observed | — | — | — | sop | — | ❌ SOP naming mismatch — will be skipped |

### Warning Details

**Banner_Finance_Release_Notes_9.3.22.pdf — year not detected**
The file is under `data/docs/banner/finance/releases/` but not inside a year subfolder.
Fix: Move to `data/docs/banner/finance/releases/2026/Banner_Finance_Release_Notes_9.3.22.pdf`.

**SOP154_Procedure_Axiom.docx — SOP naming mismatch**
Filename uses underscores instead of ` - ` separator. File will be silently skipped.
Fix: Rename to `SOP154 - Procedure Axiom.docx`.

### Action Plan
1. ❌ **Fix SOP154 filename** — rename before running SOP ingest (file is completely invisible otherwise)
2. ⚠ **Move Finance release note** to a year subfolder to get year metadata (optional but recommended)
3. ✅ **Ingest Finance release note after path fix** — likely new, ~28 seconds
4. ℹ **General release note already appears indexed** — skip unless re-ingest needed
5. ℹ **General user guide already appears indexed** — skip unless re-ingest needed
```

---

## Agent 18: Document Upload Coordinator Agent

**Purpose:** Conversationally guide an operator through uploading a new PDF to the knowledge base
without requiring filesystem access. Determines the correct metadata from context, chooses between
multipart upload and URL-based upload, then returns the `upload_id` needed for chunking.

**Use case:** An IT admin in Botpress or a CLI says "I need to add the Banner Finance 9.3.22 release
notes" — Agent 18 asks for the file or URL, validates the source_type and module, triggers the
upload, and explains that the document is not queryable until chunking completes. No SSH, no
filesystem access on the citesearch server, no manual Azure Blob setup required.

**Type:** Multi-turn guided workflow agent. 2–5 tool calls per session.

**Requires:** Phase U upload routes deployed on the citesearch backend.

**Recommended model:** `claude-haiku-4-5` — guided workflow with structured tool calls.

**Coordination:** Agent 18 owns upload metadata collection and Blob/sidecar creation. Agent 19 owns
chunking and verification. Do not promise retrieval from Agent 18 alone.

---

### Tools

```python
UPLOAD_TOOLS = [
    {
        "name": "banner_upload_file",
        "description": (
            "Upload a PDF from a local path to Blob Storage and create a sidecar. "
            "This does not chunk or index the PDF."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "file_path":    {"type": "string", "description": "Local path to the file to upload"},
                "source_type":  {"type": "string", "enum": ["banner", "banner_user_guide"]},
                "module":       {"type": "string", "description": "Banner module: General, Finance, Student, etc."},
                "version":      {"type": "string", "description": "e.g. 9.3.22 — only for release notes"},
                "year":         {"type": "string", "description": "e.g. 2026 — only for release notes"},
            },
            "required": ["file_path", "source_type", "module"],
        },
    },
    {
        "name": "banner_upload_from_url",
        "description": (
            "Download a PDF from an allowlisted HTTPS URL to Blob Storage and create a sidecar. "
            "This does not chunk or index the PDF."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "url":          {"type": "string"},
                "source_type":  {"type": "string", "enum": ["banner", "banner_user_guide"]},
                "module":       {"type": "string"},
                "version":      {"type": "string"},
                "year":         {"type": "string"},
            },
            "required": ["url", "source_type", "module"],
        },
    },
    {
        "name": "upload_status",
        "description": "Read sidecar status for an uploaded PDF.",
        "input_schema": {
            "type": "object",
            "properties": {
                "upload_id": {"type": "string"},
            },
            "required": ["upload_id"],
        },
    },
    {
        "name": "upload_list",
        "description": "List tracked uploads and chunking summaries.",
        "input_schema": {"type": "object", "properties": {}},
    },
]
```

### System Prompt

```python
UPLOAD_COORDINATOR_SYSTEM = """You are a Banner ERP document upload coordinator.
Your job is to guide an operator through uploading a new PDF into citesearch Blob Storage
without requiring SSH or filesystem access on the backend server.

Workflow:
1. Understand what the operator wants to add (release notes or user guide).
2. Determine required fields:
   - source_type: release notes -> "banner"; how-to user guide -> "banner_user_guide"
   - module: which Banner module (General, Finance, Student, HR, etc.) — ask if unclear
   - version: ONLY for release notes (e.g. 9.3.22) — NOT for user guides
   - year: ONLY for release notes (e.g. 2026) — NOT for user guides
3. Ask: does the operator have a local file path, or a download URL?
   - Local file → use banner_upload_file
   - URL (must be HTTPS, from ellucian.com) → use banner_upload_from_url
4. Perform the upload (banner_upload_file or banner_upload_from_url).
5. On success, call upload_status to confirm the sidecar exists.
6. Report upload_id, blob_path, total_pages, status, gap_summary, and the next chunking action.

Rules:
- Phase U accepts PDF only. Do not attempt DOCX, TXT, Markdown, or SOP upload.
- NEVER set version or year for user guide uploads; user guides have no version metadata.
- A successful upload is NOT queryable yet. It only creates the PDF blob and sidecar.
- Tell the operator to run Agent 19 or POST /banner/upload/chunk before testing retrieval.
- If the operator gives an HTTP (not HTTPS) URL, explain why it's rejected and ask for HTTPS.
- If the upload fails with a 400 about module, explain the known module list and ask them to clarify.
"""
```

### Implementation

```python
def run_upload_tool(tool_name: str, tool_input: dict) -> str:
    if tool_name == "banner_upload_file":
        file_path = tool_input.pop("file_path")
        with open(file_path, "rb") as f:
            resp = requests.post(
                f"{BASE_URL}/banner/upload",
                files={"file": (os.path.basename(file_path), f)},
                data=tool_input,  # source_type, module, version, year
                headers={"Authorization": f"Bearer {API_KEY}"},
            )
        resp.raise_for_status()
        return json.dumps(resp.json())
    if tool_name == "banner_upload_from_url":
        resp = requests.post(f"{BASE_URL}/banner/upload/from-url", json=tool_input, headers=HEADERS)
        resp.raise_for_status()
        return json.dumps(resp.json())
    if tool_name == "upload_status":
        return json.dumps(call_api("GET", f"/banner/upload/{tool_input['upload_id']}/status"))
    if tool_name == "upload_list":
        return json.dumps(call_api("GET", "/banner/upload"))
    return json.dumps({"error": "unknown tool"})


def upload_coordinator_agent(user_message: str) -> str:
    messages = [{"role": "user", "content": user_message}]
    while True:
        response = client.messages.create(
            model="claude-haiku-4-5-20251001",
            max_tokens=1024,
            system=UPLOAD_COORDINATOR_SYSTEM,
            tools=UPLOAD_TOOLS,
            messages=messages,
        )
        if response.stop_reason == "end_turn":
            return next(b.text for b in response.content if b.type == "text")
        tool_results = []
        for block in response.content:
            if block.type == "tool_use":
                result = run_upload_tool(block.name, dict(block.input))
                tool_results.append({"type": "tool_result", "tool_use_id": block.id, "content": result})
        messages.append({"role": "assistant", "content": response.content})
        messages.append({"role": "user", "content": tool_results})
```

### Example Session

```
Operator: I need to add Banner Finance 9.3.22 release notes to the knowledge base.

Agent:  Got it — Banner Finance release notes. A few quick questions:
        1. Do you have a local file path, or a download URL from Ellucian?
        2. What year are these from? (e.g. 2026)

Operator: I have the file at /home/admin/Banner_Finance_9.3.22.pdf and it's from 2026.

Agent:  [calls banner_upload_file with {
            file_path: "/home/admin/Banner_Finance_9.3.22.pdf",
            source_type: "banner", module: "Finance", version: "9.3.22", year: "2026"
        }]
        → {
          "upload_id": "upload-123",
          "blob_path": "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
          "total_pages": 42,
          "status": "pending",
          "chunking_pattern": "none",
          "gap_count": 1,
          "gap_summary": "1 gap: pages 1-42 (42 pages total unchunked)"
        }
        [calls upload_status with {"upload_id": "upload-123"}]

Answer: Upload successful.
        - Upload ID: upload-123
        - Blob path: banner/finance/releases/2026/Banner_Finance_9.3.22.pdf
        - Status: pending, 42 pages unchunked
        - Next step: run Agent 19 or POST /banner/upload/chunk to make pages queryable.

---

Operator: Add the Banner Finance user guide from https://customercare.ellucian.com/downloads/finance-guide.pdf

Agent:  [calls banner_upload_from_url with {
            url: "https://customercare.ellucian.com/downloads/finance-guide.pdf",
            source_type: "banner_user_guide", module: "Finance"
        }]
        (Note: no version or year — user guides are not versioned)
        → {
          "upload_id": "upload-456",
          "blob_path": "banner/finance/use/finance-guide.pdf",
          "total_pages": 260,
          "status": "pending",
          "gap_summary": "1 gap: pages 1-260 (260 pages total unchunked)"
        }

Answer: Finance user guide uploaded.
        - Upload ID: upload-456
        - Blob path: banner/finance/use/finance-guide.pdf
        - Not queryable yet. Chunk all pages or choose a page range with Agent 19.
```

---

## Agent 19: Upload Chunking Wizard Agent

**Purpose:** Guide an operator through making an uploaded PDF queryable by chunking page ranges,
checking sidecar status, and optionally verifying retrieval after chunking completes.

**Use case:** Agent 18 uploaded a 260-page Finance user guide and returned `upload_id=upload-456`.
Agent 19 helps the operator chunk pages 1-50 first, resume later, or chunk all remaining gaps.

**Type:** Multi-turn guided workflow agent. 2-6 tool calls per session.

**Requires:** Phase U chunk, status, list, and ask routes deployed.

**Recommended model:** `claude-haiku-4-5` — guided workflow with structured tool calls.

---

### Tools

```python
CHUNKING_TOOLS = [
    {
        "name": "upload_status",
        "description": "Read full sidecar status for an uploaded PDF.",
        "input_schema": {
            "type": "object",
            "properties": {"upload_id": {"type": "string"}},
            "required": ["upload_id"],
        },
    },
    {
        "name": "upload_list",
        "description": "List tracked uploads and sidecar summaries.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "upload_chunk",
        "description": (
            "Chunk an uploaded PDF page range. Omit page_start/page_end to chunk all remaining gaps."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "upload_id": {"type": "string"},
                "page_start": {"type": "integer"},
                "page_end": {"type": "integer"},
                "pages_per_batch": {"type": "integer", "default": 10},
            },
            "required": ["upload_id"],
        },
    },
    {
        "name": "module_ask",
        "description": "Verify retrieval after chunking has completed for relevant pages.",
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {"type": "string"},
                "module": {"type": "string"},
                "source_type": {"type": "string"},
                "top_k": {"type": "integer", "default": 3},
            },
            "required": ["question", "module"],
        },
    },
]
```

### System Prompt

```python
CHUNKING_WIZARD_SYSTEM = """You are a Banner ERP upload chunking wizard.
Your job is to make an already-uploaded PDF queryable by calling POST /banner/upload/chunk.

Workflow:
1. Ask for upload_id if it was not provided. If the operator does not know it, call upload_list.
2. Call upload_status and summarize status, total_pages, gap_summary, and chunking_pattern.
3. Ask whether to chunk all remaining pages or a specific page range.
4. Call upload_chunk. If page_start/page_end are omitted, all unchunked gaps are processed.
5. Call upload_status again and report gap_count, queryable_page_count, remaining_page_count.
6. If at least one page range is queryable, optionally run module_ask with a targeted verification query.

Rules:
- Do not call upload_chunk for overlapping or out-of-bounds ranges shown by status.
- If a chunk call returns 409, explain that another chunk run is active for this upload_id.
- If status remains partial, be explicit that only chunked pages are queryable.
- Use gap_count and unchunked_ranges as the source of truth for remaining work.
- DELETE /banner/upload/{id} does not purge Azure Search chunks; never claim it does.
"""
```

### Implementation

```python
def run_chunking_tool(tool_name: str, tool_input: dict) -> str:
    if tool_name == "upload_status":
        return json.dumps(call_api("GET", f"/banner/upload/{tool_input['upload_id']}/status"))
    if tool_name == "upload_list":
        return json.dumps(call_api("GET", "/banner/upload"))
    if tool_name == "upload_chunk":
        return json.dumps(call_api("POST", "/banner/upload/chunk", tool_input))
    if tool_name == "module_ask":
        payload = {
            "question": tool_input["question"],
            "top_k": tool_input.get("top_k", 3),
            "source_type": tool_input.get("source_type", ""),
        }
        module = tool_input["module"].lower().replace(" ", "-")
        return json.dumps(call_api("POST", f"/banner/{module}/ask", payload))
    return json.dumps({"error": "unknown tool"})
```

### Example Session

```
Operator: Chunk upload-456, but start with the first 50 pages.

Agent:  [calls upload_status with {"upload_id": "upload-456"}]
        → {total_pages: 260, status: "pending", gap_summary: "1 gap: pages 1-260"}
        [calls upload_chunk with {"upload_id": "upload-456", "page_start": 1, "page_end": 50}]
        → {pages_chunked: 50, chunks_indexed: 410, status: "partial", gap_count: 1}
        [calls upload_status with {"upload_id": "upload-456"}]
        → {queryable_page_count: 50, remaining_page_count: 210, gap_summary: "1 gap: pages 51-260"}

Answer: Pages 1-50 are now queryable. The upload is still partial; pages 51-260 remain.
        Continue with another page range or chunk all remaining pages when ready.
```

---

## Tool Reference: API-to-Tool Mapping

| Tool Name | HTTP Method | Endpoint | Agent(s) Using It |
|-----------|-------------|----------|--------------------|
| `banner_ask` | POST | `/banner/ask` | 1, 3, 4, 6, 7, 9 |
| `sop_ask` | POST | `/sop/ask` | 2, 3, 4, 5, 6 |
| `sop_list` | GET | `/sop` | 2, 4, 6 |
| `banner_summarize` | POST | `/banner/summarize/{topic}` | 3, 4, 6 |
| `banner_ingest` | POST | `/banner/ingest` | 5 |
| `sop_ingest` | POST | `/sop/ingest` | 5 |
| `index_health` | GET | `/health` | 5, 7 |
| `index_stats` | GET | `/index/stats` | 5, 7, 9, 17 |
| `debug_chunks` | GET | `/debug/chunks` | 17 |
| `chat_ask` | POST | `/chat/ask` | 8 |
| `chat_intent` | POST | `/chat/intent` | 8 |
| `chat_sentiment` | POST | `/chat/sentiment` | 8 |
| (calibration protocol) | POST | `/banner/ask` | 9 |
| (calibration protocol) | GET | `/index/stats` | 9 |
| `user_guide_ask` | POST | `/banner/:module/ask` (source_type=banner_user_guide) | 10 |
| `wizard_step` | POST | `/student/wizard` | 11, 12 |
| `query_expand` | POST | `/chat/rewrite` (or direct Claude call) | 13 |
| `grounding_check` | POST | `/chat/ground` (or direct Claude call) | 14 |
| `analytics_summary` | GET | `/analytics/summary` | 15, 16 |
| `feedback_report` | GET | `/analytics/feedback` | 15, 16 |
| `feedback_submit` | POST | `/chat/feedback` | 8 (extended) |
| `ingest_dryrun` | POST | `/banner/ingest` (dry_run=true) | 17 |
| `banner_upload_file` | POST | `/banner/upload` (multipart) | 18 |
| `banner_upload_from_url` | POST | `/banner/upload/from-url` | 18 |
| `upload_chunk` | POST | `/banner/upload/chunk` | 19 |
| `upload_status` | GET | `/banner/upload/{upload_id}/status` | 18, 19 |
| `upload_list` | GET | `/banner/upload` | 18, 19 |
| `upload_delete` | DELETE | `/banner/upload/{upload_id}` | 19 |

---

## Implementation Notes

### Project Structure (suggested)

```
agents/
├── __init__.py
├── common.py              # run_tool dispatcher, API client setup
├── banner_ask.py          # Agent 1
├── sop_navigator.py       # Agent 2
├── upgrade_analyzer.py    # Agent 3
├── conversational.py      # Agent 4
├── ingestion.py           # Agent 5
├── gap_analyzer.py        # Agent 6
├── diagnostics.py         # Agent 7
├── calibration.py         # Agent 9
├── user_guide.py          # Agent 10
├── wizard_conductor.py    # Agent 11
├── wizard_harness.py      # Agent 12
├── query_rewriter.py      # Agent 13
├── grounding.py           # Agent 14
├── analytics.py           # Agent 15
├── feedback_analyst.py    # Agent 16
├── preflight.py           # Agent 17
├── upload_coordinator.py  # Agent 18
├── upload_chunking.py     # Agent 19
└── cli.py                 # Entry point: pick agent by command-line arg
```

### Confidence Escalation (all agents)

The existing backend already returns `top_score` and `retrieval_count` in every response.
All agents should enforce this pattern:

```python
def check_confidence(api_result: dict) -> str | None:
    """Returns an escalation message if confidence is too low, else None."""
    if api_result.get("retrieval_count", 0) == 0:
        return "No documents found in the index. Check that ingestion has been run."
    if api_result.get("top_score", 0) < 0.01:
        return f"Near-zero score ({api_result['top_score']:.3f}). Verify answer against source docs."
    # Note: scores of 0.01–0.05 are normal for this index — not low confidence
    return None
```

### Cost Control

| Agent | Recommended Model | Typical Cost per Run |
|-------|------------------|---------------------|
| Banner Ask (Agent 1) | `claude-haiku-4-5` | < $0.001 |
| SOP Navigator (Agent 2) | `claude-haiku-4-5` | < $0.001 |
| Upgrade Analyzer (Agent 3) | `claude-sonnet-4-6` | ~$0.01–0.05 |
| Conversational (Agent 4) | `claude-sonnet-4-6` | ~$0.005 per turn |
| Ingestion Orchestrator (Agent 5) | `claude-haiku-4-5` | < $0.001 |
| Gap Analyzer (Agent 6) | `claude-sonnet-4-6` | ~$0.05–0.10 |
| Diagnostics (Agent 7) | `claude-haiku-4-5` | < $0.005 |
| Internal Banner Chatbot (Agent 8) | `claude-haiku-4-5` | < $0.002 per turn |
| Confidence Calibration (Agent 9) | `claude-haiku-4-5` | < $0.005 per run |
| User Guide Navigation (Agent 10) | `claude-haiku-4-5` | < $0.001 per turn |
| Banner Student Wizard Conductor (Agent 11) | `claude-haiku-4-5` | < $0.002 per turn |
| Wizard Test Harness (Agent 12) | `claude-haiku-4-5` | < $0.01 per wizard run |
| Query Rewriting (Agent 13) | `claude-haiku-4-5` | < $0.001 per call |
| Answer Grounding (Agent 14) | `claude-haiku-4-5` | < $0.001 per check |
| Analytics Advisor (Agent 15) | `claude-haiku-4-5` | < $0.005 per query |
| Feedback Pattern Analyst (Agent 16) | `claude-sonnet-4-6` | < $0.02 per weekly report |
| PDF Preflight Auditor (Agent 17) | `claude-haiku-4-5` | < $0.002 per audit run |
| Document Upload Coordinator (Agent 18) | `claude-haiku-4-5` | < $0.003 per upload session |
| Upload Chunking Wizard (Agent 19) | `claude-haiku-4-5` | < $0.003 per chunking session |

Azure OpenAI costs (embedding + chat) remain the same regardless of which Claude model is used.

### Testing Agents Without Claude API Costs

Use `anthropic.Anthropic(api_key="fake")` and mock `client.messages.create` to test the
tool dispatch loop and prompt logic without incurring API costs.

---

## Priority Recommendation

**Start here (highest value, lowest complexity):**

1. **Agent 1 — Banner Ask**: One tool, immediate utility, validates the end-to-end flow
2. **Agent 2 — SOP Navigator**: One additional tool, complements Agent 1

**Next (unique capability not available today):**

3. **Agent 3 — Upgrade Impact Analyzer**: Automates what currently takes hours of manual reading
4. **Agent 7 — Diagnostics**: Saves time when something breaks

**After the above are working:**

5. **Agent 10 — User Guide Navigation**: Low complexity, high value for onboarding new Banner users
6. **Agent 4 — Conversational**: Requires more careful session management
7. **Agent 5 — Ingestion Orchestrator**: Useful but ingestion is currently rare
8. **Agent 6 — Gap Analyzer**: High value for upgrade planning but complex to validate

**Primary chatbot (internal users — this is the main Botpress target):**

8. **Agent 8 — Internal Banner Chatbot**: Wraps the adapter `/chat/*` layer with intent
   classification, sentiment-aware escalation, and a Banner-admin persona. This is the
   agent Botpress calls.

**Wizard system (deploy after Phase F is complete):**

9. **Agent 11 — Wizard Conductor**: The Botpress integration point for all Banner Student
   wizard sessions. Replaces per-wizard Execute Code with a single generic conductor that
   handles all wizard types via `POST /student/wizard`.
10. **Agent 12 — Wizard Test Harness**: Run before merging any new wizard step sequence
    into `catalog.go`. Automates golden-path + error-path validation so manual testing
    of 14-step forms is not required.

**Java AI-ROADMAP ports (deploy after Phases H–K are complete):**

11. **Agent 13 — Query Rewriting**: Deployed inside the adapter as a pre-retrieval step.
    Run standalone to test how query expansion changes citesearch results for terse questions.
12. **Agent 14 — Grounding Verifier**: Run on demand to audit answer quality. Most valuable
    for Finance and SOP answers where hallucination risk is highest.
13. **Agent 15 — Analytics Advisor**: Weekly review of escalation rate and intent distribution.
    First signal that the index needs new documents or a prompt needs adjustment.
14. **Agent 16 — Feedback Analyst**: Run after 50+ feedback entries accumulate. Generates
    a prioritized remediation list distinguishing retrieval gaps from prompt quality issues.

**Ingest operations (run before any new document collection is added):**

15. **Agent 17 — PDF Preflight Auditor**: Run before every ingest of new PDFs. Catches naming
    convention errors, detects scanned PDFs, and estimates time — before a single embedding
    call is made. Requires Phase L.2 (dry-run endpoint) to be deployed.
16. **Agent 18 — Document Upload Coordinator**: The conversational path to upload a PDF without
    SSH or filesystem access. It handles module/source_type inference and returns an `upload_id`.
    Requires Phase U upload routes.
17. **Agent 19 — Upload Chunking Wizard**: Makes uploaded PDFs queryable by chunking all remaining
    pages or selected page ranges, then verifies status and retrieval. Requires Phase U chunk/status
    routes.

---

*Related wiki documents: [INTEGRATIONS.md](INTEGRATIONS.md) covers LangGraph and n8n alternatives.
These Claude agents are the simplest path — no separate orchestration infrastructure required.*
