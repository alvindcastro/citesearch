# Banner Student — Guided Workflow Chatbot

> **Project:** citesearch extension  
> **Scope:** Conversational wizard chatbot covering Banner Student module operations across Catalog, Scheduling, Registration, Student Records, and Admissions  
> **Interface:** Botpress (extending existing citesearch/Ask Banner setup)  
> **Status:** Planning

---

## Background

This is **Approach 2** of a two-approach evaluation for an Ellucian Banner chatbot.

Rather than a RAG Q&A bot (Approach 1, already supported by citesearch), this chatbot is a **guided workflow engine** — a conversational state machine that walks a user step-by-step through Banner Student operations, validates their inputs, and produces a structured output at the end.

The citesearch backend remains in the loop as a **knowledge fallback** at any step. If a user asks "what does this field mean?" mid-wizard, the bot calls `/banner/student/lookup`, answers the question, and resumes exactly where it left off.

---

## Who This Is For

| Audience | How they use it |
|---|---|
| Curriculum / catalog admin | Course creation, modification, retirement |
| Department scheduler | Section creation, meeting times, instructor assignment |
| Registrar staff | Registration overrides, grade changes, withdrawals |
| Student records staff | Program/plan updates, person record management |
| Admissions staff | New person creation, contact info updates |

The chatbot is designed for a **mixed audience** — prompts and language should be accessible to non-technical Banner users, not just system administrators.

---

## Architecture

### How a Wizard Works

Every Banner Student operation is modelled as a **wizard** — an ordered sequence of steps, each collecting one or more fields. The same shared infrastructure handles all wizards:

```
[User triggers intent]
        ↓
[Intent router identifies wizard]
        ↓
[Wizard engine loads step sequence for that operation]
        ↓
[Step loop: prompt → collect → validate → advance]
        ↓
[Escape hatch: clarifying question → citesearch → resume step]
        ↓
[Review summary: show all fields, confirm]
        ↓
[Output: summary card / email / JSON / Banner API]
```

### Escape Hatch (applies to all wizards)

At any step, the user can ask a clarifying question. The bot:

1. Detects the off-topic intent via keyword or LLM classification
2. Calls citesearch `/banner/student/lookup` with the question + current field context
3. Returns the answer inline
4. Resumes the **same step** — collected state is never lost

### Session State Shape

A `wizard_session` object accumulates fields across turns. Shared across all wizard types:

```json
{
  "session_id": "abc-123",
  "wizard": "course_creation",
  "current_step": "schedule_type",
  "fields": {
    "subject_code": "COMP",
    "course_number": "3850",
    "short_title": "Software Engineering",
    "credit_hours": 3,
    "course_level": "UG"
  }
}
```

### Review Node (applies to all wizards)

Before any output, the bot renders a structured summary and asks for confirmation:

> "Here's what I have. Does everything look correct?"
>
> ✅ Confirm | ✏️ Edit a field | ❌ Start over

---

## Shared Adapter Endpoint

A single endpoint on the citesearch adapter handles all wizard sessions:

### `POST /student/wizard`

**Request:**
```json
{
  "session_id": "abc-123",
  "wizard": "course_creation",
  "step": "credit_hours",
  "value": "3",
  "question": null
}
```

**Response (advance):**
```json
{
  "valid": true,
  "next_step": "course_level",
  "prompt": "What is the course level?",
  "options": ["UG", "GR", "CE"],
  "session": { ... }
}
```

**Response (clarifying question):**
```json
{
  "valid": null,
  "answer": "Credit hours define the number of semester credit hours awarded...",
  "resume_step": "credit_hours",
  "prompt": "Got it — back to credit hours. How many credit hours is this course?"
}
```

---

## Botpress Design

- Each wizard step = one **Botpress flow node** with an Execute Code block
- Picklist fields use **Quick Reply buttons** to eliminate invalid free-text input
- All validation is server-side in the adapter — Botpress stays as thin presentation layer
- A single **`/student/wizard`** call per turn handles: validation, next step, citesearch fallback, and session persistence
- The `wizard` field in the session routes the adapter to the correct step sequence

---

## Output Options

Applies to all wizards. Options in order of implementation complexity:

| Option | Description | Complexity |
|---|---|---|
| **Summary card** | Botpress renders collected fields as a readable checklist. User manually enters in Banner. | Low |
| **Email / ticket** | Adapter emails the structured summary to the appropriate staff for manual entry. | Low–Medium |
| **JSON export** | Structured JSON payload — can feed a Banner REST API call or RPA process. | Medium |
| **Banner API** | Direct POST to Banner Student API. Requires credentials, endpoint access, and error handling. | High |

