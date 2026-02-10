# Messaging Patterns

Data flow patterns and best practices for messaging in Thrum UI.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                      UI Components                          │
│  InboxView → ThreadList → ThreadItem → MessageBubble        │
│                              ↓           InlineReply         │
├─────────────────────────────────────────────────────────────┤
│                   TanStack Query Hooks                      │
│  useThreadList() • useThread() • useSendMessage()           │
│  useMarkAsRead() • useCreateThread()                        │
├─────────────────────────────────────────────────────────────┤
│                   WebSocket Client                          │
│  JSON-RPC 2.0 over WebSocket                                │
├─────────────────────────────────────────────────────────────┤
│                   Thrum Daemon                              │
│  thread.* • message.* RPC methods                           │
└─────────────────────────────────────────────────────────────┘
```

---

## Data Fetching Patterns

### Thread List

**Pattern**: Query with pagination

```typescript
// Hook: useThreadList()
const { data, isLoading, error } = useThreadList({
  page_size: 20,
  page: 1,
});

// Response
type ThreadListResponse = {
  threads: Thread[];
  page: number;
  page_size: number;
  total_count: number;
  total_pages: number;
};
```

**Cache Key**: `['threads', 'list', params]`

**Stale Time**: 5000ms (5 seconds)

**Invalidation**:

- After creating new thread
- After sending message (updates thread list)
- After marking messages as read (updates unread counts)

---

### Individual Thread

**Pattern**: Lazy loading with enabled option

```typescript
// Hook: useThread()
const { data, isLoading } = useThread(threadId, {
  enabled: expanded, // Only fetch when expanded
});

// Response
type ThreadGetResponse = {
  thread_id: string;
  title: string;
  messages: Message[];
  total_messages: number;
};
```

**Cache Key**: `['threads', 'detail', threadId, params]`

**Stale Time**: 1000ms (1 second)

**Enabled Control**:

- Prevents fetching until needed
- Used by ThreadItem to lazy load on expand

**Invalidation**:

- After sending message to thread
- After marking messages as read

---

## Mutation Patterns

### Sending Messages

**Pattern**: Mutation with optimistic updates

```typescript
// Hook: useSendMessage()
const { mutate: sendMessage, isPending } = useSendMessage();

sendMessage(
  {
    thread_id: "thread-123",
    body: { format: "markdown", content: "Hello!" },
    acting_as: "agent:cli", // Optional
    disclosed: true, // Optional
  },
  {
    onSuccess: () => {
      // Handle success (e.g., clear form)
    },
  },
);
```

**Query Invalidation** (Automatic):

```typescript
onSuccess: () => {
  queryClient.invalidateQueries({ queryKey: ["messages", "list"] });
  queryClient.invalidateQueries({ queryKey: ["threads"] });
};
```

**Result**: Thread list and detail automatically refetch

---

### Creating Threads

**Pattern**: Mutation with cache invalidation

```typescript
// Hook: useCreateThread()
const { mutate: createThread, isPending } = useCreateThread();

createThread(
  { title: "New Thread" },
  {
    onSuccess: (data) => {
      // data.thread_id available
      // Navigate to new thread or close modal
    },
  },
);
```

**Query Invalidation** (Automatic):

```typescript
onSuccess: () => {
  queryClient.invalidateQueries({ queryKey: ["threads", "list"] });
};
```

**Result**: Thread list refetches to show new thread

---

### Marking Messages as Read

**Pattern**: Mutation with optimistic cache updates

```typescript
// Hook: useMarkAsRead()
const { mutate: markAsRead } = useMarkAsRead();

