import { memo, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import { MoreVertical, Pencil, Trash2 } from 'lucide-react';
import type { Message } from '@thrum/shared-logic';
import { useAgentList, useCurrentUser, useEditMessage, useDeleteMessage } from '@thrum/shared-logic';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import { ScopeBadge } from '@/components/ui/ScopeBadge';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { cn } from '@/lib/utils';

interface MessageBubbleProps {
  message: Message;
  isOwn: boolean;
}

export const MessageBubble = memo(function MessageBubble({ message, isOwn }: MessageBubbleProps) {
  const { data: agentListData } = useAgentList();
  const currentUser = useCurrentUser();
  const editMessage = useEditMessage();
  const deleteMessage = useDeleteMessage();

  const [isEditing, setIsEditing] = useState(false);
  const [editContent, setEditContent] = useState(message.body.content || '');
  const [showDeleteDialog, setShowDeleteDialog] = useState(false);

  const showViaTag = message.disclosed && message.authored_by;
  const isDeleted = !!message.deleted_at;
  const isEdited = message.updated_at && message.updated_at !== message.created_at;

  // Check if current user owns this message (user_id matches agent_id for user messages)
  const isOwnMessage = currentUser && message.agent_id === currentUser.user_id;

  const formatRelativeTime = (timestamp: string) => {
    const date = new Date(timestamp);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    const diffHours = Math.floor(diffMs / 3600000);

    if (diffMins < 1) return 'just now';
    if (diffMins < 60) return `${diffMins}m ago`;
    if (diffHours < 24) return `${diffHours}h ago`;
    return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  };

  // Get display name for agent_id
  const getDisplayName = (agentId: string | undefined) => {
    if (!agentId) return 'Unknown';
    const agent = agentListData?.agents.find(a => a.agent_id === agentId);
    return agent?.display || agentId;
  };

  const displayName = getDisplayName(message.agent_id);

  const handleEditClick = () => {
    setIsEditing(true);
    setEditContent(message.body.content || '');
  };

  const handleSaveEdit = async () => {
    if (editContent.trim() === message.body.content?.trim()) {
      setIsEditing(false);
      return;
    }

    try {
      await editMessage.mutateAsync({
        message_id: message.message_id,
        content: editContent,
      });
      setIsEditing(false);
    } catch (error) {
      console.error('Failed to edit message:', error);
    }
  };

  const handleCancelEdit = () => {
    setIsEditing(false);
    setEditContent(message.body.content || '');
  };

  const handleDeleteClick = () => {
    setShowDeleteDialog(true);
  };

  const handleConfirmDelete = async () => {
    try {
      await deleteMessage.mutateAsync({
        message_id: message.message_id,
      });
      setShowDeleteDialog(false);
    } catch (error) {
      console.error('Failed to delete message:', error);
    }
  };

  return (
    <>
      <div
        className={cn(
          'group max-w-[80%] rounded-lg p-3 relative',
          isOwn
            ? 'ml-auto bg-primary text-primary-foreground'
            : 'bg-muted text-foreground'
        )}
      >
        <div className="flex items-center gap-2 text-xs opacity-70 mb-1">
          <span className="font-medium">{displayName}</span>
          {showViaTag && (
            <Badge variant="outline" className="text-[10px] h-4">
              via {message.authored_by}
            </Badge>
          )}
          <span className="ml-auto">{formatRelativeTime(message.created_at)}</span>
          {isEdited && !isDeleted && (
            <span className="italic opacity-50">(edited)</span>
          )}

          {/* Dropdown menu for own messages */}
          {isOwnMessage && !isDeleted && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-6 w-6 opacity-0 group-hover:opacity-100 transition-opacity"
                >
                  <MoreVertical className="h-4 w-4" />
                  <span className="sr-only">Message actions</span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onClick={handleEditClick}>
                  <Pencil className="mr-2 h-4 w-4" />
                  Edit
                </DropdownMenuItem>
                <DropdownMenuItem onClick={handleDeleteClick}>
                  <Trash2 className="mr-2 h-4 w-4" />
                  Delete
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          )}
        </div>

        {/* Message content or edit mode */}
        {isDeleted ? (
          <div className="italic opacity-50 text-sm">
            [message deleted]
          </div>
        ) : isEditing ? (
          <div className="space-y-2">
            <Textarea
              value={editContent}
              onChange={(e) => setEditContent(e.target.value)}
              className={cn(
                'min-h-[60px]',
                isOwn
                  ? 'bg-primary/50 text-primary-foreground'
                  : 'bg-muted'
              )}
              autoFocus
            />
            <div className="flex gap-2 justify-end">
              <Button
                size="sm"
                variant="ghost"
                onClick={handleCancelEdit}
                disabled={editMessage.isPending}
              >
                Cancel
              </Button>
              <Button
                size="sm"
                onClick={handleSaveEdit}
                disabled={editMessage.isPending || !editContent.trim()}
              >
                {editMessage.isPending ? 'Saving...' : 'Save'}
              </Button>
            </div>
          </div>
        ) : (
          <div className={cn('prose prose-sm max-w-none', isOwn && 'prose-invert')}>
            <ReactMarkdown
              components={{
                p: ({ children }) => {
                  // Process text nodes to highlight mentions
                  const processNode = (node: any): any => {
                    if (typeof node === 'string') {
                      const parts: any[] = [];
                      const mentionPattern = /@(\w+)/g;
                      let lastIndex = 0;
                      let match;

                      while ((match = mentionPattern.exec(node)) !== null) {
                        if (match.index > lastIndex) {
                          parts.push(node.slice(lastIndex, match.index));
                        }

                        const role = match[1] ?? '';
                        // Check if mention is in message.mentions array
                        const isValidMention = role && message.mentions?.includes(role);

                        if (isValidMention) {
                          // Check if current user has this role
                          const currentUserRoles = agentListData?.agents
                            .filter(a => a.agent_id === currentUser?.user_id)
                            .map(a => a.role) || [];
                          const isCurrentUser = currentUserRoles.includes(role);

                          const highlightColor = isCurrentUser
                            ? 'rgba(56, 189, 248, 0.3)'
                            : 'rgba(56, 189, 248, 0.15)';

                          parts.push(
                            <span
                              key={`mention-${match.index}`}
                              style={{
                                backgroundColor: highlightColor,
                                color: 'rgb(56, 189, 248)',
                                padding: '0 2px',
                                borderRadius: '2px',
                              }}
                            >
                              @{role}
                            </span>
                          );
                        } else {
                          parts.push(match[0]);
                        }

                        lastIndex = match.index + match[0].length;
                      }

                      if (lastIndex < node.length) {
                        parts.push(node.slice(lastIndex));
                      }

                      return parts.length > 0 ? parts : node;
                    }

                    if (Array.isArray(node)) {
                      return node.map(processNode);
                    }

                    return node;
                  };

                  return <p>{processNode(children)}</p>;
                },
              }}
            >
              {message.body.content}
            </ReactMarkdown>
          </div>
        )}

        {/* Scope badges - only show if message is not deleted */}
        {!isDeleted && message.scopes && message.scopes.length > 0 && (
          <div className="flex flex-wrap gap-1 mt-2">
            {message.scopes.map((scope, index) => (
              <ScopeBadge key={`${scope.type}-${scope.value}-${index}`} scope={scope} />
            ))}
          </div>
        )}
      </div>

      {/* Delete confirmation dialog */}
      <Dialog open={showDeleteDialog} onOpenChange={setShowDeleteDialog}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Message</DialogTitle>
            <DialogDescription>
              Are you sure you want to delete this message? This action cannot be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="ghost"
              onClick={() => setShowDeleteDialog(false)}
              disabled={deleteMessage.isPending}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={handleConfirmDelete}
              disabled={deleteMessage.isPending}
            >
              {deleteMessage.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}, (prevProps, nextProps) => {
  // Only re-render if message ID, updated_at, or deleted_at changes
  return (
    prevProps.message.message_id === nextProps.message.message_id &&
    prevProps.message.updated_at === nextProps.message.updated_at &&
    prevProps.message.deleted_at === nextProps.message.deleted_at &&
    prevProps.isOwn === nextProps.isOwn
  );
});
