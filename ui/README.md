# Thrum UI

Web and terminal user interfaces for the Thrum agent messaging system.

## Monorepo Structure

This is a Turborepo monorepo containing three packages:

```text
ui/
├── packages/
│   ├── shared-logic/     # Framework-agnostic business logic
│   ├── web-app/          # React web application
│   └── tui-app/          # Ink terminal UI (placeholder)
├── turbo.json           # Turborepo configuration
├── package.json         # Root package with monorepo scripts
├── pnpm-workspace.yaml  # pnpm workspace configuration
└── tsconfig.json        # Shared TypeScript configuration
```

## Package Responsibilities

### `@thrum/shared-logic`

Framework-agnostic business logic shared between web and TUI applications.

**Contents:**

- WebSocket client for daemon communication
- JSON-RPC 2.0 request/response handling
- TanStack Query hooks for data fetching
- TanStack Store for state management
- TypeScript types and Zod schemas
- Automatic reconnection with exponential backoff

**Key Features:**

- Connection state management
- Event streaming subscriptions
- Request timeout handling
- Type-safe API calls

### `@thrum/web-app`

React-based web application built with Vite.

**Tech Stack:**

- React 18+ with TypeScript
- Vite for bundling and dev server
- Tailwind CSS for styling
- shadcn/ui component library
- React Router for navigation
- TanStack Query for server state

**Features:**

- User registration and authentication
- Real-time WebSocket connection
- Responsive dashboard
- Protected routes

### `@thrum/tui-app`

Terminal UI application (placeholder for future implementation).

**Planned Tech Stack:**

- Ink for terminal rendering
- Same shared-logic as web-app
- Real-time updates in terminal

## Development Setup

### Prerequisites

- Node.js >=20.0.0
- pnpm >=9.0.0
- Thrum daemon running (for WebSocket connection)

### Installation

```bash
# Install dependencies for all packages
pnpm install
```

### Running the Development Server

```bash
# Start all packages in dev mode
pnpm dev

# Or run specific package
cd packages/web-app
pnpm dev
```

The web app will be available at `http://localhost:5173` by default.

### WebSocket Configuration

The web app connects to the Thrum daemon via WebSocket. By default, it connects
to:

- **Development**: `ws://localhost:9842`
- **Production**: `ws://<hostname>:9842`

The WebSocket proxy is configured in `packages/web-app/vite.config.ts`.

## Building

```bash
# Build all packages
pnpm build

# Type-check all packages
pnpm type-check
```

## Testing

```bash
# Run tests for all packages
pnpm test

# Run tests with coverage
cd packages/shared-logic
pnpm test:coverage

# Watch mode for development
pnpm test:watch
```

### Test Coverage Targets

- **shared-logic**: >40% (foundation phase, will increase in Epic 11+)
- **web-app**: >70%

## Linting

```bash
# Lint all packages
pnpm lint
```

## Package Dependencies

### Dependency Graph

```text
web-app → shared-logic
tui-app → shared-logic
```

Both UI applications depend on `shared-logic` to access the WebSocket client,
API hooks, and state management.

## Project Structure Deep Dive

### shared-logic Package

```text
packages/shared-logic/
├── src/
│   ├── api/
│   │   ├── client.ts        # WebSocket client singleton
│   │   ├── websocket.ts     # ThrumWebSocket class
│   │   └── index.ts
│   ├── hooks/
│   │   ├── useAgent.ts      # Agent list hooks
│   │   ├── useAuth.ts       # User registration hooks
│   │   ├── useMessage.ts    # Message/inbox hooks
│   │   ├── useThread.ts     # Thread management hooks
│   │   ├── useWebSocket.ts  # WebSocket state hooks
│   │   └── index.ts
│   ├── stores/
│   │   └── index.ts         # TanStack Store (future)
│   ├── types/
│   │   ├── api.ts           # API request/response types
│   │   ├── websocket.ts     # WebSocket & JSON-RPC types
│   │   └── index.ts
│   └── index.ts             # Public API exports
├── package.json
├── tsconfig.json
└── vitest.config.ts
```

### web-app Package

```text
packages/web-app/
├── src/
│   ├── pages/
│   │   ├── LoginPage.tsx      # User registration
│   │   └── DashboardPage.tsx  # Main dashboard
│   ├── components/
│   │   └── ui/                # shadcn/ui components
│   ├── lib/
│   │   └── utils.ts           # Utility functions
│   ├── App.tsx                # Router configuration
│   ├── main.tsx               # App entry point
│   └── index.css              # Global styles
├── index.html
├── package.json
├── tailwind.config.js
├── tsconfig.json
└── vite.config.ts
```

