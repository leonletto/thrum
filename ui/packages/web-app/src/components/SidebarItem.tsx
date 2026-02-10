import { cn } from '../lib/utils';

interface SidebarItemProps {
  icon: React.ReactNode;
  label: string;
  badge?: number;
  active?: boolean;
  onClick: () => void;
}

export function SidebarItem({
  icon: _icon, // Reserved for future use - currently using CSS nav-icon
  label,
  badge,
  active,
  onClick,
}: SidebarItemProps) {
  return (
    <button
      onClick={onClick}
      className={cn('nav-item w-full flex items-center gap-3', active && 'active')}
    >
      <div className="nav-icon"></div>
      <span className="flex-1 text-left">{label}</span>
      {badge !== undefined && badge > 0 && (
        <span className="px-2 py-0.5 text-xs rounded-full bg-red-500 text-white font-mono">
          {badge}
        </span>
      )}
    </button>
  );
}
