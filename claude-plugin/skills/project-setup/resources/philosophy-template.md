# Implementation Philosophy

> **For all agents:** This document governs how features are built. Read this
> BEFORE any plan file or task descriptions. When in doubt, ask: "[your project's
> guiding question — e.g., 'Am I wiring a real service, or faking it?']"
>
> **Fill this template** for your project. The pre-filled examples below are
> universal defaults — adapt, replace, or extend them for your domain.

## Core Principle

**Every implementation MUST satisfy ALL of these criteria:**

1. [Criterion 1 — e.g., Handler calls real service method, not hardcoded data]
2. [Criterion 2 — e.g., Template renders from service response, not inline HTML]
3. [Criterion 3 — e.g., Changing underlying data changes rendered output]
4. [Criterion 4 — e.g., Access control uses real permission checks, not client-side hiding]
5. The verification step in the task description passes

## Decision Framework

When implementing any requirement, follow this decision tree:

| Question | Action |
|----------|--------|
| Does a backend service already exist? | Wire it. Do not rewrite. |
| Does the service exist but need extension? | Extend the service, then wire. |
| Is this purely new capability? | Build minimal service first, then UI. |
| Is this seed data / configuration? | Load into real stores, not hardcoded. |

## Anti-Patterns

<!-- Define 5-7 anti-patterns with BAD/GOOD code examples.                -->
<!-- Pre-filled with 3 universal patterns. Adapt or replace for your      -->
<!-- project. The BAD/GOOD format is critical — it makes the standard     -->
<!-- concrete and unambiguous for implementation agents.                   -->

### 1. Hardcoded data in handlers

```
// BAD — handler returns static data, ignoring the service layer
func handleList(w http.ResponseWriter, r *http.Request) {
    items := []Item{{Name: "hardcoded-1"}, {Name: "hardcoded-2"}}
    tmpl.Execute(w, items)
}

// GOOD — handler calls real service, renders response
func handleList(w http.ResponseWriter, r *http.Request) {
    items, err := h.store.ListItems(r.Context())
    if err != nil { http.Error(w, err.Error(), 500); return }
    tmpl.Execute(w, items)
}
```

**Why:** Hardcoded data passes tests but breaks when real data changes. It
creates a false sense of completion.

### 2. Client-side permission hiding

```html
<!-- BAD — server renders everything, JS/CSS hides unauthorized items -->
<button id="admin-action" style="display:none">Delete</button>
<script>if (userRole === 'admin') show('admin-action')</script>

<!-- GOOD — server only renders items the user can access -->
{{if .Permissions.CanDelete}}
<button>Delete</button>
{{end}}
```

**Why:** Client-side hiding is a UI illusion, not access control. The server
still processes unauthorized requests.

### 3. Scripted/mocked responses in production paths

```python
# BAD — handler returns a pre-written response instead of calling the service
def analyze(request):
    return {"result": "This ticket appears to be high priority..."}

# GOOD — handler calls the real analysis service
def analyze(request):
    result = analysis_service.analyze(request.ticket_id)
    return {"result": result}
```

**Why:** Scripted responses mask integration failures. The service may not
actually work, and you won't know until production.

### 4. [Project-specific anti-pattern]

```
// BAD
// GOOD
```

**Why:** [Impact explanation]

### 5. [Project-specific anti-pattern]

```
// BAD
// GOOD
```

**Why:** [Impact explanation]

## Acceptable Simplifications

Not everything needs to be production-grade. These simplifications are OK:

| Simplification | Why It's OK | Boundary (when to stop) |
|---------------|-------------|-------------------------|
| SQLite instead of Postgres | Same SQL interface, simpler setup | When concurrent write throughput matters |
| Cached/mock external APIs | Deterministic tests, no network deps | When testing real API behavior changes |
| Static seed data | Reproducible demos and tests | When data must reflect real user input |
| [Project-specific] | [Rationale] | [Boundary] |

## Red Flags

If you see any of these in your code, **STOP and fix before continuing:**

- [ ] Literal data structures returned from handlers (not from a store/service)
- [ ] Inline HTML generation (`fmt.Fprintf`, string concatenation) instead of templates
- [ ] Missing error handling on service/store calls
- [ ] TODO/FIXME comments left in committed code
- [ ] [Project-specific red flag]
- [ ] [Project-specific red flag]
- [ ] [Project-specific red flag]

## For Implementation Agents

### Before You Start a Task

- [ ] Read the design doc referenced in your prompt
- [ ] Read the task description (`bd show {TASK_ID}`) — it's your source of truth
- [ ] Search the plan file for your task's implementation code (`grep "## Task: {BEAD_ID}" plan-file.md`)
- [ ] Identify which existing services to wire (do not reimplement)
- [ ] Review the anti-patterns above — know what NOT to do

### Before You Close a Task

- [ ] All acceptance criteria in the beads task are satisfied
- [ ] Tests pass (run quality commands from your prompt)
- [ ] No red flags present in your implementation
- [ ] Commit message is descriptive
- [ ] Code follows existing project patterns and conventions
