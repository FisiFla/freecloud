"use client";

import { useEffect, type RefObject } from "react";

/**
 * Elements that can receive keyboard focus. `input[type=hidden]` and
 * disabled controls are excluded; visibility is checked separately (see
 * isVisible) because jsdom does not do layout, so offsetParent-based checks
 * would filter out every element in tests.
 */
const FOCUSABLE_SELECTOR = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled]):not([type='hidden'])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "[tabindex]:not([tabindex='-1'])",
].join(",");

function isVisible(el: HTMLElement): boolean {
  if (el.hasAttribute("hidden")) return false;
  if (el.getAttribute("aria-hidden") === "true") return false;
  if (el.closest('[aria-hidden="true"]')) return false;
  return true;
}

/**
 * useFocusTrap keeps Tab focus inside `containerRef` while `active` is true
 * and restores focus to the previously-focused element when it is released.
 *
 * A dialog that does not trap focus lets the user Tab into the page behind
 * it, which keyboard and screen-reader users read as "the dialog is gone".
 * Restoring focus to the trigger on close is what lets a keyboard user carry
 * on where they were.
 */
export function useFocusTrap(
  containerRef: RefObject<HTMLElement | null>,
  active: boolean,
) {
  useEffect(() => {
    if (!active) return;
    const container = containerRef.current;
    if (!container) return;

    const previouslyFocused = document.activeElement as HTMLElement | null;

    const focusable = () =>
      Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter(
        isVisible,
      );

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "Tab") return;
      const items = focusable();
      if (items.length === 0) {
        e.preventDefault();
        container.focus();
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      const current = document.activeElement as HTMLElement | null;
      if (e.shiftKey) {
        if (current === first || !current || !container.contains(current)) {
          e.preventDefault();
          last.focus();
        }
      } else if (current === last || !current || !container.contains(current)) {
        e.preventDefault();
        first.focus();
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      // Restore focus to whatever was focused before the trap was armed.
      if (previouslyFocused && typeof previouslyFocused.focus === "function") {
        previouslyFocused.focus();
      }
    };
  }, [active, containerRef]);
}

/**
 * useBodyScrollLock freezes background scrolling while a modal surface is
 * open, so the page behind cannot be scrolled out of view.
 */
export function useBodyScrollLock(active: boolean) {
  useEffect(() => {
    if (!active) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previous;
    };
  }, [active]);
}
