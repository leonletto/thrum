import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Badge } from '@/components/ui/badge';
import type { TelegramStatusResponse, TelegramGroup, RemoteAgent } from '@thrum/shared-logic';

interface TelegramGroupsProps {
  status: TelegramStatusResponse;
  onConfigure: (config: { groups: TelegramGroup[] }) => void;
}

function XIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 16 16"
      fill="currentColor"
      className="w-3 h-3"
      aria-hidden="true"
    >
      <path d="M4.293 4.293a1 1 0 011.414 0L8 6.586l2.293-2.293a1 1 0 011.414 1.414L9.414 8l2.293 2.293a1 1 0 01-1.414 1.414L8 9.414l-2.293 2.293a1 1 0 01-1.414-1.414L6.586 8 4.293 5.707a1 1 0 010-1.414z" />
    </svg>
  );
}

interface GroupItemProps {
  group: TelegramGroup;
  onUpdate: (updated: TelegramGroup) => void;
  onRemove: () => void;
}

function GroupItem({ group, onUpdate, onRemove }: GroupItemProps) {
  const [expanded, setExpanded] = useState(false);
  const [newBotId, setNewBotId] = useState('');
  const [botError, setBotError] = useState<string | null>(null);
  const [newAgent, setNewAgent] = useState({ name: '', prefix: '', bot: '' });
  const [agentError, setAgentError] = useState<string | null>(null);

  const trustedBots = group.trusted_bots ?? [];
  const remoteAgents = group.remote_agents ?? [];

  function handleAddBot() {
    const trimmed = newBotId.trim();
    if (!trimmed) return;
    const parsed = Number(trimmed);
    if (!Number.isInteger(parsed) || parsed <= 0) {
      setBotError('Bot ID must be a positive integer.');
      return;
    }
    if (trustedBots.includes(parsed)) {
      setBotError('Bot ID already in the list.');
      return;
    }
    setBotError(null);
    setNewBotId('');
    onUpdate({ ...group, trusted_bots: [...trustedBots, parsed] });
  }

  function handleRemoveBot(id: number) {
    onUpdate({ ...group, trusted_bots: trustedBots.filter((b) => b !== id) });
  }

  function handleAddAgent() {
    const { name, prefix, bot } = newAgent;
    if (!name.trim() || !prefix.trim() || !bot.trim()) {
      setAgentError('All three fields (name, prefix, bot) are required.');
      return;
    }
    setAgentError(null);
    setNewAgent({ name: '', prefix: '', bot: '' });
    onUpdate({ ...group, remote_agents: [...remoteAgents, { name: name.trim(), prefix: prefix.trim(), bot: bot.trim() }] });
  }

  function handleRemoveAgent(idx: number) {
    onUpdate({ ...group, remote_agents: remoteAgents.filter((_, i) => i !== idx) });
  }

  return (
    <div
      className="rounded border"
      style={{ borderColor: 'var(--border)' }}
    >
      {/* Group header */}
      <div className="flex items-center justify-between px-3 py-2">
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="flex items-center gap-2 flex-1 text-left focus:outline-none"
          aria-expanded={expanded}
        >
          <span className="text-xs font-mono" style={{ color: 'var(--accent-color)', fontSize: '0.6rem' }}>
            {expanded ? '▼' : '▶'}
          </span>
          <span className="text-sm font-mono font-medium" style={{ color: 'var(--foreground)' }}>
            {group.name}
          </span>
          <span className="text-xs font-mono" style={{ color: 'var(--muted-foreground)' }}>
            #{group.chat_id}
          </span>
          <span className="text-xs font-mono ml-auto mr-2" style={{ color: 'var(--muted-foreground)' }}>
            {trustedBots.length} bot{trustedBots.length !== 1 ? 's' : ''} · {remoteAgents.length} agent{remoteAgents.length !== 1 ? 's' : ''}
          </span>
        </button>
        <button
          type="button"
          onClick={onRemove}
          className="opacity-60 hover:opacity-100 focus:outline-none"
          aria-label={`Remove group ${group.name}`}
        >
          <XIcon />
        </button>
      </div>

      {/* Expanded details */}
      {expanded && (
        <div className="px-3 pb-3 space-y-4 border-t" style={{ borderColor: 'var(--border)' }}>
          {/* Trusted bots */}
          <div className="space-y-2 pt-3">
            <Label className="text-xs font-mono uppercase tracking-wider" style={{ color: 'var(--text-secondary)' }}>
              Trusted Bot IDs
            </Label>
            {trustedBots.length > 0 ? (
              <div className="flex flex-wrap gap-2" role="list" aria-label="Trusted bot IDs">
                {trustedBots.map((id) => (
                  <span key={id} role="listitem">
                    <Badge variant="secondary" className="flex items-center gap-1.5 pr-1">
                      <span className="font-mono">{id}</span>
                      <button
                        type="button"
                        onClick={() => handleRemoveBot(id)}
                        className="ml-0.5 rounded-sm opacity-70 hover:opacity-100 focus:outline-none"
                        aria-label={`Remove bot ID ${id}`}
                      >
                        <XIcon />
                      </button>
                    </Badge>
                  </span>
                ))}
              </div>
            ) : (
              <p className="text-xs font-mono italic" style={{ color: 'var(--muted-foreground)' }}>
                No trusted bots.
              </p>
            )}
            <div className="space-y-1">
              <div className="flex gap-2">
                <Input
                  type="text"
                  inputMode="numeric"
                  pattern="[0-9]*"
                  value={newBotId}
                  onChange={(e) => { setNewBotId(e.target.value); setBotError(null); }}
                  onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); handleAddBot(); } }}
                  placeholder="Bot user ID"
                  aria-label="New trusted bot ID"
                  className="font-mono text-sm"
                />
                <Button type="button" variant="outline" size="sm" onClick={handleAddBot} disabled={!newBotId.trim()}>
                  Add
                </Button>
              </div>
              {botError && (
                <p className="text-xs font-mono" style={{ color: 'var(--destructive)' }} role="alert">
                  {botError}
                </p>
              )}
            </div>
          </div>

          {/* Remote agents */}
          <div className="space-y-2">
            <Label className="text-xs font-mono uppercase tracking-wider" style={{ color: 'var(--text-secondary)' }}>
              Remote Agents
            </Label>
            {remoteAgents.length > 0 ? (
              <div className="space-y-1" role="list" aria-label="Remote agents">
                {remoteAgents.map((agent: RemoteAgent, idx: number) => (
                  <div
                    key={idx}
                    role="listitem"
                    className="flex items-center justify-between px-2 py-1.5 rounded text-xs font-mono"
                    style={{ background: 'var(--accent-subtle-bg)', color: 'var(--foreground)' }}
                  >
                    <span>
                      <span style={{ color: 'var(--accent-color)' }}>{agent.name}</span>
                      <span style={{ color: 'var(--muted-foreground)' }}> · prefix: </span>
                      <span>{agent.prefix}</span>
                      <span style={{ color: 'var(--muted-foreground)' }}> · bot: </span>
                      <span>{agent.bot}</span>
                    </span>
                    <button
                      type="button"
                      onClick={() => handleRemoveAgent(idx)}
                      className="ml-2 opacity-60 hover:opacity-100 focus:outline-none"
                      aria-label={`Remove remote agent ${agent.name}`}
                    >
                      <XIcon />
                    </button>
                  </div>
                ))}
              </div>
            ) : (
              <p className="text-xs font-mono italic" style={{ color: 'var(--muted-foreground)' }}>
                No remote agents.
              </p>
            )}
            <div className="space-y-1">
              <div className="grid grid-cols-3 gap-2">
                <Input
                  type="text"
                  value={newAgent.name}
                  onChange={(e) => { setNewAgent((a) => ({ ...a, name: e.target.value })); setAgentError(null); }}
                  placeholder="Name"
                  aria-label="Remote agent name"
                  className="font-mono text-sm"
                />
                <Input
                  type="text"
                  value={newAgent.prefix}
                  onChange={(e) => { setNewAgent((a) => ({ ...a, prefix: e.target.value })); setAgentError(null); }}
                  placeholder="Prefix"
                  aria-label="Remote agent prefix"
                  className="font-mono text-sm"
                />
                <Input
                  type="text"
                  value={newAgent.bot}
                  onChange={(e) => { setNewAgent((a) => ({ ...a, bot: e.target.value })); setAgentError(null); }}
                  placeholder="Bot"
                  aria-label="Remote agent bot"
                  className="font-mono text-sm"
                />
              </div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={handleAddAgent}
                disabled={!newAgent.name.trim() || !newAgent.prefix.trim() || !newAgent.bot.trim()}
              >
                Add Agent
              </Button>
              {agentError && (
                <p className="text-xs font-mono" style={{ color: 'var(--destructive)' }} role="alert">
                  {agentError}
                </p>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

export function TelegramGroups({ status, onConfigure }: TelegramGroupsProps) {
  const [newChatId, setNewChatId] = useState('');
  const [newName, setNewName] = useState('');
  const [addError, setAddError] = useState<string | null>(null);

  const groups: TelegramGroup[] = status.groups ?? [];

  function handleUpdateGroup(idx: number, updated: TelegramGroup) {
    const next = groups.map((g, i) => (i === idx ? updated : g));
    onConfigure({ groups: next });
  }

  function handleRemoveGroup(idx: number) {
    onConfigure({ groups: groups.filter((_, i) => i !== idx) });
  }

  function handleAddGroup() {
    const trimmedId = newChatId.trim();
    const trimmedName = newName.trim();
    if (!trimmedId || !trimmedName) {
      setAddError('Both chat ID and name are required.');
      return;
    }
    const parsed = Number(trimmedId);
    if (!Number.isInteger(parsed) || parsed === 0) {
      setAddError('Chat ID must be a non-zero integer.');
      return;
    }
    if (groups.some((g) => g.chat_id === parsed)) {
      setAddError('A group with this chat ID already exists.');
      return;
    }
    setAddError(null);
    setNewChatId('');
    setNewName('');
    onConfigure({ groups: [...groups, { chat_id: parsed, name: trimmedName }] });
  }

  return (
    <div className="space-y-4">
      <div>
        <Label className="text-xs font-mono uppercase tracking-wider" style={{ color: 'var(--text-secondary)' }}>
          Groups
        </Label>
        <p className="mt-1 text-xs font-mono" style={{ color: 'var(--muted-foreground)' }}>
          Telegram groups the bot participates in, with trusted bots and remote agents.
        </p>
      </div>

      {groups.length > 0 ? (
        <div className="space-y-2" role="list" aria-label="Configured Telegram groups">
          {groups.map((group, idx) => (
            <div key={group.chat_id} role="listitem">
              <GroupItem
                group={group}
                onUpdate={(updated) => handleUpdateGroup(idx, updated)}
                onRemove={() => handleRemoveGroup(idx)}
              />
            </div>
          ))}
        </div>
      ) : (
        <p className="text-xs font-mono italic" style={{ color: 'var(--muted-foreground)' }}>
          No groups configured.
        </p>
      )}

      {/* Add new group */}
      <div className="space-y-1">
        <div className="flex gap-2">
          <Input
            type="text"
            inputMode="numeric"
            pattern="-?[0-9]*"
            value={newChatId}
            onChange={(e) => { setNewChatId(e.target.value); setAddError(null); }}
            onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); handleAddGroup(); } }}
            placeholder="Chat ID"
            aria-label="New group chat ID"
            className="font-mono text-sm w-36 flex-shrink-0"
          />
          <Input
            type="text"
            value={newName}
            onChange={(e) => { setNewName(e.target.value); setAddError(null); }}
            onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); handleAddGroup(); } }}
            placeholder="Group name"
            aria-label="New group name"
            className="font-mono text-sm"
          />
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={handleAddGroup}
            disabled={!newChatId.trim() || !newName.trim()}
          >
            Add
          </Button>
        </div>
        {addError && (
          <p className="text-xs font-mono" style={{ color: 'var(--destructive)' }} role="alert">
            {addError}
          </p>
        )}
      </div>
    </div>
  );
}
