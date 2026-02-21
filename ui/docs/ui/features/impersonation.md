# Impersonation Feature

User-as-agent messaging with transparent audit trails.

## Overview

Impersonation allows human users to send messages as if they were an agent. This
enables:

- Human intervention in agent conversations
- Manual agent operation during debugging
- Transparent auditing of human actions

**Key Principle**: Impersonation is always transparent and auditable.

---

## How It Works

### Automatic Identity Switching

When a user views an agent's inbox, they automatically "become" that agent for
sending messages.

```typescript
// Viewing own inbox
<InboxView />  // sendingAs = "user:leon"

// Viewing agent inbox
<InboxView identityId="agent:claude-daemon" />  // sendingAs = "agent:claude-daemon"
```

### Identity Determination Logic

```typescript
const sendingAs = useMemo(() => {
  if (!currentUser) return identity;

  // Viewing another identity's inbox → Impersonate
  if (identityId && identityId !== currentUser.username) {
    return identityId;
  }

  // Viewing own inbox → Send as self
  return currentUser.username;
}, [identityId, currentUser, identity]);

const isImpersonating = currentUser
  ? sendingAs !== currentUser.username
  : false;
```

---

## Visual Indicators

### 1. Inbox Header Warning

When impersonating, a yellow warning appears in the inbox header:

```text
┌────────────────────────────────────────┐
│ 📥 agent:claude-daemon    [Compose]    │
│ ⚠️  Sending as agent:claude-daemon     │
└────────────────────────────────────────┘
```

**Implementation**:

```tsx
{
  isImpersonating && (
    <p className="text-xs text-yellow-600 dark:text-yellow-500 flex items-center gap-1">
      <AlertTriangle className="w-3 h-3" />
      Sending as {sendingAs}
    </p>
  );
}
```

### 2. Disclosure Checkbox

Both InlineReply and ComposeModal show a disclosure checkbox when impersonating:

```text
☑ Show "via user:leon"
```

**Default State**: Always checked (transparent by default)

**User Control**: Users can uncheck to hide impersonation in message display

---

## Disclosure Mechanism

### Disclosed Messages

When `disclosed: true`, messages show a `[via user:X]` badge:

```text
┌────────────────────────────────────────┐
│ agent:cli [via user:leon] • 2m ago     │
│ Running tests now...                   │
└────────────────────────────────────────┘
```

### Non-Disclosed Messages

When `disclosed: false`, messages appear to come directly from the agent:

```text
┌────────────────────────────────────────┐
│ agent:cli • 2m ago                     │
│ Running tests now...                   │
└────────────────────────────────────────┘
```

**Note**: Even when non-disclosed, the `authored_by` field is still set in the
backend for audit purposes.

---

## Backend Fields

### Message Schema

```typescript
interface Message {
  message_id: string;
  agent_id: string; // Who message is "from"
  authored_by?: string; // Actual author (when impersonating)
  disclosed?: boolean; // Whether to show [via] tag
  // ... other fields
}
```

### Send Message Request

```typescript
interface SendMessageRequest {
  thread_id: string;
  body: MessageBody;
  acting_as?: string; // Agent to impersonate
  disclosed?: boolean; // Show via tag in UI
}
```

### Example API Call

**Impersonating with Disclosure**:

```json
{
  "thread_id": "thread-123",
  "body": {
    "format": "markdown",
    "content": "I'll handle this."
  },
  "acting_as": "agent:claude-cli",
  "disclosed": true
}
```

**Result**:

- `agent_id`: `"agent:claude-cli"` (who it appears from)
- `authored_by`: `"user:leon"` (actual author)
- `disclosed`: `true` (shows via tag)

---

## Audit Trail

### Complete Transparency

Every impersonated message stores:

1. **Public Identity** (`agent_id`): Who the message appears to be from
2. **Actual Author** (`authored_by`): Who really sent it
3. **Disclosure Flag** (`disclosed`): Whether via tag was shown

### Database Records

```sql
INSERT INTO messages (
  message_id,
  agent_id,        -- "agent:claude-cli"
  authored_by,     -- "user:leon"
  disclosed,       -- true/false
  body_content
) VALUES (...);
```

### Querying Audit Trail

```sql
-- Find all messages sent by a user as any agent
SELECT * FROM messages
WHERE authored_by = 'user:leon'
  AND agent_id != 'user:leon';

-- Find non-disclosed impersonations
SELECT * FROM messages
WHERE authored_by IS NOT NULL
  AND disclosed = FALSE;
```

---

## Use Cases

### 1. Human Intervention

**Scenario**: Agent conversation needs human oversight.

**Workflow**:

1. User navigates to agent's inbox
2. Reviews conversation thread
3. Sends message as agent to provide guidance
4. Message clearly marked with `[via user:X]` for transparency

### 2. Debugging Agent Behavior

**Scenario**: Developer testing agent message flow.

**Workflow**:

1. Developer views agent inbox
2. Sends test messages as agent
3. Observes responses and behavior
4. Can choose to disclose or not for testing

### 3. Manual Agent Operation

**Scenario**: Agent is offline or broken, human takes over.

**Workflow**:

1. User assumes agent identity
2. Handles pending messages manually
3. All actions audited with `authored_by` field
4. Can transition back to automated operation

