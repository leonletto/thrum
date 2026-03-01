package types

// BaseEvent is the common structure for all events.
type BaseEvent struct {
	Type         string `json:"type"`
	Timestamp    string `json:"timestamp"`
	EventID      string `json:"event_id"`
	Version      int    `json:"v"`
	OriginDaemon string `json:"origin_daemon,omitempty"`
}

// MessageCreateEvent represents a message.create event.
type MessageCreateEvent struct {
	Type         string      `json:"type"`
	Timestamp    string      `json:"timestamp"`
	EventID      string      `json:"event_id"`
	Version      int         `json:"v"`
	OriginDaemon string      `json:"origin_daemon,omitempty"`
	MessageID    string      `json:"message_id"`
	ThreadID     string      `json:"thread_id,omitempty"`
	AgentID      string      `json:"agent_id"`
	SessionID    string      `json:"session_id"`
	Body         MessageBody `json:"body"`
	Scopes       []Scope     `json:"scopes,omitempty"`
	Refs         []Ref       `json:"refs,omitempty"`
	AuthoredBy   string      `json:"authored_by,omitempty"` // Actual author if impersonating
	Disclosed    bool        `json:"disclosed,omitempty"`   // Show [via user:X] in UI
}

// MessageBody represents the body of a message.
type MessageBody struct {
	Format     string `json:"format"`
	Content    string `json:"content"`
	Structured string `json:"structured,omitempty"` // JSON string
}

// Scope represents a message scope.
type Scope struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// Ref represents a message reference.
type Ref struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// MessageEditEvent represents a message.edit event.
type MessageEditEvent struct {
	Type         string      `json:"type"`
	Timestamp    string      `json:"timestamp"`
	EventID      string      `json:"event_id"`
	Version      int         `json:"v"`
	OriginDaemon string      `json:"origin_daemon,omitempty"`
	MessageID    string      `json:"message_id"`
	Body         MessageBody `json:"body"`
}

// MessageDeleteEvent represents a message.delete event.
type MessageDeleteEvent struct {
	Type         string `json:"type"`
	Timestamp    string `json:"timestamp"`
	EventID      string `json:"event_id"`
	Version      int    `json:"v"`
	OriginDaemon string `json:"origin_daemon,omitempty"`
	MessageID    string `json:"message_id"`
	Reason       string `json:"reason,omitempty"`
}

// ThreadUpdatedEvent represents a thread.updated event (real-time notification, not persisted).
type ThreadUpdatedEvent struct {
	Type         string  `json:"type"`
	Timestamp    string  `json:"timestamp"`
	EventID      string  `json:"event_id"`
	Version      int     `json:"v"`
	OriginDaemon string  `json:"origin_daemon,omitempty"`
	ThreadID     string  `json:"thread_id"`
	MessageCount int     `json:"message_count"`
	UnreadCount  int     `json:"unread_count"`
	LastActivity string  `json:"last_activity"`
	LastSender   string  `json:"last_sender"`
	Preview      *string `json:"preview,omitempty"`
}

// AgentRegisterEvent represents an agent.register event.
type AgentRegisterEvent struct {
	Type         string `json:"type"`
	Timestamp    string `json:"timestamp"`
	EventID      string `json:"event_id"`
	Version      int    `json:"v"`
	OriginDaemon string `json:"origin_daemon,omitempty"`
	AgentID      string `json:"agent_id"`
	Kind         string `json:"kind"`
	Name         string `json:"name,omitempty"` // Agent name (empty for legacy unnamed agents)
	Role         string `json:"role"`
	Module       string `json:"module"`
	Worktree     string `json:"worktree,omitempty"` // Worktree name (Decision 24)
	Display      string `json:"display,omitempty"`
	Hostname     string `json:"hostname,omitempty"`
}

// AgentSessionStartEvent represents an agent.session.start event.
type AgentSessionStartEvent struct {
	Type         string `json:"type"`
	Timestamp    string `json:"timestamp"`
	EventID      string `json:"event_id"`
	Version      int    `json:"v"`
	OriginDaemon string `json:"origin_daemon,omitempty"`
	SessionID    string `json:"session_id"`
	AgentID      string `json:"agent_id"`
}

// AgentSessionEndEvent represents an agent.session.end event.
type AgentSessionEndEvent struct {
	Type         string `json:"type"`
	Timestamp    string `json:"timestamp"`
	EventID      string `json:"event_id"`
	Version      int    `json:"v"`
	OriginDaemon string `json:"origin_daemon,omitempty"`
	SessionID    string `json:"session_id"`
	Reason       string `json:"reason,omitempty"`
}

