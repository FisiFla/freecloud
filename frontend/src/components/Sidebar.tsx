"use client";

import { useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useSession, signOut } from "next-auth/react";
import { Cloud, Users, Grid, Shield, Settings, LogOut, ShieldCheck, LayoutDashboard, BarChart2, Layers, Lock, Plug2, FileBarChart2, Server, Mail, Link2, Building2, Menu, X } from "lucide-react";
import DarkModeToggle from "./DarkModeToggle";
import { useOrg } from "@/app/providers";

const navLinks = [
  { href: "/", label: "Dashboard", icon: Cloud },
  { href: "/employees", label: "Employees", icon: Users },
  { href: "/apps", label: "App Catalog", icon: Grid },
  { href: "/integrations", label: "Integrations", icon: Plug2 },
  { href: "/teams", label: "Fleet Teams", icon: Layers },
  { href: "/compliance", label: "Compliance", icon: ShieldCheck },
  { href: "/analytics", label: "Analytics", icon: BarChart2 },
  { href: "/audit-log", label: "Audit Log", icon: Shield },
  { href: "/reports", label: "Reports", icon: FileBarChart2 },
  { href: "/portal", label: "My Portal", icon: LayoutDashboard },
  { href: "/portal/security", label: "Security", icon: Lock },
  { href: "/settings", label: "Settings", icon: Settings },
  { href: "/settings/organizations", label: "Organizations", icon: Building2 },
  { href: "/settings/fleet", label: "Fleet Config", icon: Server },
  { href: "/settings/smtp", label: "SMTP", icon: Mail },
  { href: "/settings/identity-providers", label: "Identity Providers", icon: Link2 },
];

export default function Sidebar() {
  const pathname = usePathname();
  const { data: session } = useSession();
  const { me, activeOrgId, setActiveOrg } = useOrg();
  const [mobileOpen, setMobileOpen] = useState(false);

  // Close mobile menu on navigation
  const closeMobile = () => setMobileOpen(false);

  // Don't render the sidebar on unauthenticated / chrome-less pages.
  if (
    pathname === "/signin" ||
    pathname === "/setup" ||
    pathname === "/forgot-password" ||
    pathname === "/access-blocked"
  ) {
    return null;
  }

  const isActive = (href: string) => {
    if (href === "/") return pathname === "/";
    return !!pathname && pathname.startsWith(href);
  };

  const sidebarContent = (
    <>
      {/* Logo */}
      <div className="flex items-center gap-2 border-b border-slate-100 px-6 py-5 dark:border-slate-800">
        <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-indigo-600 text-white">
          <Cloud className="h-5 w-5" />
        </div>
        <span className="text-lg font-bold text-slate-800 dark:text-slate-100">FreeCloud</span>
      </div>

      {/* Org switcher */}
      {me && me.orgs.length > 0 && (
        <div className="border-b border-slate-100 px-3 py-3 dark:border-slate-800">
          <label htmlFor="org-switcher" className="sr-only">Active organization</label>
          <div className="relative">
            <Building2 className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-500 dark:text-slate-500" />
            <select
              id="org-switcher"
              value={activeOrgId}
              onChange={(e) => setActiveOrg(e.target.value)}
              className="w-full appearance-none rounded-lg border border-slate-200 bg-white py-2 pl-8 pr-3 text-sm font-medium text-slate-700 focus:border-indigo-400 focus:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-200"
            >
              {me.orgs.map((o) => (
                <option key={o.orgId} value={o.orgId}>{o.orgName}</option>
              ))}
            </select>
          </div>
        </div>
      )}

      {/* Navigation */}
      <nav className="flex-1 space-y-1 overflow-y-auto px-3 py-4">
        {navLinks.map((link) => {
          const active = isActive(link.href);
          return (
            <Link
              key={link.href}
              href={link.href}
              onClick={closeMobile}
              aria-current={active ? "page" : undefined}
              className={`flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:ring-indigo-500 ${
                active
                  ? "bg-indigo-50 text-indigo-700 dark:bg-indigo-950 dark:text-indigo-300"
                  : "text-slate-500 hover:bg-slate-50 hover:text-slate-700 dark:text-slate-400 dark:hover:bg-slate-800 dark:hover:text-slate-200"
              }`}
            >
              <link.icon className="h-5 w-5 shrink-0" />
              {link.label}
            </Link>
          );
        })}
      </nav>

      {/* Footer */}
      <div className="border-t border-slate-100 px-6 py-4 dark:border-slate-800">
        {session?.user && (
          <div className="mb-3 flex items-center gap-3">
            <div className="flex h-8 w-8 items-center justify-center rounded-full bg-indigo-100 text-xs font-semibold text-indigo-700">
              {session.user.name?.charAt(0) || "U"}
            </div>
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium text-slate-700 dark:text-slate-300">{session.user.name}</p>
              <p className="truncate text-xs text-slate-500 dark:text-slate-500">{session.user.email}</p>
            </div>
          </div>
        )}
        <div className="flex items-center justify-between">
          <p className="text-xs text-slate-500 dark:text-slate-600">FreeCloud v1.7.0</p>
          <div className="flex items-center gap-1">
            <DarkModeToggle />
            {session && (
              <button
                onClick={() => signOut({ callbackUrl: "/signin" })}
                className="rounded-lg p-2.5 text-surface-fg-secondary transition-colors hover:bg-red-50 hover:text-red-600 focus-visible:ring-2 focus-visible:ring-indigo-500 dark:text-slate-500 dark:hover:bg-red-950 dark:hover:text-red-400"
                aria-label="Sign out"
                title="Sign out"
              >
                <LogOut className="h-4 w-4" />
              </button>
            )}
          </div>
        </div>
      </div>
    </>
  );

  return (
    <>
      {/* Mobile hamburger */}
      <button
        onClick={() => setMobileOpen(!mobileOpen)}
        className="fixed left-4 top-4 z-40 rounded-lg border border-slate-200 bg-white p-2.5 shadow-sm lg:hidden dark:border-slate-700 dark:bg-slate-900 focus-visible:ring-2 focus-visible:ring-indigo-500"
        aria-label={mobileOpen ? "Close sidebar" : "Open sidebar"}
        aria-expanded={mobileOpen}
      >
        {mobileOpen ? <X className="h-5 w-5 text-slate-700 dark:text-slate-300" /> : <Menu className="h-5 w-5 text-slate-700 dark:text-slate-300" />}
      </button>

      {/* Desktop sidebar (always visible) */}
      <aside className="fixed left-0 top-0 z-30 hidden h-screen w-60 flex-col border-r border-slate-200 bg-white lg:flex dark:bg-slate-900 dark:border-slate-700">
        {sidebarContent}
      </aside>

      {/* Mobile drawer (overlay) */}
      {mobileOpen && (
        <div className="fixed inset-0 z-30 lg:hidden">
          {/* Backdrop */}
          <div className="absolute inset-0 bg-black/30" onClick={() => setMobileOpen(false)} />
          {/* Drawer */}
          <aside className="relative flex h-full w-60 flex-col border-r border-slate-200 bg-white dark:bg-slate-900 dark:border-slate-700 shadow-xl">
            {sidebarContent}
          </aside>
        </div>
      )}
    </>
  );
}