**Recommended starting point:** Summary card → Email/ticket handoff. Prove the wizard UX first, wire up the API later.

---

## Phased Rollout

Wizards are grouped into four phases, ordered by impact and implementation complexity.

---

### Phase 1 — Catalog / Curriculum
> Highest value for curriculum and catalog admins. Foundation wizards that other phases depend on.

#### 1.1 Create a Course (SCACRSE)

| Field | Required | Type | Validation |
|---|---|---|---|
| Subject Code | Yes | Text | 2–4 uppercase letters |
| Course Number | Yes | Text | Alphanumeric, Banner format |
| Short Title | Yes | Text | Max 30 chars |
| Long Title | No | Text | Max 100 chars |
| Credit Hours | Yes | Number / Range | Positive, max 2 decimal places |
| Course Level | Yes | Picklist | UG, GR, CE |
| Schedule Type | Yes | Picklist | Lecture, Lab, Seminar, Online, Hybrid |
| Grading Mode | Yes | Picklist | Standard, Pass-Fail, Audit |
| College | Yes | Text / Picklist | Must match Banner college codes |
| Department | Yes | Text / Picklist | Must match Banner dept codes |
| Prerequisites | No | Text | Free text |
| Co-requisites | No | Text | Free text |
| Attributes | No | Multi-select | Honors, Gen Ed, Writing-Intensive, etc. |
| Enrollment Restrictions | No | Text | By level, major, classification |
| Instructor Consent Required | No | Boolean | Yes / No |
| Repeatable | No | Boolean + Number | Max credits / max attempts |
| Effective Term | Yes | Text | Banner term code (e.g. 202510) |
| Status | Yes | Picklist | Active, Inactive, Pending |

#### 1.2 Modify an Existing Course

Steps: look up course by subject + number → display current values → collect changed fields only → review diff → output.

#### 1.3 Inactivate / Retire a Course

Steps: look up course → confirm no active sections exist → collect effective term → set status to Inactive → review → output.

---

### Phase 2 — Schedule / Sections
> For department schedulers. Depends on Phase 1 courses existing in the catalog.

#### 2.1 Create a Section (SSASECT)

| Field | Required | Type | Notes |
|---|---|---|---|
| Course (Subject + Number) | Yes | Lookup | Must exist in catalog |
| Term | Yes | Text | Banner term code |
| Section Number | Yes | Text | e.g. 001, 002 |
| Campus | Yes | Picklist | |
| Status | Yes | Picklist | Active, Cancelled, etc. |
| Maximum Enrollment | Yes | Number | |
| Schedule Type | Yes | Picklist | Inherits from course, can override |
| Instructional Method | Yes | Picklist | Face-to-face, Online, Hybrid |
| Part of Term | Yes | Picklist | Full term, first half, second half, etc. |
| Credit Hours | Yes | Number | Can override course default |

#### 2.2 Add Meeting Times and Rooms

Steps: look up section → collect days of week → collect start/end time → collect building/room → check for conflicts → review → output.

#### 2.3 Assign Instructor to a Section

Steps: look up section → search instructor by name/ID → assign role (primary, secondary) → collect override grade indicator → review → output.

#### 2.4 Cancel a Section

Steps: look up section → confirm enrolled student count → collect cancellation reason → set status to Cancelled → review → output. Flag if students are enrolled.

---

### Phase 3 — Registration
> For registrar staff. Highest sensitivity — directly affects student records.

#### 3.1 Add/Drop a Student from a Section

Steps: look up student by ID or name → look up section → confirm action (add or drop) → collect override reason if restrictions exist → review → output.

#### 3.2 Override a Prerequisite or Restriction

Steps: look up student → look up section → identify the restriction being overridden → collect override justification → collect authorizing staff ID → review → output.

#### 3.3 Process a Late Registration

Steps: look up student → look up section → confirm late registration fee applicability → collect authorization → review → output.

---

### Phase 4 — Student Records & Admissions
> Broadest scope. Touches person records and academic history.

#### 4.1 Update a Student's Program / Plan

Steps: look up student → display current program/plan → collect new program → collect new plan (major/minor) → collect effective term → review → output.

#### 4.2 Record a Grade Change

Steps: look up student → look up term + course → display current grade → collect new grade → collect reason code → collect authorizing faculty/staff → review → output. Requires approval workflow trigger.

#### 4.3 Process a Withdrawal

Steps: look up student → collect withdrawal term → collect withdrawal date → collect reason code → confirm financial aid implications flag → review → output.

#### 4.4 Create a New Person Record (SPAIDEN)

Steps: collect last name, first name, middle name → collect date of birth → collect gender → collect SSN / SIN (masked input) → collect address → collect phone → collect email → check for duplicate person match → review → output.

