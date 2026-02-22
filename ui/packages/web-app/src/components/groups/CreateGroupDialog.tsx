import { useState } from 'react';
import { useGroupCreate } from '@thrum/shared-logic';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Button } from '@/components/ui/button';

export interface CreateGroupDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function CreateGroupDialog({ open, onOpenChange }: CreateGroupDialogProps) {
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [error, setError] = useState<string | null>(null);

  const createGroup = useGroupCreate();

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmedName = name.trim();
    if (!trimmedName) {
      setError('Name is required');
      return;
    }
    setError(null);
    createGroup.mutate(
      { name: trimmedName, description: description.trim() || undefined },
      {
        onSuccess: () => {
          setName('');
          setDescription('');
          onOpenChange(false);
        },
        onError: (err: Error) => {
          setError(err.message ?? 'Failed to create group');
        },
      }
    );
  };

  const handleOpenChange = (next: boolean) => {
    if (!next) {
      setName('');
      setDescription('');
      setError(null);
    }
    onOpenChange(next);
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Create Group</DialogTitle>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="group-name">Name</Label>
            <Input
              id="group-name"
              placeholder="e.g. backend"
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoFocus
              data-testid="group-name-input"
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="group-description">
              Description{' '}
              <span className="text-muted-foreground text-xs">(optional)</span>
            </Label>
            <Input
              id="group-description"
              placeholder="What is this group for?"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              data-testid="group-description-input"
            />
          </div>

          {error && (
            <p className="text-sm text-red-400" data-testid="create-group-error">
              {error}
            </p>
          )}

          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={() => handleOpenChange(false)}
              disabled={createGroup.isPending}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={createGroup.isPending}
              data-testid="create-group-submit"
            >
              {createGroup.isPending ? 'Creating…' : 'Create'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
