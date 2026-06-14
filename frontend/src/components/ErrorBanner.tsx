import { AlertCircle } from "lucide-react";

interface ErrorBannerProps {
  message: string;
  title?: string;
  onDismiss?: () => void;
}

/**
 * Reusable red error banner with an optional dismiss button.
 */
export default function ErrorBanner({ message, title, onDismiss }: ErrorBannerProps) {
  return (
    <div className="flex items-start gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
      <AlertCircle className="mt-0.5 h-5 w-5 shrink-0" />
      <div className="flex-1">
        {title && <p className="font-medium">{title}</p>}
        <p className={title ? "text-red-600" : ""}>{message}</p>
      </div>
      {onDismiss && (
        <button
          onClick={onDismiss}
          className="text-red-400 hover:text-red-600"
          aria-label="Dismiss"
        >
          ✕
        </button>
      )}
    </div>
  );
}
