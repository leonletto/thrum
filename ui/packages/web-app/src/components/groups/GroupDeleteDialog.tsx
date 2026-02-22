import { useState } from 'react';
import {
  useGroupDelete,
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
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';

export interface GroupDeleteDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  groupName: string;
}

export function GroupDeleteDialog({
  open,
  onOpenChange,
  groupName,
}: GroupDeleteDialogProps) {
  const [archiveFirst, setArchiveFirst] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const isEveryone = groupName === 'everyone';

  const { data: messageData } = useMessageList({
    scope: { type: 'group', value: groupName },
    page_size: 1,
  });
  const messageCount = messageData?.total ?? 0;

  const archiveMessages = useMessageArchive();
  const deleteGroup = useGroupDelete();

  const isPending = archiveMessages.isPending || deleteGroup.isPending;

  const handleConfirm = async () => {
    if (isEveryone) return;
    setError(null);

    try {
      if (archiveFirst) {
        await archiveMessages.mutateAsync({ archive_type: 'group', identifier: groupName });
      }
      await deleteGroup.mutateAsync({ name: groupName, delete_messages: true });
      onOpenChange(false);
      selectLiveFeed();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'An error occurred');
    }
  };

  const handleOpenChange = (next: boolean) => {
    if (!next) {
      setArchiveFirst(false);
      setError(null);
    }
    onOpenChange(next);
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="text-red-400">Delete Group</DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Are you sure you want to delete group{' '}
            <span className="font-mono font-semibold text-foreground">&quot;#{groupName}&quot;</span>?
          </p>

          <div className="space-y-1">
            <p className="text-sm font-medium">This will permanently remove:</p>
            <ul className="text-sm text-muted-foreground space-y-1 ml-4 list-none">
              <li className="before:content-['•'] before:mr-2">Group and all members</li>
              <li className="before:content-['•'] before:mr-2">
                All {messageCount} message{messageCount !== 1 ? 's' : ''}
              </li>
            </ul>
          </div>

          <div className="flex items-center gap-2">
            <Checkbox
              id="archive-checkbox"
              checked={archiveFirst}
              onCheckedChange={(checked) => setArchiveFirst(checked === true)}
              data-testid="archive-checkbox"
              disabled={isEveryone}
            />
            <Label htmlFor="archive-checkbox" className="cursor-pointer text-sm">
              Archive messages before deleting
            </Label>
          </div>

          {isEveryone && (
            <p className="text-sm text-amber-400" data-testid="everyone-warning">
              The <span className="font-mono font-semibold">#everyone</span> group cannot be deleted.
            </p>
          )}

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
            disabled={isEveryone || isPending}
            data-testid="confirm-delete-button"
            title={isEveryone ? 'The #everyone group cannot be deleted' : undefined}
          >
            {isPending ? 'Deleting...' : 'Delete Group'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
