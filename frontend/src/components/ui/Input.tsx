"use client";

import {
  type InputHTMLAttributes,
  type SelectHTMLAttributes,
  cloneElement,
  forwardRef,
  isValidElement,
  type ReactElement,
  type ReactNode,
} from "react";

interface FieldProps {
  label: string;
  error?: string;
  hint?: string;
  children: ReactNode;
  required?: boolean;
  /**
   * id of the control this field labels. The label is associated with it via
   * htmlFor and the child control receives the id, so the control gets an
   * accessible name (WCAG 1.3.1 / 4.1.2).
   *
   * When no id is given the caption is rendered as a plain <span> instead of
   * a <label>: there is no control to associate it with, and an orphan
   * <label> is announced as a form field that does not exist.
   */
  id?: string;
}

export function Field({ label, error, hint, children, required, id }: FieldProps) {
  // Only clone when an id was supplied, so a child that already carries its
  // own id is left untouched.
  const control =
    id && isValidElement(children)
      ? cloneElement(children as ReactElement<{ id?: string }>, { id })
      : children;
  const captionClass = "block text-sm font-medium text-slate-700 dark:text-slate-300";
  return (
    <div>
      {id ? (
        <label htmlFor={id} className={captionClass}>
          {label}
          {required && <span className="ml-0.5 text-red-500">*</span>}
        </label>
      ) : (
        <span className={captionClass}>
          {label}
          {required && <span className="ml-0.5 text-red-500">*</span>}
        </span>
      )}
      {control}
      {error && <p className="mt-1 text-xs text-red-500">{error}</p>}
      {hint && !error && <p className="mt-1 text-xs text-slate-500 dark:text-slate-500">{hint}</p>}
    </div>
  );
}

const inputBase =
  "mt-1 w-full rounded-lg border border-slate-200 bg-white px-3 py-2.5 text-sm text-slate-700 shadow-sm placeholder-slate-400 transition-colors focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100 dark:placeholder-slate-500 disabled:cursor-not-allowed disabled:opacity-50";

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(
  ({ className = "", ...props }, ref) => (
    <input ref={ref} className={`${inputBase} ${className}`} {...props} />
  ),
);
Input.displayName = "Input";

export const Select = forwardRef<HTMLSelectElement, SelectHTMLAttributes<HTMLSelectElement>>(
  ({ className = "", children, ...props }, ref) => (
    <select ref={ref} className={`${inputBase} ${className}`} {...props}>
      {children}
    </select>
  ),
);
Select.displayName = "Select";
