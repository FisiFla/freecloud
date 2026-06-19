import type { Metadata } from "next";
import { Inter } from "next/font/google";
import "./globals.css";
import Sidebar from "@/components/Sidebar";
import { Providers } from "./providers";

const inter = Inter({ subsets: ["latin"] });

// All pages depend on the next-auth session, so they cannot be statically
// prerendered at build time. Force dynamic rendering for the whole app.
export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "FreeCloud — Unified Identity & Device Management",
  description: "FreeCloud Dashboard",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body className={inter.className}>
        <Providers>
          <div className="flex min-h-screen">
            <Sidebar />
            <main className="ml-60 flex-1 bg-slate-50 p-8">{children}</main>
          </div>
        </Providers>
      </body>
    </html>
  );
}
