# Compose Components

Components for creating new message threads and replying to existing ones.

## ComposeModal

Modal dialog for creating new message threads.

### Location

`packages/web-app/src/components/inbox/ComposeModal.tsx`

### Props

```typescript
interface ComposeModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sendingAs: string; // Identity messages will be sent as
  isImpersonating: boolean; // Whether user is impersonating
}
```

### Features

- **Modal Dialog**: Full-screen overlay for composing
- **Recipient Field**: Input for recipient identity (currently text input)
- **Subject Field**: Optional thread title
- **Body Field**: Textarea for message content
- **Disclosure Checkbox**: Shows when impersonating
- **Send/Cancel Actions**: Form submission and dismissal

### Form Fields

```typescript
const [recipient, setRecipient] = useState("");
const [title, setTitle] = useState("");
const [content, setContent] = useState("");
const [disclosed, setDisclosed] = useState(true); // Default: transparent
```

### Visual Layout

```
┌──────────────────────────────────────────┐
│ New Message                           ✕  │
├──────────────────────────────────────────┤
│ To:                                      │
│ ┌──────────────────────────────────────┐ │
│ │ agent:claude-daemon                  │ │
│ └──────────────────────────────────────┘ │
│                                          │
│ Subject:                                 │
│ ┌──────────────────────────────────────┐ │
│ │ Need help with API                   │ │
│ └──────────────────────────────────────┘ │
│                                          │
│ Message:                                 │
│ ┌──────────────────────────────────────┐ │
│ │                                      │ │
│ │ Can you help me understand...        │ │
│ │                                      │ │
│ └──────────────────────────────────────┘ │
│                                          │
│ ☐ Show "via user:leon"                  │
│                                          │
│                   [Cancel]  [Send]       │
└──────────────────────────────────────────┘
```

### Behavior

#### Opening

Triggered by clicking "Compose" button in InboxHeader.

#### Submission

```typescript
const handleSubmit = (e: FormEvent) => {
  e.preventDefault();

  createThread(
    { title }, // Currently only title supported
    {
      onSuccess: () => {
        onOpenChange(false); // Close modal
        // Reset form fields
        setRecipient("");
        setTitle("");
        setContent("");
      },
    },
  );
};
```

#### Validation

- **Required**: Title field must not be empty
- **Disabled States**: Send button disabled when:
  - Title is empty
  - Request is pending

### Current Limitations

**Backend API Gap**: The `thread.create` RPC currently only accepts `title`.
Full implementation requires:

- `recipient` field
- `message` initial message body
- `acting_as` impersonation field
- `disclosed` flag

**Tracked in**: Backend task `thrum-8to.1`

### Future Enhancements

When backend supports full thread creation:

```typescript
createThread({
  title,
  recipient,
  message: { format: "markdown", content },
  ...(isImpersonating && { acting_as: sendingAs, disclosed }),
});
```

Planned improvements:

- Replace text input with Select dropdown for recipients
- Fetch agent list for recipient options
- Display "From" indicator
- Full message composition support

---

## InlineReply

Reply form shown at the bottom of expanded threads.

### Location

`packages/web-app/src/components/inbox/InlineReply.tsx`

### Props

```typescript
interface InlineReplyProps {
  threadId: string;
  sendingAs: string;
  isImpersonating: boolean;
}
```

### Features

- **Textarea Input**: Multi-line message composition
- **Send Button**: Submits reply to thread
- **Loading State**: Disabled during send operation
- **Disclosure Checkbox**: Shows when impersonating (defaults to checked)
- **Auto-clear**: Clears textarea after successful send
- **Validation**: Prevents sending empty messages

### Form State

```typescript
const [content, setContent] = useState("");
const [disclosed, setDisclosed] = useState(true); // Default: transparent
const { mutate: sendMessage, isPending } = useSendMessage();
```

### Visual Layout

**Normal Mode (Not Impersonating)**:

```
┌──────────────────────────────────────────┐
│ ┌──────────────────────────────────────┐ │
│ │ Write a reply...                     │ │
│ │                                      │ │
│ └──────────────────────────────────────┘ │
│                               [Send]     │
└──────────────────────────────────────────┘
```

**Impersonation Mode**:

```
┌──────────────────────────────────────────┐
│ ┌──────────────────────────────────────┐ │
│ │ Write a reply...                     │ │
│ │                                      │ │
│ └──────────────────────────────────────┘ │
│ ☑ Show "via user:leon"      [Send]      │
└──────────────────────────────────────────┘
```

### Send Behavior

```typescript
const handleSubmit = (e: FormEvent) => {
  e.preventDefault();
  if (!content.trim()) return; // Prevent empty sends

  sendMessage(
    {
      thread_id: threadId,
      body: { format: "markdown", content },
      ...(isImpersonating && { acting_as: sendingAs, disclosed }),
    },
    {
      onSuccess: () => setContent(""), // Clear on success
    },
  );
};
```

