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
    <html lang="en" suppressHydrationWarning>
      <head>
        <script
          dangerouslySetInnerHTML={{
            __html: `(function(){try{const d=localStorage.getItem('fc-dark-mode');if(d==='true')document.documentElement.classList.add('dark');}catch(e){}})()`,
          }}
        />
      </head>
      <body className={inter.className}>
        <a
          href="#main-content"
          className="sr-only focus:not-sr-only focus:absolute focus:z-50 focus:px-4 focus:py-2 focus:top-2 focus:left-2 focus:rounded-lg focus:bg-indigo-600 focus:text-white focus:text-sm focus:font-medium"
        >
          Skip to main content
        </a>
        <Providers>
          <div className="flex min-h-screen">
            <Sidebar />
            <main id="main-content" className="flex-1 bg-[var(--color-bg)] p-8 pt-16 lg:pt-8 lg:ml-60">
              {children}
            </main>
          </div>
        </Providers>
      </body>
    </html>
  );
}
