"use client";

import { X } from "lucide-react";
import { ReactNode, useEffect, useRef } from "react";
import { useBodyScrollLock, useFocusTrap } from "./useFocusTrap";

interface SlideOverProps {
  isOpen: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  beforeClose?: () => boolean;
}

export default function SlideOver({ isOpen, onClose, title, children, beforeClose }: SlideOverProps) {
  const panelRef = useRef<HTMLDivElement>(null);

  const handleClose = () => {
    if (beforeClose && !beforeClose()) return;
    onClose();
  };

  // Trap Tab inside the panel and restore focus to the trigger on close.
  // Declared before the focus effect below so the trap captures the
  // previously-focused element before focus moves into the panel.
  useFocusTrap(panelRef, isOpen);
  useBodyScrollLock(isOpen);

  useEffect(() => {
    const handleEscape = (e: KeyboardEvent) => {
      if (e.key === "Escape") handleClose();
    };
    if (isOpen) document.addEventListener("keydown", handleEscape);
    return () => document.removeEventListener("keydown", handleEscape);
  }, [isOpen, handleClose]);

  useEffect(() => {
    if (isOpen) {
      panelRef.current?.focus();
    }
  }, [isOpen]);

  return (
    <>
      {/* Overlay — aria-hidden because it is decorative: the Close button and
          the Escape key are its keyboard equivalents. */}
      <div
        className={`fixed inset-0 z-40 bg-black/40 transition-opacity duration-300 ${
          isOpen ? "opacity-100" : "pointer-events-none opacity-0"
        }`}
        onClick={handleClose}
        aria-hidden="true"
      />

      {/* Slide panel */}
      <div
        ref={panelRef}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-labelledby="slide-over-title"
        inert={!isOpen}
        className={`fixed inset-y-0 right-0 z-50 w-full max-w-xl transform bg-white shadow-xl transition-transform duration-300 ease-in-out dark:bg-slate-900 ${
          isOpen ? "translate-x-0" : "translate-x-full"
        }`}
      >
        <div className="flex h-full flex-col">
          {/* Header */}
          <div className="flex items-center justify-between border-b border-slate-200 px-6 py-4 dark:border-slate-700">
            <h2 id="slide-over-title" className="text-lg font-semibold text-slate-800 dark:text-slate-100">{title}</h2>
            <button
              onClick={handleClose}
              aria-label="Close panel"
              className="rounded-lg p-1.5 text-slate-400 hover:bg-slate-100 hover:text-slate-600 transition-colors dark:text-slate-400 dark:hover:bg-slate-800 dark:hover:text-slate-200"
            >
              <X className="h-5 w-5" />
            </button>
          </div>

          {/* Body */}
          <div className="flex-1 overflow-y-auto px-6 py-6">{children}</div>
        </div>
      </div>
    </>
  );
}