// AgentUpdateEvent represents an agent.update event (work contexts).
type AgentUpdateEvent struct {
	Type         string               `json:"type"` // "agent.update"
	Timestamp    string               `json:"timestamp"`
	EventID      string               `json:"event_id"`
	Version      int                  `json:"v"`
	OriginDaemon string               `json:"origin_daemon,omitempty"`
	AgentID      string               `json:"agent_id"`
	WorkContexts []SessionWorkContext `json:"work_contexts"`
}

// SessionWorkContext represents work context for a session.
type SessionWorkContext struct {
	SessionID        string          `json:"session_id"`
	Branch           string          `json:"branch,omitempty"`
	WorktreePath     string          `json:"worktree_path,omitempty"`
	UnmergedCommits  []CommitSummary `json:"unmerged_commits,omitempty"`
	UncommittedFiles []string        `json:"uncommitted_files,omitempty"`
	ChangedFiles     []string        `json:"changed_files,omitempty"`
	FileChanges      []FileChange    `json:"file_changes,omitempty"`
	GitUpdatedAt     string          `json:"git_updated_at,omitempty"`
	CurrentTask      string          `json:"current_task,omitempty"`
	TaskUpdatedAt    string          `json:"task_updated_at,omitempty"`
	Intent           string          `json:"intent,omitempty"`
	IntentUpdatedAt  string          `json:"intent_updated_at,omitempty"`
}

// AgentCleanupEvent represents an agent.cleanup event.
type AgentCleanupEvent struct {
	Type         string `json:"type"` // "agent.cleanup"
	Timestamp    string `json:"timestamp"`
	EventID      string `json:"event_id"`
	Version      int    `json:"v"`
	OriginDaemon string `json:"origin_daemon,omitempty"`
	AgentID      string `json:"agent_id"` // Deleted agent name
	Reason       string `json:"reason,omitempty"`
	Method       string `json:"method,omitempty"` // "manual", "automated", "ui"
}

// GroupCreateEvent represents a group.create event.
type GroupCreateEvent struct {
	Type         string `json:"type"` // "group.create"
	Timestamp    string `json:"timestamp"`
	EventID      string `json:"event_id"`
	Version      int    `json:"v"`
	OriginDaemon string `json:"origin_daemon,omitempty"`
	GroupID      string `json:"group_id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	CreatedBy    string `json:"created_by"`
}

// GroupMemberAddEvent represents a group.member.add event.
type GroupMemberAddEvent struct {
	Type         string `json:"type"` // "group.member.add"
	Timestamp    string `json:"timestamp"`
	EventID      string `json:"event_id"`
	Version      int    `json:"v"`
	OriginDaemon string `json:"origin_daemon,omitempty"`
	GroupID      string `json:"group_id"`
	MemberType   string `json:"member_type"` // "agent", "role", "group"
	MemberValue  string `json:"member_value"`
	AddedBy      string `json:"added_by"`
}

// GroupMemberRemoveEvent represents a group.member.remove event.
type GroupMemberRemoveEvent struct {
	Type         string `json:"type"` // "group.member.remove"
	Timestamp    string `json:"timestamp"`
	EventID      string `json:"event_id"`
	Version      int    `json:"v"`
	OriginDaemon string `json:"origin_daemon,omitempty"`
	GroupID      string `json:"group_id"`
	MemberType   string `json:"member_type"`
	MemberValue  string `json:"member_value"`
	RemovedBy    string `json:"removed_by"`
}

// GroupUpdateEvent represents a group.update event.
type GroupUpdateEvent struct {
	Type         string            `json:"type"` // "group.update"
	Timestamp    string            `json:"timestamp"`
	EventID      string            `json:"event_id"`
	Version      int               `json:"v"`
	OriginDaemon string            `json:"origin_daemon,omitempty"`
	GroupID      string            `json:"group_id"`
	UpdatedBy    string            `json:"updated_by"`
	Fields       map[string]string `json:"fields"` // Changed fields
}

// GroupDeleteEvent represents a group.delete event.
type GroupDeleteEvent struct {
	Type         string `json:"type"` // "group.delete"
	Timestamp    string `json:"timestamp"`
	EventID      string `json:"event_id"`
	Version      int    `json:"v"`
	OriginDaemon string `json:"origin_daemon,omitempty"`
	GroupID      string `json:"group_id"`
	DeletedBy    string `json:"deleted_by"`
}

// CommitSummary represents a single commit.
type CommitSummary struct {
	SHA     string   `json:"sha"`
	Message string   `json:"message"`
	Files   []string `json:"files,omitempty"`
}

// FileChange represents detailed information about a changed file.
type FileChange struct {
	Path         string `json:"path"`
	LastModified string `json:"last_modified"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Status       string `json:"status"` // "modified", "added", "deleted", "renamed"
}