### Disclosure Checkbox

The disclosure checkbox controls whether impersonation is visible to recipients:

- **Checked** (`disclosed: true`): Message shows `[via user:X]` badge
- **Unchecked** (`disclosed: false`): Message appears to come directly from
  agent

**Default**: Always checked (transparent by default)

**Visibility**: Only shown when `isImpersonating` is true

### Validation Rules

- **Empty Check**: `content.trim()` must not be empty
- **Button Disabled When**:
  - Content is empty
  - Send is pending
- **Textarea Disabled When**: Send is pending

---

## Integration with Hooks

### useCreateThread

Used by ComposeModal for new thread creation.

```typescript
const { mutate: createThread, isPending } = useCreateThread();

createThread(
  { title: "New Thread" },
  {
    onSuccess: () => {
      // Close modal, reset form
    },
  },
);
```

**Query Invalidation**: Automatically invalidates `['threads', 'list']` to
refresh inbox.

### useSendMessage

Used by InlineReply for sending messages to existing threads.

```typescript
const { mutate: sendMessage, isPending } = useSendMessage();

sendMessage(
  {
    thread_id: "thread-123",
    body: { format: "markdown", content: "Hello!" },
    acting_as: "agent:cli", // Optional impersonation
    disclosed: true, // Optional disclosure
  },
  {
    onSuccess: () => {
      // Clear form
    },
  },
);
```

**Query Invalidation**: Automatically invalidates:

- `['messages', 'list']` - Message lists
- `['threads']` - Thread data (to update message counts)

---

## Testing

### ComposeModal Tests

**Test Scenarios**:

- Modal opens and closes
- Form fields accept input
- Send button disabled when title empty
- Form resets after successful send
- Disclosure checkbox appears when impersonating
- Cancel button closes modal

### InlineReply Tests

**Test Scenarios**:

- Textarea accepts input
- Send button disabled when content empty
- Content clears after successful send
- Disclosure checkbox appears when impersonating
- Loading state disables controls during send
- Empty messages cannot be sent

---

## Keyboard Shortcuts

### Planned (Not Yet Implemented)

- **Cmd/Ctrl + Enter**: Send message
- **Escape**: Close ComposeModal

**Tracked in**: Task closure notes for `thrum-kba` and `thrum-h9l`

---

## Error Handling

### Current Implementation

- **Validation**: Client-side validation prevents empty sends
- **Loading States**: UI disabled during pending operations
- **Success Handling**: Forms clear on successful send

### Planned Improvements

- Display error messages on send failure
- Retry mechanism for failed sends
- Network error indicators

**Tracked in**: Task closure notes for `thrum-kba` and `thrum-h9l`

---

## Styling

### Tailwind Classes

Both components use Tailwind CSS and shadcn/ui components:

- **Dialog**: `Dialog`, `DialogContent`, `DialogHeader`, `DialogTitle`,
  `DialogFooter`
- **Form**: `space-y-4`, `space-y-2`
- **Inputs**: `Input`, `Textarea`, `Label`
- **Actions**: `Button` with variants (`default`, `outline`)
- **Checkbox**: `Checkbox` with label

### Responsive Design

- **ComposeModal**: `sm:max-w-[500px]` - Centered modal on larger screens
- **Textarea**: `min-h-[60px]` (InlineReply), `min-h-[120px]` (ComposeModal)
- **Resize**: `resize-none` - Fixed height textareas

---

## Dependencies

### Hooks (from `@thrum/shared-logic`)

- `useCreateThread()` - Create new threads (ComposeModal)
- `useSendMessage()` - Send messages to threads (InlineReply)
- `useCurrentUser()` - Get current user for disclosure text

### UI Components (from shadcn/ui)

- `Dialog`, `DialogContent`, `DialogHeader`, `DialogTitle`, `DialogFooter` -
  Modal
- `Button` - Actions
- `Input` - Single-line text fields
- `Label` - Form labels
- `Textarea` - Multi-line text input
- `Checkbox` - Disclosure option

### External Libraries

- `lucide-react` - Icons (`Loader2` spinner, `PenSquare` compose icon)

---

## Backend Dependencies

### Full ComposeModal Support

**Requires**: Backend task `thrum-8to.1` (Enhance thread.create RPC)

**Missing Fields**:

- `recipient` - Who to send to
- `message` - Initial message body
- `acting_as` - Impersonation identity
- `disclosed` - Show via tag

### Current Workaround

ComposeModal only sends `title` field. User must:

1. Create thread with title
2. Navigate to thread
3. Use InlineReply to send first message

### When Backend Complete

Single-step thread creation with initial message will work seamlessly.
