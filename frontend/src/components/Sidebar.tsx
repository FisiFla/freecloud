"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useSession, signOut } from "next-auth/react";
import { Cloud, Users, Grid, Shield, Settings, LogOut, ShieldCheck, LayoutDashboard, BarChart2, Layers, Lock, Plug2, FileBarChart2 } from "lucide-react";
import DarkModeToggle from "./DarkModeToggle";

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
];

export default function Sidebar() {
  const pathname = usePathname();
  const { data: session } = useSession();

  // Don't render the sidebar on auth pages (sign-in).
  if (pathname === "/signin") return null;

  const isActive = (href: string) => {
    if (href === "/") return pathname === "/";
    return !!pathname && pathname.startsWith(href);
  };

  return (
    <aside className="fixed left-0 top-0 z-30 flex h-screen w-60 flex-col border-r border-slate-200 bg-white dark:bg-slate-900 dark:border-slate-700">
      {/* Logo */}
      <div className="flex items-center gap-2 border-b border-slate-100 px-6 py-5 dark:border-slate-800">
        <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-indigo-600 text-white">
          <Cloud className="h-5 w-5" />
        </div>
        <span className="text-lg font-bold text-slate-800 dark:text-slate-100">FreeCloud</span>
      </div>

      {/* Navigation */}
      <nav className="flex-1 space-y-1 px-3 py-4">
        {navLinks.map((link) => {
          const active = isActive(link.href);
          return (
            <Link
              key={link.href}
              href={link.href}
              aria-current={active ? "page" : undefined}
              className={`flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium transition-colors ${
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
            <div className="h-8 w-8 rounded-full bg-indigo-100 flex items-center justify-center text-xs font-semibold text-indigo-700">
              {session.user.name?.charAt(0) || "U"}
            </div>
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-slate-700 truncate dark:text-slate-300">
                {session.user.name}
              </p>
              <p className="text-xs text-slate-400 truncate dark:text-slate-500">
                {session.user.email}
              </p>
            </div>
          </div>
        )}
        <div className="flex items-center justify-between">
          <p className="text-xs text-slate-400 dark:text-slate-600">FreeCloud v0.1.0</p>
          <div className="flex items-center gap-1">
            <DarkModeToggle />
            {session && (
              <button
                onClick={() => signOut({ callbackUrl: "/signin" })}
                className="text-slate-400 hover:text-red-500 transition-colors dark:text-slate-500 dark:hover:text-red-400"
                aria-label="Sign out"
                title="Sign out"
              >
                <LogOut className="h-4 w-4" />
              </button>
            )}
          </div>
        </div>
      </div>
    </aside>
  );
}
