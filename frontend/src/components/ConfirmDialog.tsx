"use client";

import { AlertTriangle } from "lucide-react";
import { useEffect } from "react";

interface ConfirmDialogProps {
  isOpen: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: string;
  message: string;
  confirmLabel?: string;
  variant?: "danger" | "default";
}

export default function ConfirmDialog({
  isOpen,
  onClose,
  onConfirm,
  title,
  message,
  confirmLabel = "Confirm",
  variant = "default",
}: ConfirmDialogProps) {
  useEffect(() => {
    const handleEscape = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    if (isOpen) document.addEventListener("keydown", handleEscape);
    return () => document.removeEventListener("keydown", handleEscape);
  }, [isOpen, onClose]);

  if (!isOpen) return null;

  return (
    <>
      {/* Overlay */}
      <div className="fixed inset-0 z-50 bg-black/40" onClick={onClose} />

      {/* Modal */}
      <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
        <div className="w-full max-w-md rounded-xl bg-white p-6 shadow-2xl">
          <div className="flex items-start gap-4">
            <div
              className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-full ${
                variant === "danger" ? "bg-red-100" : "bg-indigo-100"
              }`}
            >
              <AlertTriangle
                className={`h-5 w-5 ${
                  variant === "danger" ? "text-red-600" : "text-indigo-600"
                }`}
              />
            </div>
            <div className="flex-1">
              <h3 className="text-lg font-semibold text-slate-800">{title}</h3>
              <p className="mt-1 text-sm text-slate-500">{message}</p>
            </div>
          </div>

          <div className="mt-6 flex justify-end gap-3">
            <button
              onClick={onClose}
              className="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-600 transition-colors hover:bg-slate-50"
            >
              Cancel
            </button>
            <button
              onClick={() => {
                onConfirm();
                onClose();
              }}
              className={`rounded-lg px-4 py-2 text-sm font-medium text-white transition-colors ${
                variant === "danger"
                  ? "bg-red-600 hover:bg-red-700"
                  : "bg-indigo-600 hover:bg-indigo-700"
              }`}
            >
              {confirmLabel}
            </button>
          </div>
        </div>
      </div>
    </>
  );
}