#### 4.5 Update Address / Contact Info

Steps: look up person by ID → select address type to update → collect new address fields → collect effective date → review → output.

---

## Validation Layer

Validation is enforced server-side in the adapter. Botpress never validates — it only renders prompts and collects input.

| Rule Type | Example |
|---|---|
| Format | Subject code must be 2–4 uppercase alpha characters |
| Range | Credit hours must be a positive number ≤ 12 |
| Allowed values | Course level must be one of: UG, GR, CE |
| Conditional required | If repeatable = Yes, max credits is required |
| Banner term format | Effective term must match `YYYYTT` pattern |
| Cross-record check | Course must exist before a section can be created |
| Conflict detection | Room/time conflict check before confirming meeting times |
| Duplicate detection | Person name + DOB match check before creating new record |

---

## citesearch Integration

| When | Endpoint | Purpose |
|---|---|---|
| User asks a clarifying question mid-step | `/banner/student/lookup` | Explain a field or give valid examples |
| User asks about downstream effects | `/banner/student/cross-reference` | "Will cancelling this section affect enrolled students?" |
| User wants to understand a step's purpose | `/banner/student/procedure` | Explain what this operation does in Banner |

---

## What Needs to Be Built

| Component | Description | Phase |
|---|---|---|
| Intent router | Identifies which wizard to launch from user's opening message | All |
| Botpress flow — Phase 1 wizards | Catalog: create, modify, inactivate course | Phase 1 |
| Botpress flow — Phase 2 wizards | Scheduling: section, meeting times, instructor, cancel | Phase 2 |
| Botpress flow — Phase 3 wizards | Registration: add/drop, override, late reg | Phase 3 |
| Botpress flow — Phase 4 wizards | Records + admissions: program, grade change, withdrawal, person | Phase 4 |
| `POST /student/wizard` adapter endpoint | Shared session management, step sequencing, validation routing | All |
| Per-wizard validation rules | Field rules per operation, server-side in adapter | Per phase |
| citesearch fallback | Mid-flow question detection, lookup call, resume logic | All |
| Conflict / duplicate detection | Room conflicts, person duplicate matching | Phase 2, 4 |
| Output renderer | Summary card, email template, or JSON → API | All |
| Approval workflow trigger | Grade changes and course retirement may need downstream routing | Phase 1, 4 |

---

## Open Questions

- [ ] What is the final output mechanism for each phase? (summary, email, API, RPA?)
- [ ] Do we have access to Banner's REST API for direct submission?
- [ ] Should the bot support **editing a previously collected field** from the Review node?
- [ ] Do we need an **authentication layer** — should the bot know who the user is before allowing sensitive operations (grade changes, withdrawals)?
- [ ] Should College / Department and other reference fields be free text or loaded dynamically from Banner?
- [ ] Which operations require an **approval workflow** after the wizard completes?
- [ ] Should Phase 3 (Registration) and Phase 4 (Records) require elevated permissions vs Phase 1–2?

---

## Implementation Plan

Execution is tracked in `PLAN.md`. Phases map to this document as follows:

| PLAN.md Phase | Scope | Status |
|---|---|---|
| **Phase F** | `POST /student/wizard` adapter endpoint (Phase 1.1 — Create a Course) | Planned |
| **Phase G** | Claude Agents 11 & 12 documentation (wizard conductor + test harness) | Planned |

Phase F follows strict TDD (Red → Green → Refactor). Run `go test ./... -v` at every task boundary.

New wizard types (Phase 1.2, 2.x, 3.x, 4.x) are added by:
1. Adding a new `[]StepDefinition` slice in `internal/wizard/catalog.go`
2. Registering it in `DefaultRegistry()`
3. Running Agent 12 (Wizard Test Harness) to verify the new step sequence before merging

No changes to the engine, handler, or session store are required for new wizard types.

**Claude Agents for the wizard system:**
- [Agent 11 — Wizard Conductor](CLAUDE_AGENTS.md#agent-11-banner-student-wizard-conductor): Botpress-facing multi-turn driver
- [Agent 12 — Wizard Test Harness](CLAUDE_AGENTS.md#agent-12-wizard-test-harness-agent): Automated pre-merge validator

---

## Related

- [citesearch repo](https://github.com/alvindcastro/citesearch)
- [CHATBOT.md](../wiki/CHATBOT.md) — existing chatbot architecture
- [BOTPRESS-SETUP.md](../wiki/BOTPRESS-SETUP.md) — Botpress wiring and Execute Code snippets
- Banner forms: `SCACRSE`, `SSASECT`, `SPAIDEN`, `SFAREGS`
