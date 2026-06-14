"use client";

import { useEffect } from "react";

// Segment-level error boundary. Without this, a render-time exception anywhere
// in a page produces a full white-screen crash in production. This catches it
// and offers a recovery action.
export default function Error({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    // eslint-disable-next-line no-console
    console.error(error);
  }, [error]);

  return (
    <div className="flex min-h-[60vh] flex-col items-center justify-center gap-4 text-center">
      <h2 className="text-xl font-semibold text-slate-800">Something went wrong</h2>
      <p className="max-w-md text-sm text-slate-500">
        An unexpected error occurred while rendering this page. You can try again, or
        reload if the problem persists.
      </p>
      <button
        onClick={reset}
        className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-700"
      >
        Try again
      </button>
    </div>
  );
}