markAsRead(messageIds);
```

**Optimistic Update**:

```typescript
onSuccess: (data, messageIds) => {
  // Invalidate to refetch with new counts
  queryClient.invalidateQueries({ queryKey: ["threads"] });

  // Optimistically update message is_read status
  queryClient.setQueriesData({ queryKey: ["threads", "detail"] }, (old) => {
    if (!old?.messages) return old;
    return {
      ...old,
      messages: old.messages.map((msg) =>
        messageIds.includes(msg.message_id) ? { ...msg, is_read: true } : msg,
      ),
    };
  });
};
```

**Result**:

- Messages immediately marked as read in UI
- Unread counts refetch from server

---

## State Management

### UI State (Local)

**Component-Level State**:

- Expanded thread ID (ThreadList)
- Form inputs (InlineReply, ComposeModal)
- Disclosure checkbox state

**Example**:

```typescript
// ThreadList.tsx
const [expandedThreadId, setExpandedThreadId] = useState<string | null>(null);
```

**Pattern**: Keep UI state local, don't lift unnecessarily

---

### Server State (TanStack Query)

**Query Cache**:

- Thread lists
- Individual threads with messages
- Current user data

**Pattern**: Single source of truth from server

**Benefits**:

- Automatic background refetching
- Optimistic updates
- Cache deduplication
- Loading/error states

---

### Global State (Planned)

**TanStack Store** (for future features):

- Selected agent context
- WebSocket connection status
- Global notifications

**Not Yet Used**: Current implementation uses only TanStack Query

---

## Real-Time Updates

### WebSocket Events (Planned)

**Pattern**: Event-driven cache updates

```typescript
// Hook: useWebSocketEvents() (planned)
useEffect(() => {
  const unsubscribe = wsClient.on("message.new", (event) => {
    // Update thread list with new message
    queryClient.invalidateQueries({ queryKey: ["threads", "list"] });

    // If thread is currently viewed, refetch detail
    queryClient.invalidateQueries({
      queryKey: ["threads", "detail", event.thread_id],
    });
  });

  return unsubscribe;
}, [wsClient, queryClient]);
```

**Events**:

- `message.new` - New message in thread
- `thread.updated` - Thread metadata changed (unread count, etc.)

**Backend Dependency**: Task `thrum-8to.4` (WebSocket events)

---

## Performance Optimizations

### 1. Lazy Loading

**Pattern**: Only fetch data when needed

**Implementation**:

```typescript
useThread(threadId, {
  enabled: expanded, // Only fetch when thread is expanded
});
```

**Benefit**: Reduces initial load and unnecessary network requests

---

### 2. Debouncing

**Pattern**: Batch rapid operations

**Implementation**:

```typescript
useEffect(() => {
  if (!unreadIds.length) return;

  const timer = setTimeout(() => {
    markAsRead.mutate(unreadIds); // After 500ms
  }, 500);

  return () => clearTimeout(timer);
}, [unreadIds]);
```

**Use Cases**:

- Mark as read (500ms debounce)
- Search input (future)
- Auto-save drafts (future)

**Benefit**: Reduces API calls and server load

---

### 3. Query Deduplication

**Pattern**: Automatic by TanStack Query

**How It Works**:

- Multiple components requesting same data
- Only one network request made
- Results shared across all subscribers

**Example**:

```typescript
// Component A
useThread("thread-123");

// Component B (same thread)
useThread("thread-123"); // Uses cached data, no new request
```

**Benefit**: Eliminates redundant requests

---

### 4. Stale-While-Revalidate

**Pattern**: Show cached data immediately, fetch in background

**Configuration**:

```typescript
useQuery({
  queryKey: ["threads", "list"],
  queryFn: fetchThreadList,
  staleTime: 5000, // Data fresh for 5 seconds
});
```

**Behavior**:

1. Return cached data immediately (if available)
2. If data is stale (>5s old), refetch in background
3. Update UI when fresh data arrives

**Benefit**: Instant perceived loading, always fresh data

---

## Error Handling

### Query Errors

**Pattern**: Render error state

```typescript
const { data, isLoading, error } = useThreadList();

if (error) {
  return <div>Error loading threads: {error.message}</div>;
}
```

**Retry Behavior**:

- TanStack Query automatically retries failed requests
- Exponential backoff by default
- Can be configured per query

---

### Mutation Errors

**Pattern**: Handle in onError callback

```typescript
sendMessage(
  { ... },
  {
    onError: (error) => {
      console.error('Failed to send:', error);
      // Show user-facing error message
    },
  }
);
```

**Current Limitation**: No user-facing error UI yet

**Planned**: Error toast notifications (Epic 13)

---

## Testing Patterns

### Mocking Hooks

**Pattern**: Mock at the hook level

```typescript
import * as hooks from '@thrum/shared-logic';

vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useThreadList: vi.fn(),
    useCurrentUser: vi.fn(),
  };
});

// In test
vi.mocked(hooks.useThreadList).mockReturnValue({
  data: { threads: [...] },
  isLoading: false,
  error: null,
} as any);
```

---

### QueryClient Wrapper

**Pattern**: Provide QueryClient for tests

```typescript
const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: false },  // Disable retries in tests
    mutations: { retry: false },
  },
});

render(
  <QueryClientProvider client={queryClient}>
    <YourComponent />
  </QueryClientProvider>
);
```

---

### Testing Mutations

**Pattern**: Mock mutation functions

```typescript
const mockSendMessage = vi.fn();
vi.mocked(hooks.useSendMessage).mockReturnValue({
  mutate: mockSendMessage,
  isPending: false,
} as any);

// Trigger send
await user.click(screen.getByText("Send"));

