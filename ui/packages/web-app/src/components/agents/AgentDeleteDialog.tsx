import { useState } from 'react';
import {
  useAgentDelete,
  useMessageList,
  useMessageArchive,
  selectLiveFeed,
} from '@thrum/shared-logic';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';

export interface AgentDeleteDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  agentName: string;
  agentId: string;
}

export function AgentDeleteDialog({
  open,
  onOpenChange,
  agentName,
  agentId,
}: AgentDeleteDialogProps) {
  const [confirmText, setConfirmText] = useState('');
  const [archiveFirst, setArchiveFirst] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const { data: messageData } = useMessageList({ for_agent: agentId, page_size: 1 });
  const messageCount = messageData?.total ?? 0;

  const archiveMessages = useMessageArchive();
  const deleteAgent = useAgentDelete();

  const isConfirmed = confirmText === agentName;
  const isPending = archiveMessages.isPending || deleteAgent.isPending;

  const handleConfirm = async () => {
    if (!isConfirmed) return;
    setError(null);

    try {
      if (archiveFirst) {
        await archiveMessages.mutateAsync({ archive_type: 'agent', identifier: agentId });
      }
      await deleteAgent.mutateAsync(agentName);
      onOpenChange(false);
      selectLiveFeed();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'An error occurred');
    }
  };

  const handleOpenChange = (next: boolean) => {
    if (!next) {
      setConfirmText('');
      setArchiveFirst(false);
      setError(null);
    }
    onOpenChange(next);
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="text-red-400">Delete Agent</DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Are you sure you want to delete agent{' '}
            <span className="font-mono font-semibold text-foreground">&quot;{agentName}&quot;</span>?
          </p>

          <div className="space-y-1">
            <p className="text-sm font-medium">This will permanently remove:</p>
            <ul className="text-sm text-muted-foreground space-y-1 ml-4 list-none">
              <li className="before:content-['•'] before:mr-2">Identity and context files</li>
              <li className="before:content-['•'] before:mr-2">
                All {messageCount} message{messageCount !== 1 ? 's' : ''}
              </li>
              <li className="before:content-['•'] before:mr-2">JSONL message shard</li>
            </ul>
          </div>

          <div className="flex items-center gap-2">
            <Checkbox
              id="archive-checkbox"
              checked={archiveFirst}
              onCheckedChange={(checked) => setArchiveFirst(checked === true)}
              data-testid="archive-checkbox"
            />
            <Label htmlFor="archive-checkbox" className="cursor-pointer text-sm">
              Archive messages before deleting
            </Label>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="confirm-input" className="text-sm">
              Type <span className="font-mono font-semibold">&quot;{agentName}&quot;</span> to confirm:
            </Label>
            <Input
              id="confirm-input"
              value={confirmText}
              onChange={(e) => setConfirmText(e.target.value)}
              placeholder={agentName}
              data-testid="confirm-input"
              autoComplete="off"
            />
          </div>

          {error && (
            <p className="text-sm text-red-400" data-testid="delete-error">
              {error}
            </p>
          )}
        </div>

        <DialogFooter>
          <Button
            variant="outline"
            type="button"
            onClick={() => handleOpenChange(false)}
            disabled={isPending}
          >
            Cancel
          </Button>
          <Button
            variant="destructive"
            type="button"
            onClick={handleConfirm}
            disabled={!isConfirmed || isPending}
            data-testid="confirm-delete-button"
          >
            {isPending ? 'Deleting...' : 'Delete Agent'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
