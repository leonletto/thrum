import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Badge } from '@/components/ui/badge';
import type { TelegramStatusResponse } from '@thrum/shared-logic';

export interface TelegramConfig {
  allow_from?: number[];
}

interface TelegramAllowListProps {
  status: TelegramStatusResponse;
  onConfigure: (config: Partial<TelegramConfig>) => void;
}

export function TelegramAllowList({ status, onConfigure }: TelegramAllowListProps) {
  const [newUserId, setNewUserId] = useState('');
  const [inputError, setInputError] = useState<string | null>(null);

  const allowFrom: number[] = status.allow_from ?? [];

  function handleAddUserId() {
    const trimmed = newUserId.trim();
    if (!trimmed) return;

    const parsed = Number(trimmed);
    if (!Number.isInteger(parsed) || parsed <= 0) {
      setInputError('User ID must be a positive integer.');
      return;
    }
    if (allowFrom.includes(parsed)) {
      setInputError('User ID already in the list.');
      return;
    }

    setInputError(null);
    setNewUserId('');
    onConfigure({ allow_from: [...allowFrom, parsed] });
  }

  function handleRemoveUserId(id: number) {
    onConfigure({ allow_from: allowFrom.filter((existing) => existing !== id) });
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter') {
      e.preventDefault();
      handleAddUserId();
    }
  }

  return (
    <div className="space-y-4">
      {/* Section heading */}
      <div>
        <Label className="text-xs font-mono uppercase tracking-wider" style={{ color: 'var(--text-secondary)' }}>
          Allow List
        </Label>
        <p className="mt-1 text-xs font-mono" style={{ color: 'var(--muted-foreground)' }}>
          Only these Telegram user IDs can send messages to the bridge.
        </p>
      </div>

      {/* Existing user IDs as removable chips */}
      {allowFrom.length > 0 ? (
        <div className="flex flex-wrap gap-2" role="list" aria-label="Allowed user IDs">
          {allowFrom.map((id) => (
            <span key={id} role="listitem">
              <Badge
                variant="secondary"
                className="flex items-center gap-1.5 pr-1"
              >
                <span className="font-mono">{id}</span>
                <button
                  type="button"
                  onClick={() => handleRemoveUserId(id)}
                  className="ml-0.5 rounded-sm opacity-70 hover:opacity-100 focus:outline-none focus:ring-1 focus:ring-[var(--accent-color)]"
                  aria-label={`Remove user ID ${id}`}
                >
                  <svg
                    xmlns="http://www.w3.org/2000/svg"
                    viewBox="0 0 16 16"
                    fill="currentColor"
                    className="w-3 h-3"
                    aria-hidden="true"
                  >
                    <path d="M4.293 4.293a1 1 0 011.414 0L8 6.586l2.293-2.293a1 1 0 011.414 1.414L9.414 8l2.293 2.293a1 1 0 01-1.414 1.414L8 9.414l-2.293 2.293a1 1 0 01-1.414-1.414L6.586 8 4.293 5.707a1 1 0 010-1.414z" />
                  </svg>
                </button>
              </Badge>
            </span>
          ))}
        </div>
      ) : (
        <p className="text-xs font-mono italic" style={{ color: 'var(--muted-foreground)' }}>
          No user IDs in the allow list.
        </p>
      )}

      {/* Add new user ID */}
      <div className="space-y-1">
          <div className="flex gap-2">
            <Input
              type="text"
              inputMode="numeric"
              pattern="[0-9]*"
              value={newUserId}
              onChange={(e) => {
                setNewUserId(e.target.value);
                setInputError(null);
              }}
              onKeyDown={handleKeyDown}
              placeholder="Telegram user ID"
              aria-label="New Telegram user ID"
              className="font-mono text-sm"
            />
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={handleAddUserId}
              disabled={!newUserId.trim()}
            >
              Add
            </Button>
          </div>
          {inputError && (
            <p className="text-xs font-mono" style={{ color: 'var(--destructive)' }} role="alert">
              {inputError}
            </p>
          )}
        </div>

    </div>
  );
}
