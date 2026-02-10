import { useState, type FormEvent } from 'react';
import { Loader2, Plus, X } from 'lucide-react';
import { useSendMessage, useCurrentUser, type MessageScope } from '@thrum/shared-logic';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { MentionAutocomplete } from './MentionAutocomplete';
import { ScopeBadge } from '@/components/ui/ScopeBadge';

interface InlineReplyProps {
  threadId: string;
  sendingAs: string;
  isImpersonating: boolean;
}

export function InlineReply({
  threadId,
  sendingAs,
  isImpersonating,
}: InlineReplyProps) {
  const [content, setContent] = useState('');
  const [mentions, setMentions] = useState<string[]>([]);
  const [disclosed, setDisclosed] = useState(true);
  const [scopes, setScopes] = useState<MessageScope[]>([]);
  const [showScopeInput, setShowScopeInput] = useState(false);
  const [scopeType, setScopeType] = useState('');
  const [scopeValue, setScopeValue] = useState('');
  const currentUser = useCurrentUser();
  const { mutate: sendMessage, isPending } = useSendMessage();

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!content.trim()) return;

    sendMessage(
      {
        content,
        thread_id: threadId,
        body: { format: 'markdown', content },
        ...(mentions.length > 0 && { mentions }),
        ...(scopes.length > 0 && { scopes }),
        ...(isImpersonating && {
          acting_as: sendingAs,
          disclosed,
        }),
      },
      {
        onSuccess: () => {
          setContent('');
          setMentions([]);
          setScopes([]);
          setShowScopeInput(false);
        },
      }
    );
  };

  const handleContentChange = (newContent: string, newMentions: string[]) => {
    setContent(newContent);
    setMentions(newMentions);
  };

  const handleAddScope = () => {
    if (scopeType.trim() && scopeValue.trim()) {
      setScopes([...scopes, { type: scopeType.trim(), value: scopeValue.trim() }]);
      setScopeType('');
      setScopeValue('');
      setShowScopeInput(false);
    }
  };

  const handleRemoveScope = (index: number) => {
    setScopes(scopes.filter((_, i) => i !== index));
  };

  return (
    <form onSubmit={handleSubmit} className="border-t pt-3 space-y-2">
      <MentionAutocomplete
        value={content}
        onChange={handleContentChange}
        placeholder="Write a reply... (Use @ to mention agents)"
        className="min-h-[60px] resize-none"
        disabled={isPending}
      />

      {showScopeInput && (
        <div className="flex gap-2 items-center">
          <Input
            placeholder="Type (e.g., project)"
            value={scopeType}
            onChange={(e) => setScopeType(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && (e.preventDefault(), handleAddScope())}
            className="flex-1"
            size={1}
          />
          <Input
            placeholder="Value (e.g., thrum)"
            value={scopeValue}
            onChange={(e) => setScopeValue(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && (e.preventDefault(), handleAddScope())}
            className="flex-1"
            size={1}
          />
          <Button
            type="button"
            size="sm"
            onClick={handleAddScope}
            disabled={!scopeType.trim() || !scopeValue.trim()}
          >
            Add
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            onClick={() => {
              setShowScopeInput(false);
              setScopeType('');
              setScopeValue('');
            }}
          >
            <X className="h-4 w-4" />
          </Button>
        </div>
      )}

      {scopes.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {scopes.map((scope, index) => (
            <ScopeBadge
              key={`${scope.type}-${scope.value}-${index}`}
              scope={scope}
              onRemove={() => handleRemoveScope(index)}
            />
          ))}
        </div>
      )}

      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          {!showScopeInput && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setShowScopeInput(true)}
            >
              <Plus className="h-4 w-4 mr-1" />
              Add Scope
            </Button>
          )}

          {isImpersonating && (
            <label className="flex items-center gap-2 text-sm cursor-pointer">
              <Checkbox
                checked={disclosed}
                onCheckedChange={(checked) => setDisclosed(checked === true)}
              />
              <span>Show "via {currentUser?.username}"</span>
            </label>
          )}
        </div>

        <Button type="submit" size="sm" disabled={isPending || !content.trim()}>
          {isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : 'Send'}
        </Button>
      </div>
    </form>
  );
}
