"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Cloud, Users, Grid, Shield, Settings } from "lucide-react";

const navLinks = [
  { href: "/", label: "Dashboard", icon: Cloud },
  { href: "/employees", label: "Employees", icon: Users },
  { href: "/apps", label: "App Catalog", icon: Grid },
  { href: "/audit-log", label: "Audit Log", icon: Shield },
  { href: "/settings", label: "Settings", icon: Settings },
];

export default function Sidebar() {
  const pathname = usePathname();

  const isActive = (href: string) => {
    if (href === "/") return pathname === "/";
    return pathname.startsWith(href);
  };

  return (
    <aside className="fixed left-0 top-0 z-30 flex h-screen w-60 flex-col border-r border-slate-200 bg-white">
      {/* Logo */}
      <div className="flex items-center gap-2 border-b border-slate-100 px-6 py-5">
        <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-indigo-600 text-white">
          <Cloud className="h-5 w-5" />
        </div>
        <span className="text-lg font-bold text-slate-800">FreeCloud</span>
      </div>

      {/* Navigation */}
      <nav className="flex-1 space-y-1 px-3 py-4">
        {navLinks.map((link) => {
          const active = isActive(link.href);
          return (
            <Link
              key={link.href}
              href={link.href}
              className={`flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium transition-colors ${
                active
                  ? "bg-indigo-50 text-indigo-700"
                  : "text-slate-500 hover:bg-slate-50 hover:text-slate-700"
              }`}
            >
              <link.icon className="h-5 w-5 shrink-0" />
              {link.label}
            </Link>
          );
        })}
      </nav>

      {/* Footer */}
      <div className="border-t border-slate-100 px-6 py-4">
        <p className="text-xs text-slate-400">FreeCloud v0.1.0</p>
      </div>
    </aside>
  );
}