## Component Architecture

### Layout Components

The UI is built with a hierarchical component structure:

```text
AppShell
├── Header (fixed)
│   ├── Branding ("Thrum")
│   ├── User Badge
│   └── Settings Button
├── Sidebar (fixed width, scrollable)
│   ├── Live Feed Nav
│   ├── My Inbox Nav
│   └── AgentList
│       └── AgentCard × N
└── Main Content Area (flexible, scrollable)
    ├── LiveFeed View
    ├── InboxView (My Inbox)
    └── InboxView (Agent-specific)
```

### Core Components

#### AppShell

Main application container that provides the overall layout structure.

```tsx
import { AppShell } from "./components/AppShell";

<AppShell>
  <YourContentHere />
</AppShell>;
```

**Features:**

- Fixed header with branding and user info
- Fixed-width sidebar (w-64 = 256px)
- Flexible main content area
- Proper spacing (pt-14 for header offset)

#### Sidebar

Navigation sidebar with three sections: Live Feed, My Inbox, and Agent List.

```tsx
import { Sidebar } from "./components/Sidebar";

<Sidebar />;
```

**Features:**

- Uses global UI store for navigation state
- Highlights active view
- Scrollable agent list
- Integrates with `SidebarItem` for consistent navigation UI

#### SidebarItem

Reusable navigation button component.

```tsx
<SidebarItem
  icon={<span>●</span>}
  label="Live Feed"
  active={isActive}
  badge={unreadCount}
  onClick={handleClick}
/>
```

**Props:**

- `icon`: React node for the icon
- `label`: Display text
- `active`: boolean for active state styling
- `badge?`: Optional number for unread counts
- `onClick`: Click handler

#### AgentCard

Displays agent information in the sidebar.

```tsx
<AgentCard
  agent={agentInfo}
  active={isSelected}
  onClick={() => selectAgent(agent.identity)}
/>
```

**Props:**

- `agent`: AgentInfo object (identity, unreadCount, lastCheckin, status)
- `active`: boolean indicating if this agent's inbox is currently viewed
- `onClick`: Handler for agent selection

**Features:**

- Shows agent identity
- Displays unread message count (when > 0)
- Shows relative time since last checkin
- Status indicator (online/offline)
- Active state styling (bg-accent vs hover:bg-accent/50)

#### AgentList

Container for all agent cards with sorting and loading states.

```tsx
<AgentList />
```

**Features:**

- Automatically sorts agents by last checkin (most recent first)
- Shows agent count in header
- Handles loading state
- Integrates with UI store for agent selection

#### LiveFeed

Real-time activity stream showing all messages and events.

```tsx
<LiveFeed />
```

**Features:**

- Displays feed items chronologically
- Shows sender → receiver for each message
- Message preview with truncation
- Relative timestamps
- Clickable items for navigation (future)

#### FeedItem

Individual feed item component.

```tsx
<FeedItem item={feedItemData} onClick={() => navigateToThread(item)} />
```

**Props:**

- `item`: FeedItem object (from, to, preview, timestamp, type)
- `onClick`: Navigation handler

#### InboxView

Displays inbox for current user or specific agent.

```tsx
// My Inbox
<InboxView />

// Agent-specific inbox
<InboxView identityId="agent:claude-daemon" />
```

**Props:**

- `identityId?`: Optional agent identity for agent-specific inbox

**Features:**

- Empty state handling
- Conditional title based on identityId
- Ready for TanStack Query integration

### View Routing

The `DashboardPage` component handles view routing based on UI store state:

```tsx
export function DashboardPage() {
  const { selectedView, selectedAgentId } = useStore(uiStore);

  return (
    <AppShell>
      {selectedView === "live-feed" && <LiveFeed />}
      {selectedView === "my-inbox" && <InboxView />}
      {selectedView === "agent-inbox" && selectedAgentId && (
        <InboxView identityId={selectedAgentId} />
      )}
    </AppShell>
  );
}
```

## Messaging Documentation

Comprehensive documentation for messaging features:

### Components

- **[Inbox Components](docs/ui/components/inbox.md)** - InboxView, ThreadList,
  ThreadItem, MessageBubble
- **[Compose Components](docs/ui/components/compose.md)** - ComposeModal,
  InlineReply

### Features

- **[Impersonation](docs/ui/features/impersonation.md)** - User-as-agent
  messaging with audit trails

### Patterns

- **[Messaging Patterns](docs/ui/patterns/messaging.md)** - Data flow, caching,
  real-time updates

