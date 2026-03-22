import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import "./globals.css";
import Sidebar from "@/components/Sidebar";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: "Vector MCP | Professional Codebase Explorer",
  description:
    "Accelerate your development with AI-powered codebase intelligence and documentation analysis.",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      lang="en"
      className={`${geistSans.variable} ${geistMono.variable} h-full antialiased`}
      suppressHydrationWarning
    >
      <body
        className="h-full flex flex-row m-0 p-0 overflow-hidden bg-background text-foreground"
        suppressHydrationWarning
      >
        <Sidebar />
        <main
          id="main-content"
          className="flex-1 h-full overflow-hidden relative"
          role="main"
        >
          {children}
        </main>
      </body>
    </html>
  );
}
