import { useState } from 'react';
import { Settings, Users, X, Bot, Shield, Plus, Trash2 } from 'lucide-react';
import {
  useMessageListPaged,
  useGroupInfo,
  useCurrentUser,
  useAgentList,
  useGroupMemberAdd,
  useGroupMemberRemove,
} from '@thrum/shared-logic';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { MessageList } from '../inbox/MessageList';
import { ComposeBar } from '../inbox/ComposeBar';
import { GroupDeleteDialog } from './GroupDeleteDialog';

interface GroupChannelViewProps {
  groupName: string;
  /** Deep-link: scroll to and highlight this message ID when set. */
  selectedMessageId?: string | null;
  /** Called after the highlight animation clears the selection. */
  onClearSelectedMessage?: () => void;
}

export function GroupChannelView({ groupName, selectedMessageId, onClearSelectedMessage }: GroupChannelViewProps) {
  const [replyTo, setReplyTo] = useState<{
    messageId: string;
    senderName: string;
  } | undefined>(undefined);
  const [membersOpen, setMembersOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  // Add-member form state
  const [addType, setAddType] = useState<'agent' | 'role'>('agent');
  const [addAgentValue, setAddAgentValue] = useState('');
  const [addRoleValue, setAddRoleValue] = useState('');
  const [addError, setAddError] = useState<string | null>(null);

  const currentUser = useCurrentUser();
  const {
    messages,
    total: messagesTotal,
    isLoading: messagesLoading,
    hasMore: messagesHasMore,
    loadMore: messagesLoadMore,
    isLoadingMore: messagesLoadingMore,
  } = useMessageListPaged({
    scope: { type: 'group', value: groupName },
    page_size: 50,
    sort_order: 'desc',
  });
  const { data: groupInfo } = useGroupInfo(groupName);
  const { data: agentData } = useAgentList();

  const memberAdd = useGroupMemberAdd();
  const memberRemove = useGroupMemberRemove();

  const memberCount = groupInfo?.members?.length ?? 0;
  const isEveryone = groupName === 'everyone';

  const sendingAs = currentUser?.username ?? 'unknown';

  const agents = agentData?.agents ?? [];

  const handleReply = (messageId: string, senderName: string) => {
    setReplyTo({ messageId, senderName });
  };

  const handleClearReply = () => {
    setReplyTo(undefined);
  };

  const handleAddMember = (e: React.FormEvent) => {
    e.preventDefault();
    const value = addType === 'agent' ? addAgentValue : addRoleValue.trim();
    if (!value) {
      setAddError(addType === 'agent' ? 'Select an agent' : 'Enter a role name');
      return;
    }
    setAddError(null);
    memberAdd.mutate(
      { group_name: groupName, member_type: addType, member_value: value },
      {
        onSuccess: () => {
          setAddAgentValue('');
          setAddRoleValue('');
        },
        onError: (err: Error) => {
          setAddError(err.message ?? 'Failed to add member');
        },
      }
    );
  };

  const handleRemoveMember = (memberType: 'agent' | 'role', memberValue: string) => {
    memberRemove.mutate({
      group_name: groupName,
      member_type: memberType,
      member_value: memberValue,
    });
  };

  return (
    <div className="h-full flex flex-col relative font-mono">
      {/* Header */}
      <div
        className="flex items-center gap-3 px-4 py-2 border-b border-[var(--accent-border)] bg-[var(--panel-bg-start)] shrink-0"
        data-testid="group-channel-header"
      >
        <div className="flex items-center gap-2 flex-1 min-w-0">
          <span className="text-[var(--text-secondary)] font-semibold text-sm truncate">
            #{groupName}
          </span>
          <Badge
            variant="secondary"
            className="shrink-0 text-xs bg-[var(--accent-subtle-bg-hover)] border border-[var(--accent-border)] text-[var(--accent-color)]"
            data-testid="member-count-badge"
          >
            {memberCount} {memberCount === 1 ? 'member' : 'members'}
          </Badge>
        </div>

        <div className="flex items-center gap-1 shrink-0">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setMembersOpen(true)}
            className="h-7 px-2 text-xs text-[var(--accent-color)] hover:text-[var(--accent-color)] hover:bg-[var(--accent-subtle-bg)]"
            aria-label="View members"
          >
            <Users className="h-3.5 w-3.5 mr-1" />
            Members
          </Button>

          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setSettingsOpen(true)}
            className="h-7 px-2 text-[var(--accent-color)] hover:text-[var(--accent-color)] hover:bg-[var(--accent-subtle-bg)]"
            aria-label="Group settings"
          >
            <Settings className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>

      {/* Message area */}
      <div className="flex-1 min-h-0 flex flex-col overflow-hidden">
        <MessageList
          messages={messages}
          isLoading={messagesLoading}
          currentUserId={currentUser?.user_id}
          onReply={handleReply}
          totalCount={messagesTotal}
          hasMore={messagesHasMore}
          onLoadMore={messagesLoadMore}
          isLoadingMore={messagesLoadingMore}
          selectedMessageId={selectedMessageId}
          onClearSelectedMessage={onClearSelectedMessage}
        />
      </div>

      {/* Compose bar */}
      <div className="shrink-0">
        <ComposeBar
          sendingAs={sendingAs}
          isImpersonating={false}
          groupScope={groupName}
          replyTo={replyTo}
          onClearReply={handleClearReply}
        />
      </div>

      {/* Members slide-out panel */}
      {membersOpen && (
        <div
          className="absolute inset-y-0 right-0 w-72 bg-[var(--panel-bg-start)] border-l border-[var(--accent-border)] flex flex-col z-20 shadow-xl"
          data-testid="members-panel"
          role="dialog"
          aria-label="Group members"
        >
          <div className="flex items-center justify-between px-4 py-3 border-b border-[var(--accent-border)] shrink-0">
            <div className="flex items-center gap-2">
              <Users className="h-4 w-4 text-[var(--accent-color)]" />
              <span className="text-sm font-semibold text-[var(--text-secondary)]">
                Members
              </span>
              <Badge
                variant="secondary"
                className="text-xs bg-[var(--accent-subtle-bg-hover)] border border-[var(--accent-border)] text-[var(--accent-color)]"
              >
                {memberCount}
              </Badge>
            </div>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setMembersOpen(false)}
              className="h-6 w-6 p-0 text-[var(--text-muted)] hover:text-[var(--accent-color)] hover:bg-[var(--accent-subtle-bg)]"
              aria-label="Close members panel"
            >
              <X className="h-3.5 w-3.5" />
            </Button>
          </div>

          <div className="flex-1 overflow-y-auto">
            {!groupInfo ? (
              <div className="px-4 py-3 text-xs text-[var(--text-faint)] italic">
                Loading members…
              </div>
            ) : groupInfo.members.length === 0 ? (
              <div className="px-4 py-3 text-xs text-[var(--text-faint)] italic">
                No members
              </div>
            ) : (
              <ul className="divide-y divide-[var(--accent-border)]">
                {groupInfo.members.map((member, idx) => (
                  <li
                    key={`${member.member_type}:${member.member_value}:${idx}`}
                    className="flex items-start gap-2 px-4 py-2.5"
                    data-testid="member-item"
                  >
                    {member.member_type === 'agent' ? (
                      <Bot className="h-3.5 w-3.5 text-[var(--accent-color)] shrink-0 mt-0.5" />
                    ) : (
                      <Shield className="h-3.5 w-3.5 text-purple-400 shrink-0 mt-0.5" />
                    )}
                    <div className="flex flex-col min-w-0 flex-1">
                      <span className="text-xs text-[var(--text-secondary)] truncate">
                        {member.member_value}
                      </span>
                      <span className="text-[10px] text-[var(--text-faint)] capitalize">
                        {member.member_type}
                      </span>
                      <span className="text-[10px] text-[var(--text-faint)]">
                        Added:{' '}
                        {new Date(member.added_at).toLocaleDateString(
                          undefined,
                          { month: 'short', day: 'numeric', year: 'numeric' }
                        )}
                      </span>
                    </div>
                    {!isEveryone && (
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        onClick={() =>
                          handleRemoveMember(member.member_type, member.member_value)
                        }
                        className="h-5 w-5 p-0 text-[var(--text-faint)] hover:text-red-400 hover:bg-red-900/20 shrink-0"
                        aria-label={`Remove ${member.member_value}`}
                        data-testid="remove-member-button"
                        disabled={memberRemove.isPending}
                      >
                        <X className="h-3 w-3" />
                      </Button>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </div>

          {/* Add member section */}
          {!isEveryone && (
            <div
              className="shrink-0 border-t border-[var(--accent-border)] px-4 py-3 space-y-2"
              data-testid="add-member-section"
            >
              <div className="text-[10px] text-[var(--text-faint)] uppercase tracking-wider font-semibold">
                Add member
              </div>
              <form onSubmit={handleAddMember} className="space-y-2">
                {/* Type selector */}
                <div className="flex gap-1">
                  <button
                    type="button"
                    onClick={() => {
                      setAddType('agent');
                      setAddError(null);
                    }}
                    className={`flex-1 text-[10px] px-2 py-1 rounded border transition-colors ${
                      addType === 'agent'
                        ? 'bg-[var(--accent-subtle-bg-hover)] border-[var(--accent-border-hover)] text-[var(--text-secondary)]'
                        : 'bg-transparent border-[var(--accent-border)] text-[var(--text-faint)] hover:text-[var(--accent-color)]'
                    }`}
                    data-testid="add-type-agent"
                  >
                    Agent
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      setAddType('role');
                      setAddError(null);
                    }}
                    className={`flex-1 text-[10px] px-2 py-1 rounded border transition-colors ${
                      addType === 'role'
                        ? 'bg-purple-900/50 border-purple-500/50 text-purple-300'
                        : 'bg-transparent border-[var(--accent-border)] text-[var(--text-faint)] hover:text-[var(--accent-color)]'
                    }`}
                    data-testid="add-type-role"
                  >
                    Role
                  </button>
                </div>

                {/* Value input */}
                {addType === 'agent' ? (
                  <div className="space-y-1">
                    <Label htmlFor="add-agent-select" className="sr-only">
                      Select agent
                    </Label>
                    <select
                      id="add-agent-select"
                      value={addAgentValue}
                      onChange={(e) => setAddAgentValue(e.target.value)}
                      className="w-full text-xs bg-[var(--panel-bg-start)] border border-[var(--accent-border)] rounded px-2 py-1 text-[var(--text-secondary)] focus:outline-none focus:border-[var(--accent-border-hover)]"
                      data-testid="add-agent-select"
                    >
                      <option value="">Select agent…</option>
                      {agents.map((agent) => (
                        <option key={agent.agent_id} value={agent.agent_id}>
                          {agent.display ?? agent.agent_id}
                        </option>
                      ))}
                    </select>
                  </div>
                ) : (
                  <div className="space-y-1">
                    <Label htmlFor="add-role-input" className="sr-only">
                      Role name
                    </Label>
                    <Input
                      id="add-role-input"
                      placeholder="Role name…"
                      value={addRoleValue}
                      onChange={(e) => setAddRoleValue(e.target.value)}
                      className="h-7 text-xs bg-[var(--panel-bg-start)] border-[var(--accent-border)] text-[var(--text-secondary)] placeholder:text-[var(--text-faint)]"
                      data-testid="add-role-input"
                    />
                  </div>
                )}

                {addError && (
                  <p className="text-[10px] text-red-400" data-testid="add-member-error">
                    {addError}
                  </p>
                )}

                <Button
                  type="submit"
                  size="sm"
                  className="w-full h-7 text-xs bg-[var(--accent-subtle-bg-hover)] hover:bg-[var(--accent-subtle-bg-hover)] border border-[var(--accent-border)] text-[var(--text-secondary)]"
                  disabled={memberAdd.isPending}
                  data-testid="add-member-submit"
                >
                  <Plus className="h-3 w-3 mr-1" />
                  {memberAdd.isPending ? 'Adding…' : 'Add'}
                </Button>
              </form>
            </div>
          )}
        </div>
      )}

      {/* Settings panel placeholder */}
      {settingsOpen && (
        <div
          className="absolute inset-y-0 right-0 w-72 bg-[var(--panel-bg-start)] border-l border-[var(--accent-border)] flex flex-col z-20 shadow-xl"
          data-testid="settings-panel"
          role="dialog"
          aria-label="Group settings"
        >
          <div className="flex items-center justify-between px-4 py-3 border-b border-[var(--accent-border)] shrink-0">
            <div className="flex items-center gap-2">
              <Settings className="h-4 w-4 text-[var(--accent-color)]" />
              <span className="text-sm font-semibold text-[var(--text-secondary)]">
                Settings
              </span>
            </div>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setSettingsOpen(false)}
              className="h-6 w-6 p-0 text-[var(--text-muted)] hover:text-[var(--accent-color)] hover:bg-[var(--accent-subtle-bg)]"
              aria-label="Close settings panel"
            >
              <X className="h-3.5 w-3.5" />
            </Button>
          </div>

          <div className="flex-1 overflow-y-auto px-4 py-3 space-y-3">
            <div>
              <div className="text-[10px] text-[var(--text-faint)] uppercase tracking-wider mb-1">
                Group
              </div>
              <div className="text-sm text-[var(--text-secondary)] font-mono">
                #{groupName}
              </div>
            </div>

            {groupInfo?.description && (
              <div>
                <div className="text-[10px] text-[var(--text-faint)] uppercase tracking-wider mb-1">
                  Description
                </div>
                <div className="text-xs text-[var(--accent-color)]">
                  {groupInfo.description}
                </div>
              </div>
            )}

            {groupInfo?.created_at && (
              <div>
                <div className="text-[10px] text-[var(--text-faint)] uppercase tracking-wider mb-1">
                  Created
                </div>
                <div className="text-xs text-[var(--accent-color)]">
                  {new Date(groupInfo.created_at).toLocaleDateString(
                    undefined,
                    { month: 'long', day: 'numeric', year: 'numeric' }
                  )}
                </div>
              </div>
            )}

            {groupInfo?.created_by && (
              <div>
                <div className="text-[10px] text-[var(--text-faint)] uppercase tracking-wider mb-1">
                  Created by
                </div>
                <div className="text-xs text-[var(--accent-color)]">
                  {groupInfo.created_by}
                </div>
              </div>
            )}

            {!isEveryone && (
              <div className="pt-2 border-t border-[var(--accent-border)]">
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => setDeleteOpen(true)}
                  className="w-full h-7 text-xs text-red-500 hover:text-red-400 hover:bg-red-900/20 justify-start"
                  data-testid="delete-group-button"
                >
                  <Trash2 className="h-3 w-3 mr-1.5" />
                  Delete Group
                </Button>
              </div>
            )}
          </div>
        </div>
      )}

      <GroupDeleteDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        groupName={groupName}
      />
    </div>
  );
}