## State Management Patterns

### TanStack Store for UI State

We use [TanStack Store](https://tanstack.com/store) for global UI state
management. Store is lightweight, type-safe, and framework-agnostic.

#### UI Store Structure

```typescript
// shared-logic/src/stores/uiStore.ts
export interface UIState {
  selectedView: "live-feed" | "my-inbox" | "agent-inbox";
  selectedAgentId: string | null;
}

export const uiStore = new Store<UIState>({
  selectedView: "live-feed",
  selectedAgentId: null,
});
```

#### Store Actions

Encapsulate state updates in action functions:

```typescript
export const selectLiveFeed = () => {
  uiStore.setState((state) => ({
    ...state,
    selectedView: "live-feed",
    selectedAgentId: null,
  }));
};

export const selectAgent = (agentId: string) => {
  uiStore.setState((state) => ({
    ...state,
    selectedView: "agent-inbox",
    selectedAgentId: agentId,
  }));
};
```

#### Using Store in Components

```tsx
import { useStore } from "@tanstack/react-store";
import { uiStore, selectLiveFeed } from "@thrum/shared-logic";

function MyComponent() {
  const { selectedView } = useStore(uiStore);

  return (
    <button onClick={selectLiveFeed}>
      {selectedView === "live-feed" ? "Active" : "Go to Feed"}
    </button>
  );
}
```

### TanStack Query for Server State

We use [TanStack Query](https://tanstack.com/query) for all server data fetching
and caching.

#### Mock Hooks (Development)

During UI development, we use mock hooks that return static data:

```typescript
// hooks/useAgents.ts
export function useAgents() {
  return {
    data: [
      { identity: "agent:claude-daemon", unreadCount: 5 /* ... */ },
      { identity: "agent:claude-cli", unreadCount: 0 /* ... */ },
    ],
    isLoading: false,
  };
}
```

#### Future: Real TanStack Query Hooks

These will be replaced with real query hooks:

```typescript
export function useAgents() {
  return useQuery({
    queryKey: ["agents"],
    queryFn: async () => {
      return wsClient.call("agent.list");
    },
  });
}
```

### State Management Best Practices

1. **UI State → TanStack Store**: Navigation, selections, UI toggles
2. **Server State → TanStack Query**: API data, caching, refetching
3. **Local State → useState**: Component-specific state
4. **Derived State → useMemo**: Computed values (sorting, filtering)

## Real-time Update Strategy

### Current Implementation (Mocks)

The UI is structured to support real-time updates via WebSocket events.
Currently using mock data that simulates real-time behavior.

### Future WebSocket Integration

Real-time updates will follow this pattern:

```typescript
// In component or custom hook
useEffect(() => {
  const unsubscribe = wsClient.on("agent.checkin", (data) => {
    // Update TanStack Query cache
    queryClient.setQueryData(["agents"], (old: AgentInfo[]) => {
      return old.map((agent) =>
        agent.identity === data.identity
          ? { ...agent, lastCheckin: data.timestamp }
          : agent,
      );
    });
  });

  return unsubscribe;
}, []);
```

### Event-to-Cache Mapping

| WebSocket Event    | Query Cache Key            | Action              |
| ------------------ | -------------------------- | ------------------- |
| `agent.registered` | `['agents']`               | Append new agent    |
| `agent.checkin`    | `['agents']`               | Update lastCheckin  |
| `message.created`  | `['messages']`, `['feed']` | Prepend new message |
| `thread.updated`   | `['threads', threadId]`    | Update thread data  |

### Optimistic Updates

For user actions, we use optimistic updates:

```typescript
const sendMessage = useMutation({
  mutationFn: (content: string) => wsClient.call("message.send", { content }),
  onMutate: async (newMessage) => {
    // Optimistically add to cache
    await queryClient.cancelQueries({ queryKey: ["messages"] });
    const previous = queryClient.getQueryData(["messages"]);

    queryClient.setQueryData(["messages"], (old) => [newMessage, ...old]);

    return { previous };
  },
  onError: (err, newMessage, context) => {
    // Rollback on error
    queryClient.setQueryData(["messages"], context.previous);
  },
});
```

## Styling Guidelines

### Tailwind CSS

We use [Tailwind CSS](https://tailwindcss.com/) for all styling. Tailwind
provides utility-first CSS classes.

#### Key Tailwind Patterns

**Layout:**

```tsx
<div className="flex h-screen overflow-hidden">
  <aside className="w-64 flex-shrink-0 overflow-y-auto">
  <main className="flex-1 overflow-auto">
```

**Spacing:**

- Use spacing scale: `p-4`, `px-3`, `py-2`, `mt-1`, `gap-2`
- Consistent padding: `p-2` for containers, `p-3` for cards, `p-4` for main
  areas

**Typography:**

- Headings: `text-lg font-semibold`, `text-2xl font-bold`
- Body: `text-sm`, `text-xs`
- Muted: `text-muted-foreground`

**Colors:**

- Use semantic tokens: `bg-background`, `text-foreground`, `bg-accent`,
  `text-muted-foreground`
- Hover states: `hover:bg-accent/50`, `hover:text-foreground`

### shadcn/ui Components

We use [shadcn/ui](https://ui.shadcn.com/) as our component library. These are
not npm packages but copied into your project.

#### Available Components

Located in `packages/web-app/src/components/ui/`:

- Button
- Card
- Badge
- Separator
- (More added as needed)

#### Using shadcn/ui Components

```tsx
import { Card, CardHeader, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

<Card>
  <CardHeader>
    <div className="flex justify-between">
      <span>Title</span>
      <Badge>New</Badge>
    </div>
  </CardHeader>
  <CardContent>Content here</CardContent>
</Card>;
```

#### Adding New shadcn/ui Components

```bash
# Use the shadcn CLI to add components
npx shadcn-ui@latest add button
npx shadcn-ui@latest add dialog
```

### Custom Styling Utilities

#### cn() Utility

Merge Tailwind classes safely:

```typescript
import { cn } from '@/lib/utils';

<button
  className={cn(
    'w-full p-3 rounded-md',
    active ? 'bg-accent' : 'hover:bg-accent/50'
  )}
/>
```

#### Component Styling Patterns

**Interactive Elements:**

```tsx
className = "rounded-md hover:bg-accent/50 transition-colors";
```

**Active States:**

```tsx
className={cn(
  'base-classes',
  isActive && 'bg-accent text-accent-foreground'
)}
```

**Loading States:**

```tsx
{
  isLoading ? (
    <div className="flex items-center justify-center p-4">
      <span className="text-muted-foreground">Loading...</span>
    </div>
  ) : (
    <Content />
  );
}
```

### Design Tokens

Key design tokens from Tailwind config:

- **Widths**: Sidebar = `w-64` (16rem/256px), Header = `h-14` (3.5rem/56px)
- **Spacing**: Base unit = 0.25rem (4px)
- **Border Radius**: `rounded-md` = 0.375rem (6px)
- **Transitions**: Use `transition-colors` for smooth hover effects

## API Reference

See the [WebSocket API documentation](../../docs/api/websocket.md) for complete
API reference.

### Quick Examples

#### Using Hooks in Components

```tsx
import {
  useAgentList,
  useMessageList,
  useSendMessage,
} from "@thrum/shared-logic";

function Dashboard() {
  const { data: agents, isLoading } = useAgentList();
  const { data: messages } = useMessageList({ page_size: 20 });
  const sendMessage = useSendMessage();

  const handleSend = async (content: string) => {
    await sendMessage.mutateAsync({ content });
  };

  return <div>{/* Your UI here */}</div>;
}
```

#### Direct WebSocket Client Usage

```typescript
import { wsClient } from "@thrum/shared-logic";

// Connect
await wsClient.connect();

// Make RPC call
const response = await wsClient.call("agent.list");

// Subscribe to events
const unsubscribe = wsClient.on("message.created", (data) => {
  console.log("New message:", data);
});

// Disconnect
wsClient.disconnect();
```

## Troubleshooting

### WebSocket Connection Fails

1. Ensure the Thrum daemon is running
2. Check that the daemon is listening on port 9842
3. Verify no firewall is blocking the connection
4. Check browser console for error messages

### Tests Failing

1. Run `pnpm install` to ensure dependencies are up to date
2. Clear `node_modules` and reinstall: `rm -rf node_modules && pnpm install`
3. Check that you're using Node.js >=20.0.0: `node --version`

### Build Errors

1. Run type check to identify issues: `pnpm type-check`
2. Ensure all dependencies are installed
3. Clear build cache: `rm -rf .turbo dist`

## Next Steps

- **Epic 11**: UI Core - Main interface with agent list, inbox, message composer
- **Epic 12**: UI Messaging - Thread view, message editing, search
- **Epic 13**: UI Polish - Themes, notifications, keyboard shortcuts

## Contributing

This is part of the Thrum project. See the [main README](../../README.md) for
contribution guidelines.

## License

MIT
