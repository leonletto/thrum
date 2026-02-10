import { useState, type FormEvent } from 'react';
import { Loader2, Plus, X, ChevronDown } from 'lucide-react';
import { useCreateThread, useCurrentUser, type MessageScope } from '@thrum/shared-logic';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Checkbox } from '@/components/ui/checkbox';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { MentionAutocomplete } from './MentionAutocomplete';
import { ScopeBadge } from '@/components/ui/ScopeBadge';

interface ComposeModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sendingAs: string;
  isImpersonating: boolean;
}

export function ComposeModal({
  open,
  onOpenChange,
  isImpersonating,
}: ComposeModalProps) {
  const [recipient, setRecipient] = useState('');
  const [title, setTitle] = useState('');
  const [content, setContent] = useState('');
  const [, setMentions] = useState<string[]>([]);
  const [disclosed, setDisclosed] = useState(true);
  const [scopes, setScopes] = useState<MessageScope[]>([]);
  const [showScopeInput, setShowScopeInput] = useState(false);
  const [scopeType, setScopeType] = useState('');
  const [scopeValue, setScopeValue] = useState('');
  const [priority, setPriority] = useState<'normal' | 'high' | 'low'>('normal');
  const [showAdvancedOptions, setShowAdvancedOptions] = useState(false);
  const currentUser = useCurrentUser();
  const { mutate: createThread, isPending } = useCreateThread();

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();

    // TODO: Update when backend supports recipient, message, acting_as, disclosed
    // Currently useCreateThread only accepts { title }
    createThread(
      {
        title,
      },
      {
        onSuccess: () => {
          onOpenChange(false);
          // Reset form
          setRecipient('');
          setTitle('');
          setContent('');
          setMentions([]);
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
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[500px]">
        <DialogHeader>
          <DialogTitle>New Message</DialogTitle>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="recipient">To</Label>
            <Input
              id="recipient"
              value={recipient}
              onChange={(e) => setRecipient(e.target.value)}
              placeholder="agent:name or user:name"
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="title">Subject</Label>
            <Input
              id="title"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Thread title"
              required
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="content">Message</Label>
            <MentionAutocomplete
              id="content"
              value={content}
              onChange={handleContentChange}
              placeholder="Write your message... (Use @ to mention agents)"
              className="min-h-[120px]"
            />
          </div>

          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label>Scopes (optional)</Label>
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
            </div>

            {showScopeInput && (
              <div className="flex gap-2">
                <Input
                  placeholder="Type (e.g., project)"
                  value={scopeType}
                  onChange={(e) => setScopeType(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && (e.preventDefault(), handleAddScope())}
                  className="flex-1"
                />
                <Input
                  placeholder="Value (e.g., thrum)"
                  value={scopeValue}
                  onChange={(e) => setScopeValue(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && (e.preventDefault(), handleAddScope())}
                  className="flex-1"
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
          </div>

          <div className="space-y-2">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setShowAdvancedOptions(!showAdvancedOptions)}
              className="w-full justify-between"
            >
              <span>Advanced Options</span>
              <ChevronDown className={`h-4 w-4 transition-transform ${showAdvancedOptions ? 'rotate-180' : ''}`} />
            </Button>

            {showAdvancedOptions && (
              <div className="space-y-3 pl-2 border-l-2 border-muted">
                <div className="space-y-2">
                  <Label htmlFor="priority">Priority</Label>
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button
                        id="priority"
                        variant="outline"
                        className="w-full justify-between"
                        type="button"
                      >
                        <span className="capitalize">{priority}</span>
                        <ChevronDown className="h-4 w-4 opacity-50" />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent className="w-full">
                      <DropdownMenuRadioGroup value={priority} onValueChange={(value) => setPriority(value as 'normal' | 'high' | 'low')}>
                        <DropdownMenuRadioItem value="normal">Normal</DropdownMenuRadioItem>
                        <DropdownMenuRadioItem value="high">High</DropdownMenuRadioItem>
                        <DropdownMenuRadioItem value="low">Low</DropdownMenuRadioItem>
                      </DropdownMenuRadioGroup>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
              </div>
            )}
          </div>

          {isImpersonating && (
            <label className="flex items-center gap-2 text-sm cursor-pointer">
              <Checkbox
                checked={disclosed}
                onCheckedChange={(checked) => setDisclosed(checked === true)}
              />
              <span>Show "via {currentUser?.username}"</span>
            </label>
          )}

          <DialogFooter>
            <Button
              variant="outline"
              type="button"
              onClick={() => onOpenChange(false)}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={isPending || !title.trim()}>
              {isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : 'Send'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
