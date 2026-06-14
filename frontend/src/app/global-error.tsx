"use client";

// Catches errors thrown in the root layout itself. It must render its own
// <html>/<body> because it replaces the whole document on a root-level crash.
export default function GlobalError({
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <html lang="en">
      <body
        style={{
          display: "flex",
          minHeight: "100vh",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          gap: "1rem",
          fontFamily: "system-ui, sans-serif",
          color: "#1e293b",
        }}
      >
        <h2>Something went wrong</h2>
        <p style={{ color: "#64748b", fontSize: "0.875rem" }}>
          The application hit an unexpected error.
        </p>
        <button
          onClick={reset}
          style={{
            background: "#4f46e5",
            color: "white",
            border: "none",
            borderRadius: "0.5rem",
            padding: "0.5rem 1rem",
            fontSize: "0.875rem",
            cursor: "pointer",
          }}
        >
          Try again
        </button>
      </body>
    </html>
  );
}
