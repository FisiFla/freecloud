import type { LucideIcon } from "lucide-react";

interface EmptyStateProps {
  icon: LucideIcon;
  title: string;
  description?: string;
}

/**
 * Reusable empty-state panel (dashed border, centered icon + text).
 */
export default function EmptyState({ icon: Icon, title, description }: EmptyStateProps) {
  return (
    <div className="mt-8 text-center rounded-xl border border-dashed border-slate-200 bg-white p-12">
      <Icon className="mx-auto h-8 w-8 text-slate-300" />
      <h3 className="mt-3 text-sm font-medium text-slate-600">{title}</h3>
      {description && <p className="mt-1 text-sm text-slate-400">{description}</p>}
    </div>
  );
}