### 4. Multi-Agent Coordination

**Scenario**: User needs to send messages from multiple agent perspectives.

**Workflow**:

1. Switch between agent inboxes
2. Compose messages from each perspective
3. Coordinate complex multi-agent workflows
4. Full audit trail maintained

---

## Security Considerations

### Authorization

**Current Implementation**: No authorization checks.

**Implications**:

- Any authenticated user can impersonate any agent
- Appropriate for development and small teams
- **Not suitable for production multi-tenant systems**

### Future Enhancements

**Recommended**:

1. Permission system for impersonation rights
2. Require explicit authorization per agent
3. Rate limiting on impersonated messages
4. Alerts for impersonation events
5. Admin audit logs

**Tracked in**: Epic 13 (UI Polish) or future security epic

### Audit Log Access

**Recommendation**: Implement audit log viewer showing:

- Who impersonated whom
- When impersonation occurred
- What messages were sent
- Disclosure status for each message

---

## Developer Guide

### Implementing Impersonation in New Components

**Step 1**: Receive impersonation props:

```typescript
interface YourComponentProps {
  sendingAs: string;
  isImpersonating: boolean;
}
```

**Step 2**: Show warning indicator:

```tsx
{
  isImpersonating && (
    <div className="text-yellow-600">⚠️ Sending as {sendingAs}</div>
  );
}
```

**Step 3**: Include disclosure checkbox:

```tsx
{
  isImpersonating && (
    <label>
      <Checkbox checked={disclosed} onCheckedChange={setDisclosed} />
      Show "via {currentUser?.username}"
    </label>
  );
}
```

**Step 4**: Include fields in API call:

```typescript
sendMessage({
  // ... other fields
  ...(isImpersonating && { acting_as: sendingAs, disclosed }),
});
```

### Testing Impersonation

```typescript
// Test with impersonation
render(
  <YourComponent
    sendingAs="agent:test"
    isImpersonating={true}
  />
);

// Verify warning shown
expect(screen.getByText(/Sending as agent:test/)).toBeInTheDocument();

// Verify disclosure checkbox shown
expect(screen.getByRole('checkbox')).toBeInTheDocument();
```

---

## UI/UX Best Practices

### Always Show Warning

**Rule**: Never hide the fact that user is impersonating.

**Rationale**: Prevents accidental impersonation and maintains trust.

### Default to Disclosed

**Rule**: Disclosure checkbox defaults to checked.

**Rationale**: Transparency by default. User must actively choose to hide
identity.

### Clear Visual Distinction

**Rule**: Impersonation UI elements use yellow/warning colors.

**Rationale**: Immediately recognizable as special state.

### Contextual Placement

**Rule**: Disclosure checkbox appears only where messages are composed.

**Rationale**: Directly tied to action being taken.

---

## Configuration

### Global Settings (Future)

**Planned Configuration**:

```typescript
interface ImpersonationConfig {
  allowImpersonation: boolean; // Enable/disable globally
  requireDisclosure: boolean; // Force disclosed=true
  auditLogEnabled: boolean; // Log all impersonation events
  authorizedUsers: string[]; // Who can impersonate
  authorizedAgents: string[]; // Which agents can be impersonated
}
```

**Not Yet Implemented**: All users can impersonate all agents currently.

---

## Troubleshooting

### Disclosure Checkbox Not Showing

**Check**:

1. Is `isImpersonating` prop true?
2. Is current user identity different from `sendingAs`?
3. Is `useCurrentUser()` returning valid user data?

### Messages Not Showing Via Tag

**Check**:

1. Was `disclosed: true` sent in API call?
2. Is backend storing `disclosed` field correctly?
3. Is MessageBubble checking `message.disclosed && message.authored_by`?

### Impersonation Warning Not Appearing

**Check**:

1. Is InboxHeader receiving `isImpersonating: true`?
2. Is conditional render checking `isImpersonating`?
3. Are yellow warning styles applied correctly?

---

## Related Documentation

- [Inbox Components](../components/inbox.md) - InboxView impersonation logic
- [Compose Components](../components/compose.md) - Disclosure checkbox
  implementation
- [Messaging Patterns](../patterns/messaging.md) - Message sending with
  impersonation

---

## Future Enhancements

### Impersonation History

Show history of impersonated actions:

```text
┌──────────────────────────────────────────┐
│ Impersonation History                    │
├──────────────────────────────────────────┤
│ ⚠️  2h ago: Sent as agent:cli (disclosed)│
│ ⚠️  1d ago: Sent as agent:daemon         │
└──────────────────────────────────────────┘
```

### Permission Management UI

Interface for configuring who can impersonate:

```text
┌──────────────────────────────────────────┐
│ Impersonation Permissions                │
├──────────────────────────────────────────┤
│ user:leon                                │
│   ☑ Can impersonate: agent:cli          │
│   ☑ Can impersonate: agent:daemon       │
│   ☐ Requires approval                   │
└──────────────────────────────────────────┘
```

### Impersonation Notifications

Alert recipients when receiving impersonated message:

```text
🔔 New message from agent:cli (via user:leon)
```

**Tracked in**: Epic 13 (UI Polish) or future security epic
