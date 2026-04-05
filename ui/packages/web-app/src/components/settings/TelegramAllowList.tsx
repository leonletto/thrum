import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Badge } from '@/components/ui/badge';
import type { TelegramStatusResponse } from '@thrum/shared-logic';

export interface TelegramConfig {
  allow_from?: number[];
  allow_all?: boolean;
}

interface TelegramAllowListProps {
  status: TelegramStatusResponse;
  onConfigure: (config: Partial<TelegramConfig>) => void;
}

export function TelegramAllowList({ status, onConfigure }: TelegramAllowListProps) {
  const [newUserId, setNewUserId] = useState('');
  const [inputError, setInputError] = useState<string | null>(null);

  const allowFrom: number[] = status.allow_from ?? [];
  const allowAll: boolean = status.allow_all ?? false;

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

  function handleToggleAllowAll() {
    onConfigure({ allow_all: !allowAll });
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
        !allowAll && (
          <p className="text-xs font-mono italic" style={{ color: 'var(--muted-foreground)' }}>
            No user IDs in the allow list.
          </p>
        )
      )}

      {/* Add new user ID */}
      {!allowAll && (
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
      )}

      {/* Allow-all toggle */}
      <div className="space-y-1">
        <div className="flex items-center justify-between">
          <Label
            htmlFor="tg-allow-all"
            className="text-xs font-mono cursor-pointer"
            style={{ color: 'var(--text-secondary)' }}
          >
            Allow all users
          </Label>
          <button
            id="tg-allow-all"
            type="button"
            role="switch"
            aria-checked={allowAll}
            onClick={handleToggleAllowAll}
            className="relative inline-flex h-5 w-9 flex-shrink-0 rounded-full border-2 border-transparent transition-colors focus:outline-none focus:ring-2 focus:ring-[var(--accent-color)]"
            style={{
              backgroundColor: allowAll ? 'var(--accent-color)' : 'var(--border)',
            }}
          >
            <span
              className="inline-block h-4 w-4 rounded-full bg-white shadow transform transition-transform"
              style={{ transform: allowAll ? 'translateX(1rem)' : 'translateX(0)' }}
              aria-hidden="true"
            />
          </button>
        </div>

        {allowAll && (
          <div
            className="flex items-start gap-2 px-3 py-2 rounded border text-xs font-mono"
            style={{
              borderColor: 'var(--destructive)',
              color: 'var(--destructive)',
              background: 'var(--accent-subtle-bg)',
            }}
            role="alert"
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              viewBox="0 0 20 20"
              fill="currentColor"
              className="w-4 h-4 flex-shrink-0 mt-0.5"
              aria-hidden="true"
            >
              <path
                fillRule="evenodd"
                d="M8.485 2.495c.673-1.167 2.357-1.167 3.03 0l6.28 10.875c.673 1.167-.17 2.625-1.516 2.625H3.72c-1.347 0-2.189-1.458-1.515-2.625L8.485 2.495zM10 5a.75.75 0 01.75.75v3.5a.75.75 0 01-1.5 0v-3.5A.75.75 0 0110 5zm0 9a1 1 0 100-2 1 1 0 000 2z"
                clipRule="evenodd"
              />
            </svg>
            <span>Warning: allows ANY Telegram user to send messages to this bridge.</span>
          </div>
        )}
      </div>
    </div>
  );
}
