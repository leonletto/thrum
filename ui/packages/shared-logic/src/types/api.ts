import { z } from 'zod';

/**
 * Agent types
 */
export const AgentSchema = z.object({
  agent_id: z.string(),
  kind: z.enum(['agent', 'user']),
  role: z.string(),
  module: z.string(),
  display: z.string().optional(),
  registered_at: z.string(),
  last_seen_at: z.string().optional(),
});

export type Agent = z.infer<typeof AgentSchema>;

export const AgentListResponseSchema = z.object({
  agents: z.array(AgentSchema),
});

export type AgentListResponse = z.infer<typeof AgentListResponseSchema>;

/**
 * User types
 */
export const UserRegisterRequestSchema = z.object({
  username: z.string(),
  display: z.string().optional(),
});

export type UserRegisterRequest = z.infer<typeof UserRegisterRequestSchema>;

export const UserRegisterResponseSchema = z.object({
  user_id: z.string(),
  username: z.string(),
  display_name: z.string().optional(),
  token: z.string(),
  status: z.string(), // "registered" or "existing"
});

export type UserRegisterResponse = z.infer<typeof UserRegisterResponseSchema>;

export const UserIdentifyResponseSchema = z.object({
  username: z.string(),
  email: z.string(),
  display: z.string(),
});

export type UserIdentifyResponse = z.infer<typeof UserIdentifyResponseSchema>;

/**
 * Message types
 */
export const MessageScopeSchema = z.object({
  type: z.string(),
  value: z.string(),
});

export type MessageScope = z.infer<typeof MessageScopeSchema>;

export const MessageRefSchema = z.object({
  type: z.string(),
  value: z.string(),
});

export type MessageRef = z.infer<typeof MessageRefSchema>;

export const MessageBodySchema = z.object({
  format: z.string(),
  content: z.string().optional(),
  structured: z.string().optional(),
});

export type MessageBody = z.infer<typeof MessageBodySchema>;

export const MessageSchema = z.object({
  message_id: z.string(),
  thread_id: z.string().optional(),
  agent_id: z.string().optional(),
  created_at: z.string(),
  updated_at: z.string().optional(),
  deleted_at: z.string().optional(),
  body: MessageBodySchema,
  is_read: z.boolean().optional(),
  reply_to: z.string().optional(),
  // Optional in list responses, guaranteed in detail responses
  session_id: z.string().optional(),
  scopes: z.array(MessageScopeSchema).optional(),
  refs: z.array(MessageRefSchema).optional(),
  authored_by: z.string().optional(),
  disclosed: z.boolean().optional(),
  mentions: z.array(z.string()).optional(),
});

export type Message = z.infer<typeof MessageSchema>;

export const MessageDetailSchema = MessageSchema.extend({
  session_id: z.string(),
  scopes: z.array(MessageScopeSchema),
  refs: z.array(MessageRefSchema),
});

export type MessageDetail = z.infer<typeof MessageDetailSchema>;

export const SendMessageRequestSchema = z.object({
  content: z.string(),
  thread_id: z.string().optional(),
  scopes: z.array(MessageScopeSchema).optional(),
  refs: z.array(MessageRefSchema).optional(),
  body: MessageBodySchema.optional(),
  acting_as: z.string().optional(),
  disclosed: z.boolean().optional(),
  mentions: z.array(z.string()).optional(),
  reply_to: z.string().optional(),
  caller_agent_id: z.string().optional(),
});

export type SendMessageRequest = z.infer<typeof SendMessageRequestSchema>;

export const SendMessageResponseSchema = z.object({
  message_id: z.string(),
  thread_id: z.string().optional(),
  created_at: z.string(),
  resolved_to: z.number().optional(),
  warnings: z.array(z.string()).optional(),
});

export type SendMessageResponse = z.infer<typeof SendMessageResponseSchema>;

export const MessageListRequestSchema = z.object({
  thread_id: z.string().optional(),
  author_id: z.string().optional(),
  for_agent: z.string().optional(),
  unread_for_agent: z.string().optional(),
  mention: z.string().optional(),
  scope: MessageScopeSchema.optional(),
  ref: MessageRefSchema.optional(),
  page_size: z.number().optional(),
  page: z.number().optional(),
  sort_by: z.enum(['created_at', 'updated_at']).optional(),
  sort_order: z.enum(['asc', 'desc']).optional(),
});

