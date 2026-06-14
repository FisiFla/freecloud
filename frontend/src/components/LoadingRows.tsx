interface LoadingRowsProps {
  count?: number;
  className?: string;
}

/**
 * Reusable skeleton loading rows (animated pulse bars).
 */
export default function LoadingRows({ count = 3, className = "h-16" }: LoadingRowsProps) {
  return (
    <div className="mt-6 space-y-3">
      {Array.from({ length: count }).map((_, i) => (
        <div key={i} className={`${className} animate-pulse rounded-xl bg-slate-200`} />
      ))}
    </div>
  );
}
