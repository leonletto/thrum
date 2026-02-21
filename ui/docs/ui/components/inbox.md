# Inbox Components

Components for displaying threaded message conversations in inbox view.

## InboxView

Main inbox view container that displays thread list for any identity.

### Location

`packages/web-app/src/components/inbox/InboxView.tsx`

### Props

```typescript
interface InboxViewProps {
  identityId?: string; // Optional: whose inbox to show (defaults to current user)
}
```

### Features

- **Identity Detection**: Automatically determines whose inbox to display
  - If `identityId` provided: Show that agent/user's inbox
  - If not provided: Show current user's inbox
- **Impersonation Logic**: Automatically sets sending identity
  - Own inbox: Send as self
  - Agent inbox: Send as that agent (impersonation)
- **Loading States**: Shows loading indicator while fetching threads
- **Empty States**: Displays "No threads" message when inbox is empty

### Data Flow

```text
useCurrentUser() → Determine identity
                ↓
useThreadList() → Fetch threads for identity
                ↓
ThreadList → Display threads
```

### Impersonation Detection

```typescript
const sendingAs = useMemo(() => {
  if (!currentUser) return identity;
  if (identityId && identityId !== currentUser.username) {
    return identityId; // Impersonating: send as viewed agent
  }
  return currentUser.username; // Own inbox: send as self
}, [identityId, currentUser, identity]);

const isImpersonating = currentUser
  ? sendingAs !== currentUser.username
  : false;
```

### Example Usage

```tsx
// Show current user's inbox
<InboxView />

// Show specific agent's inbox (impersonation)
<InboxView identityId="agent:claude-daemon" />
```

---

## InboxHeader

Header bar showing identity, impersonation status, and compose button.

### Location

`packages/web-app/src/components/inbox/InboxHeader.tsx`

### Props

```typescript
interface InboxHeaderProps {
  identity: string; // Whose inbox (display name)
  sendingAs: string; // Who messages will be sent as
  isImpersonating: boolean; // Whether user is impersonating
}
```

### Features

- **Identity Display**: Shows whose inbox is being viewed
- **Compose Button**: Opens ComposeModal for new threads
- **Impersonation Warning**: Yellow warning when impersonating
  - Shows "Sending as {identity}" with warning icon
  - Only visible when `isImpersonating` is true

### Visual States

**Normal (Own Inbox)**:

```text
┌─────────────────────────────────────────┐
│ 📥 leon                      [Compose]  │
└─────────────────────────────────────────┘
```

**Impersonating (Agent Inbox)**:

```text
┌─────────────────────────────────────────┐
│ 📥 agent:claude-daemon      [Compose]   │
│ ⚠️  Sending as agent:claude-daemon      │
└─────────────────────────────────────────┘
```

---

## ThreadList

Container component that renders a list of threads.

### Location

`packages/web-app/src/components/inbox/ThreadList.tsx`

### Props

```typescript
interface ThreadListProps {
  threads: ThreadListResponse["threads"];
  sendingAs: string;
  isImpersonating: boolean;
}
```

### Features

- **Single Expansion**: Only one thread can be expanded at a time
- **Empty State**: Shows "No threads" message when list is empty
- **Expansion State**: Tracks which thread is expanded via local state

### State Management

```typescript
const [expandedThreadId, setExpandedThreadId] = useState<string | null>(null);

// Toggle expansion
const handleToggle = (threadId: string) => {
  setExpandedThreadId(expandedThreadId === threadId ? null : threadId);
};
```

---

## ThreadItem

Individual thread card with expand/collapse functionality.

### Location

`packages/web-app/src/components/inbox/ThreadItem.tsx`

### Props

```typescript
interface ThreadItemProps {
  thread: ThreadListResponse["threads"][number];
  expanded: boolean;
  onToggle: () => void;
  sendingAs: string;
  isImpersonating: boolean;
}
```

### Features

#### Collapsed State

- **Thread Title**: Shows thread title/subject
- **Message Count**: "X messages" indicator
- **Last Activity**: Relative timestamp (e.g., "2h ago")
- **Unread Badge**: Shows "X new" if `unread_count > 0`
- **Chevron Icon**: Right-pointing chevron

#### Expanded State

- **Lazy Loading**: Only fetches messages when expanded using `useThread()`
- **Message List**: Displays all messages in thread
- **Inline Reply**: Shows reply form at bottom
- **Auto Mark Read**: Marks unread messages as read after 500ms
- **Chevron Icon**: Down-pointing chevron

### Data Flow

