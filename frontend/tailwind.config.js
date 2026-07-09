/** @type {import('tailwindcss').Config} */
module.exports = {
  darkMode: 'class',
  content: ["./src/**/*.{js,ts,jsx,tsx,mdx}"],
  theme: {
    extend: {
      colors: {
        primary: {
          50: "#eef2ff",
          100: "#e0e7ff",
          200: "#c7d2fe",
          300: "#a5b4fc",
          400: "#818cf8",
          500: "#6366f1",
          600: "#4f46e5",
          700: "#4338ca",
          800: "#3730a3",
          900: "#312e81",
          950: "#1e1b4b",
        },
        slate: {
          50: "#f8fafc",
          100: "#f1f5f9",
          200: "#e2e8f0",
          300: "#cbd5e1",
          400: "#94a3b8",
          500: "#64748b",
          600: "#475569",
          700: "#334155",
          800: "#1e293b",
          900: "#0f172a",
          950: "#020617",
        },
        indigo: {
          50: "#eef2ff",
          100: "#e0e7ff",
          200: "#c7d2fe",
          300: "#a5b4fc",
          400: "#818cf8",
          500: "#6366f1",
          600: "#4f46e5",
          700: "#4338ca",
          800: "#3730a3",
          900: "#312e81",
          950: "#1e1b4b",
        },
        // Semantic aliases — use these instead of raw color names
        // to keep the design system referenceable and themeable.
        accent: {
          DEFAULT: "#4f46e5",    // indigo-600
          hover: "#4338ca",      // indigo-700
          subtle: "#eef2ff",     // indigo-50
          text: "#4338ca",       // indigo-700
        },
        surface: {
          DEFAULT: "#ffffff",
          subtle: "#f8fafc",     // slate-50
          fg: "#1e293b",         // slate-800
          "fg-secondary": "#475569", // slate-600
          "fg-muted": "#64748b", // slate-500
        },
        border: {
          DEFAULT: "#e2e8f0",    // slate-200
          subtle: "#f1f5f9",     // slate-100
        },
      },
    },
  },
  plugins: [],
};