export type MessageListRequest = z.infer<typeof MessageListRequestSchema>;

export const MessageListResponseSchema = z.object({
  messages: z.array(MessageSchema),
  page: z.number(),
  page_size: z.number(),
  total: z.number(),
  total_pages: z.number(),
});

export type MessageListResponse = z.infer<typeof MessageListResponseSchema>;

export const MarkAsReadRequestSchema = z.object({
  message_ids: z.array(z.string()),
});

export type MarkAsReadRequest = z.infer<typeof MarkAsReadRequestSchema>;

export const MarkAsReadResponseSchema = z.object({
  marked_count: z.number(),
  updated_at: z.string(),
});

export type MarkAsReadResponse = z.infer<typeof MarkAsReadResponseSchema>;

/**
 * Subscription types
 */
export const SubscriptionSchema = z.object({
  subscription_id: z.string(),
  session_id: z.string(),
  filter_type: z.enum(['scope', 'mention', 'all']),
  scope: MessageScopeSchema.optional(),
  mention: z.string().optional(),
  created_at: z.string(),
});

export type Subscription = z.infer<typeof SubscriptionSchema>;

export const SubscriptionListResponseSchema = z.object({
  subscriptions: z.array(SubscriptionSchema),
});

export type SubscriptionListResponse = z.infer<typeof SubscriptionListResponseSchema>;

/**
 * Health check types
 */
export const HealthResponseSchema = z.object({
  status: z.string(),
  uptime_ms: z.number(),
  version: z.string(),
  repo_id: z.string(),
  sync_state: z.string(),
});

export type HealthResponse = z.infer<typeof HealthResponseSchema>;

/**
 * Agent Context types
 */
export const AgentContextSchema = z.object({
  session_id: z.string(),
  agent_id: z.string(),
  branch: z.string(),
  worktree_path: z.string(),
  unmerged_commits: z.array(z.object({
    hash: z.string(),
    subject: z.string(),
  })),
  uncommitted_files: z.array(z.string()),
  changed_files: z.array(z.string()),
  git_updated_at: z.string(),
  current_task: z.string(),
  task_updated_at: z.string(),
  intent: z.string(),
  intent_updated_at: z.string(),
});

export type AgentContext = z.infer<typeof AgentContextSchema>;

export const AgentContextListResponseSchema = z.object({
  contexts: z.array(AgentContextSchema),
});

export type AgentContextListResponse = z.infer<typeof AgentContextListResponseSchema>;

/**
 * Group types
 */
export const GroupSchema = z.object({
  group_id: z.string(),
  name: z.string(),
  description: z.string().optional(),
  member_count: z.number(),
  created_at: z.string(),
});

export type Group = z.infer<typeof GroupSchema>;

export const GroupMemberSchema = z.object({
  member_type: z.enum(['agent', 'role']),
  member_value: z.string(),
  added_at: z.string(),
  added_by: z.string().optional(),
});

export type GroupMember = z.infer<typeof GroupMemberSchema>;

export const GroupInfoSchema = z.object({
  group_id: z.string(),
  name: z.string(),
  description: z.string().optional(),
  created_at: z.string(),
  created_by: z.string().optional(),
  members: z.array(GroupMemberSchema),
});

export type GroupInfo = z.infer<typeof GroupInfoSchema>;

export const GroupListResponseSchema = z.object({
  groups: z.array(GroupSchema),
});

export type GroupListResponse = z.infer<typeof GroupListResponseSchema>;

/**
 * Session types
 */
export const SessionSchema = z.object({
  session_id: z.string(),
  agent_id: z.string(),
  started_at: z.string(),
  ended_at: z.string().optional(),
  active: z.boolean(),
});

export type Session = z.infer<typeof SessionSchema>;

export const SessionListResponseSchema = z.object({
  sessions: z.array(SessionSchema),
});

export type SessionListResponse = z.infer<typeof SessionListResponseSchema>;