// Verify called
expect(mockSendMessage).toHaveBeenCalledWith(
  expect.objectContaining({
    thread_id: "thread-123",
    body: expect.any(Object),
  }),
  expect.any(Object),
);
```

---

## Best Practices

### 1. Consistent Query Keys

**Rule**: Use array-based query keys with structured format

```typescript
// Good
["threads", "list", params][("threads", "detail", threadId, params)][
  ("messages", "list", params)
][
  // Bad
  "threadList"
]["getThread"];
```

**Benefit**: Easy to invalidate related queries

---

### 2. Invalidate, Don't Mutate

**Rule**: Prefer invalidation over manual cache updates

```typescript
// Good: Let server be source of truth
queryClient.invalidateQueries({ queryKey: ["threads"] });

// Okay: For optimistic updates only
queryClient.setQueryData(["threads", "detail", id], newData);
```

**Exception**: Optimistic updates (mark as read)

---

### 3. Colocate Queries with Usage

**Rule**: Call hooks in the component that needs the data

```typescript
// Good
function ThreadItem({ thread }) {
  const { data } = useThread(thread.thread_id, { enabled: expanded });
  // Use data here
}

// Bad: Prop drilling
function ThreadItem({ thread, threadDetail }) {
  // Passed down from parent
}
```

**Benefit**: Components are self-sufficient

---

### 4. Handle Loading and Error States

**Rule**: Always check isLoading and error

```typescript
// Good
const { data, isLoading, error } = useThreadList();

if (isLoading) return <Loading />;
if (error) return <Error message={error.message} />;
return <ThreadList threads={data.threads} />;

// Bad: Assumes data always exists
const { data } = useThreadList();
return <ThreadList threads={data.threads} />;  // May crash
```

---

### 5. Use TypeScript for Safety

**Rule**: Type all hook parameters and returns

```typescript
// Good
const { data } = useThread(threadId: string);
// data is ThreadGetResponse | undefined

// Bad: Untyped
const { data } = useThread(threadId);
// data is any
```

---

## Common Patterns

### Auto-Refetch on Focus

**Pattern**: Refetch data when tab gains focus

```typescript
// Automatic with TanStack Query
useQuery({
  queryKey: ["threads", "list"],
  queryFn: fetchThreadList,
  refetchOnWindowFocus: true, // Default: true
});
```

**Use Case**: User switches back to tab, sees fresh data

---

### Dependent Queries

**Pattern**: Second query depends on first

```typescript
// First query
const { data: thread } = useThread(threadId);

// Second query (depends on first)
const { data: messages } = useMessages(thread?.thread_id, {
  enabled: !!thread, // Only run if thread exists
});
```

---

### Parallel Queries

**Pattern**: Multiple independent queries

```typescript
function Inbox() {
  const { data: threads } = useThreadList();
  const { data: user } = useCurrentUser();
  const { data: agents } = useAgentList();

  // All fetch in parallel
}
```

---

## Migration Notes

### From Redux to TanStack Query

**Old Pattern** (Redux):

```typescript
useEffect(() => {
  dispatch(fetchThreads());
}, [dispatch]);

const threads = useSelector((state) => state.threads.list);
```

**New Pattern** (TanStack Query):

```typescript
const { data: threads } = useThreadList();
```

**Benefits**:

- Less boilerplate
- Automatic loading states
- Built-in caching
- No manual state management

---

## Related Documentation

- [Inbox Components](../components/inbox.md) - Component implementations
- [Compose Components](../components/compose.md) - Form patterns
- [Impersonation](../features/impersonation.md) - Impersonation data flow

---

## Future Patterns

### Infinite Scroll

**Planned**: Load more threads as user scrolls

```typescript
const { data, fetchNextPage, hasNextPage } = useInfiniteQuery({
  queryKey: ["threads", "list"],
  queryFn: ({ pageParam = 1 }) => fetchThreadList({ page: pageParam }),
  getNextPageParam: (lastPage) =>
    lastPage.page < lastPage.total_pages ? lastPage.page + 1 : undefined,
});
```

### Optimistic Thread Creation

**Planned**: Show thread immediately, sync with server

```typescript
createThread(
  { title },
  {
    onMutate: async (newThread) => {
      // Cancel outgoing refetches
      await queryClient.cancelQueries({ queryKey: ["threads", "list"] });

      // Optimistically add to cache
      queryClient.setQueryData(["threads", "list"], (old) => ({
        ...old,
        threads: [newThread, ...old.threads],
      }));
    },
  },
);
```

### Persisted Queries

**Planned**: Save cache to localStorage

```typescript
import { persistQueryClient } from "@tanstack/react-query-persist-client";

persistQueryClient({
  queryClient,
  persister: createWebStoragePersister({ storage: window.localStorage }),
});
```

**Benefit**: Instant app startup with cached data
