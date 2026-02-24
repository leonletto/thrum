import { useState, useRef, useEffect, type KeyboardEvent, type ChangeEvent } from 'react';
import { useAgentList, useGroupList } from '@thrum/shared-logic';
import { Textarea } from '@/components/ui/textarea';

interface SuggestionItem {
  id: string;
  label: string;
  sublabel?: string;
  kind: 'agent' | 'group';
}

interface MentionAutocompleteProps {
  value: string;
  onChange: (value: string, mentions: string[]) => void;
  onKeyDown?: (e: KeyboardEvent<HTMLTextAreaElement>) => void;
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  id?: string;
}

export function MentionAutocomplete({
  value,
  onChange,
  onKeyDown,
  placeholder,
  disabled,
  className,
  id,
}: MentionAutocompleteProps) {
  const [showDropdown, setShowDropdown] = useState(false);
  const [mentionSearch, setMentionSearch] = useState('');
  const [mentionStartPos, setMentionStartPos] = useState<number>(-1);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const { data: agentListData } = useAgentList();
  const { data: groupListData } = useGroupList();

  // Build merged, filtered suggestion list
  const filteredSuggestions: SuggestionItem[] = [
    ...(agentListData?.agents || [])
      .filter((agent) => agent.role.toLowerCase().includes(mentionSearch.toLowerCase()))
      .map((agent) => ({
        id: agent.agent_id,
        label: agent.role,
        sublabel: agent.display,
        kind: 'agent' as const,
      })),
    ...(groupListData?.groups || [])
      .filter((group) => group.name.toLowerCase().includes(mentionSearch.toLowerCase()))
      .map((group) => ({
        id: group.group_id,
        label: group.name,
        sublabel: group.description,
        kind: 'group' as const,
      })),
  ];

  // Extract mentions from content
  const extractMentions = (content: string): string[] => {
    const mentionPattern = /@(\w+)/g;
    const mentions: string[] = [];
    let match;
    while ((match = mentionPattern.exec(content)) !== null) {
      if (match[1]) mentions.push(match[1]);
    }
    return mentions;
  };

  // Handle textarea change
  const handleChange = (e: ChangeEvent<HTMLTextAreaElement>) => {
    const newValue = e.target.value;
    const cursorPos = e.target.selectionStart || 0;

    // Check if user typed @
    const textBeforeCursor = newValue.slice(0, cursorPos);
    const lastAtIndex = textBeforeCursor.lastIndexOf('@');

    if (lastAtIndex !== -1) {
      const textAfterAt = textBeforeCursor.slice(lastAtIndex + 1);
      // Only show dropdown if @ is at start or preceded by whitespace
      const charBeforeAt = lastAtIndex > 0 ? (newValue[lastAtIndex - 1] ?? ' ') : ' ';
      const isValidMention = /\s/.test(charBeforeAt) || lastAtIndex === 0;

      if (isValidMention && !textAfterAt.includes(' ')) {
        setShowDropdown(true);
        setMentionSearch(textAfterAt);
        setMentionStartPos(lastAtIndex);
        setSelectedIndex(0);
      } else {
        setShowDropdown(false);
      }
    } else {
      setShowDropdown(false);
    }

    const mentions = extractMentions(newValue);
    onChange(newValue, mentions);
  };

  // Insert mention at cursor position
  const insertMention = (name: string) => {
    if (mentionStartPos === -1 || !textareaRef.current) return;

    const newValue =
      value.slice(0, mentionStartPos) +
      `@${name} ` +
      value.slice(textareaRef.current.selectionStart || value.length);

    const mentions = extractMentions(newValue);
    onChange(newValue, mentions);
    setShowDropdown(false);
    setMentionSearch('');
    setMentionStartPos(-1);

    // Focus textarea
    setTimeout(() => {
      textareaRef.current?.focus();
      const newPos = mentionStartPos + name.length + 2;
      textareaRef.current?.setSelectionRange(newPos, newPos);
    }, 0);
  };

  // Handle keyboard navigation in dropdown
  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (showDropdown && filteredSuggestions.length > 0) {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setSelectedIndex((prev) => (prev + 1) % filteredSuggestions.length);
        return;
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setSelectedIndex((prev) => (prev - 1 + filteredSuggestions.length) % filteredSuggestions.length);
        return;
      } else if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        const suggestion = filteredSuggestions[selectedIndex];
        if (suggestion) insertMention(suggestion.label);
        return;
      } else if (e.key === 'Escape') {
        e.preventDefault();
        setShowDropdown(false);
        return;
      }
    }

    onKeyDown?.(e);
  };

  // Click outside to close dropdown
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (
        dropdownRef.current &&
        !dropdownRef.current.contains(event.target as Node) &&
        !textareaRef.current?.contains(event.target as Node)
      ) {
        setShowDropdown(false);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  return (
    <div className="relative">
      <Textarea
        ref={textareaRef}
        id={id}
        value={value}
        onChange={handleChange}
        onKeyDown={handleKeyDown}
        placeholder={placeholder}
        disabled={disabled}
        className={className}
      />

      {showDropdown && filteredSuggestions.length > 0 && (
        <div
          ref={dropdownRef}
          className="absolute bottom-full left-0 mb-1 w-full max-w-xs bg-[var(--panel-bg-start)] border rounded-md shadow-lg z-50 max-h-48 overflow-y-auto"
        >
          {filteredSuggestions.map((suggestion, index) => (
            <button
              key={suggestion.id}
              type="button"
              className={`w-full px-3 py-2 text-left hover:bg-accent transition-colors ${
                index === selectedIndex ? 'bg-accent' : ''
              }`}
              onClick={() => insertMention(suggestion.label)}
            >
              <div className="font-medium flex items-center gap-1">
                {suggestion.kind === 'group' && (
                  <span className="text-muted-foreground text-xs" aria-label="group">
                    #
                  </span>
                )}
                @{suggestion.label}
              </div>
              {suggestion.sublabel && (
                <div className="text-xs text-muted-foreground">{suggestion.sublabel}</div>
              )}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
