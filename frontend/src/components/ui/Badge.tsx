import type { ReactNode } from "react";

type BadgeVariant = "success" | "warning" | "info" | "neutral";

interface BadgeProps {
  variant?: BadgeVariant;
  children: ReactNode;
  className?: string;
}

const variantStyles: Record<BadgeVariant, React.CSSProperties> = {
  success: { backgroundColor: "var(--color-success-bg)", color: "var(--color-success)" },
  warning: { backgroundColor: "var(--color-warning-bg)", color: "var(--color-warning)" },
  info: { backgroundColor: "var(--color-accent-subtle)", color: "var(--color-accent-text)" },
  neutral: { backgroundColor: "#f1f5f9", color: "#475569" },
};

export function Badge({ variant = "neutral", children, className = "" }: BadgeProps) {
  return (
    <span
      className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${className}`}
      style={variantStyles[variant]}
    >
      {children}
    </span>
  );
}