```text
Thread collapsed → Click → Set expanded=true
                           ↓
useThread(id, { enabled: expanded }) → Fetch messages
                           ↓
Auto-mark unread messages (500ms debounce)
                           ↓
Display: MessageBubble[] + InlineReply
```

### Mark As Read Logic

```typescript
useEffect(() => {
  if (!expanded || !threadDetail?.messages) return;

  const unreadIds = threadDetail.messages
    .filter((m) => !m.is_read)
    .map((m) => m.message_id);

  if (unreadIds.length === 0) return;

  const timer = setTimeout(() => {
    markAsRead.mutate(unreadIds); // Mark after 500ms
  }, 500);

  return () => clearTimeout(timer);
}, [expanded, threadDetail?.messages, markAsRead]);
```

### Visual States

**Collapsed with Unread**:

```text
┌─────────────────────────────────────────┐
│ ▶ Bug in login flow [2 new]            │
│   💬 5 messages • 2h ago                │
└─────────────────────────────────────────┘
```

**Expanded**:

```text
┌─────────────────────────────────────────┐
│ ▼ Bug in login flow [2 new]            │
│   💬 5 messages • 2h ago                │
├─────────────────────────────────────────┤
│   [MessageBubble: "Found the issue"]   │
│   [MessageBubble: "Looks like..."]     │
│   [InlineReply form]                    │
└─────────────────────────────────────────┘
```

---

## MessageBubble

Individual message display with markdown rendering.

### Location

`packages/web-app/src/components/inbox/MessageBubble.tsx`

### Props

```typescript
interface MessageBubbleProps {
  message: Message;
  isOwn: boolean; // Whether sent by viewing identity
}
```

### Features

- **Markdown Rendering**: Uses `react-markdown` for content
- **Author Display**: Shows sender identity
- **Timestamp**: Relative time (e.g., "2m ago", "just now")
- **Impersonation Tag**: Shows `[via user:X]` badge if `disclosed: true`
- **Alignment**: Right-aligned for own messages, left-aligned for others
- **Styling**: Different background colors for own vs other messages

### Visual Layout

**Own Message**:

```text
                     ┌─────────────────────────┐
                     │ user:leon • 2m ago      │
                     │ Thanks for the help!    │
                     └─────────────────────────┘
```

**Other Message**:

```text
┌─────────────────────────┐
│ agent:claude • 5m ago   │
│ Happy to help!          │
└─────────────────────────┘
```

**Impersonated Message (Disclosed)**:

```text
┌───────────────────────────────────┐
│ agent:cli [via user:leon] • 1m ago│
│ Running tests now...              │
└───────────────────────────────────┘
```

### Markdown Support

Supports full markdown syntax:

- Headers
- Lists (ordered and unordered)
- Code blocks and inline code
- Links
- Emphasis (bold, italic)
- Blockquotes

---

## Testing

### Unit Tests

Location: `packages/web-app/src/components/inbox/__tests__/`

**InboxView.test.tsx** - 6 tests:

- Loading state rendering
- Empty thread list display
- User inbox header
- Agent inbox header
- Impersonation warning display
- Thread list rendering with data

### Test Pattern

```typescript
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { vi } from 'vitest';

// Mock hooks
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useThreadList: vi.fn(),
    useCurrentUser: vi.fn(),
  };
});

// Render with provider
const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false } },
});

render(
  <QueryClientProvider client={queryClient}>
    <InboxView />
  </QueryClientProvider>
);
```

---

## Performance Considerations

### Lazy Loading

- **Thread Messages**: Only fetched when thread is expanded
- **Enabled Option**: `useThread(id, { enabled: expanded })`
- Prevents loading all messages for all threads upfront

### Debouncing

- **Mark as Read**: 500ms debounce to batch operations
- Avoids excessive API calls when rapidly expanding threads

### Query Invalidation

- **On Send**: Invalidates thread list to show new message
- **On Mark Read**: Invalidates thread queries to update unread counts

---

## Dependencies

### Hooks (from `@thrum/shared-logic`)

- `useThreadList()` - Fetch thread list
- `useThread(id)` - Fetch individual thread with messages
- `useCurrentUser()` - Get current user identity
- `useMarkAsRead()` - Mark messages as read

### UI Components (from shadcn/ui)

- `Card`, `CardHeader`, `CardContent` - Thread cards
- `Button` - Actions and toggles
- `Badge` - Unread counts and status
- `ScrollArea` - Scrollable thread list

### External Libraries

- `react-markdown` - Markdown rendering in messages
- `lucide-react` - Icons (Inbox, ChevronDown, MessageSquare, etc.)
