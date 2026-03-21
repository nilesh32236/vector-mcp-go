"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { Plus, MessageSquare, Trash2, Settings, Hammer, Wrench, LayoutDashboard } from "lucide-react";
import { getSessions, createSession, deleteSession, Session } from "@/lib/api";

export default function Sidebar() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [isDeleting, setIsDeleting] = useState<string | null>(null);
  const pathname = usePathname();
  const router = useRouter();

  useEffect(() => {
    fetchSessions();
  }, []);

  const fetchSessions = async () => {
    try {
      const data = await getSessions();
      setSessions(data);
    } catch (e) {
      console.error("Failed to fetch sessions:", e);
    }
  };

  const handleNewChat = async () => {
    try {
      const { id } = await createSession();
      await fetchSessions();
      router.push(`/chat/${id}`);
    } catch (e) {
      console.error("Failed to create session:", e);
    }
  };

  const handleDelete = async (e: React.MouseEvent, id: string) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDeleting(id);
    try {
      await deleteSession(id);
      await fetchSessions();
      if (pathname === `/chat/${id}`) {
        router.push("/");
      }
    } catch (err) {
      console.error("Failed to delete session:", err);
    } finally {
      setIsDeleting(null);
    }
  };

  return (
    <aside 
      className="w-72 bg-(--sidebar-bg) border-r border-(--border) h-screen flex flex-col pt-8 pb-4 transition-all duration-300 ease-in-out z-20"
      aria-label="Side Navigation"
    >
      <div className="px-6 mb-8">
        <div className="flex items-center gap-3 mb-8 group cursor-pointer" onClick={() => router.push("/")}>
          <div className="w-10 h-10 rounded-xl bg-brand-600 flex items-center justify-center shadow-lg shadow-brand-500/20 group-hover:scale-105 transition-transform">
            <LayoutDashboard size={22} className="text-white" />
          </div>
          <div>
            <h1 className="text-lg font-bold tracking-tight text-foreground">
              Vector MCP
            </h1>
            <p className="text-[10px] font-medium text-gray-500 uppercase tracking-widest">
              Internal Engine
            </p>
          </div>
        </div>

        <div className="space-y-3">
          <button
            onClick={handleNewChat}
            className="w-full flex items-center justify-center gap-2.5 bg-brand-600 hover:bg-brand-700 active:bg-brand-800 text-white transition-all py-2.5 px-4 rounded-xl font-semibold shadow-md shadow-brand-500/10 focus-visible:ring-2 focus-visible:ring-brand-500 focus-visible:ring-offset-2"
            aria-label="Start new chat session"
          >
            <Plus size={18} strokeWidth={2.5} />
            <span>New Session</span>
          </button>
          
          <Link
            href="/tools"
            className={`w-full flex items-center gap-3 px-4 py-2.5 rounded-xl font-medium transition-all group ${
              pathname === "/tools" 
                ? "bg-brand-500/10 text-brand-600 border border-brand-500/20 shadow-sm" 
                : "text-gray-500 hover:bg-gray-100 dark:hover:bg-gray-800/50 hover:text-foreground"
            }`}
          >
            <Wrench size={18} className={pathname === "/tools" ? "text-brand-600" : "text-gray-400 group-hover:text-foreground"} />
            <span>Management</span>
          </Link>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-4 custom-scrollbar">
        <div className="flex items-center justify-between px-2 mb-4">
          <h2 className="text-[11px] font-bold text-gray-400 uppercase tracking-widest">
            Recent Activity
          </h2>
          <span className="text-[10px] bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 rounded text-gray-500 font-medium">
            {sessions.length}
          </span>
        </div>

        <nav className="space-y-1" aria-label="Session History">
          {sessions.map((session) => {
            const isActive = pathname === `/chat/${session.id}`;
            return (
              <Link
                key={session.id}
                href={`/chat/${session.id}`}
                className={`flex items-center justify-between gap-3 px-3 py-2.5 rounded-xl group transition-all relative ${
                  isActive
                    ? "bg-white dark:bg-gray-800/50 text-foreground shadow-sm border border-(--border)"
                    : "text-gray-500 hover:text-foreground hover:bg-gray-50 dark:hover:bg-gray-800/30"
                }`}
                aria-current={isActive ? "page" : undefined}
              >
                <div className="flex items-center gap-3 overflow-hidden">
                  <MessageSquare 
                    size={18} 
                    className={`shrink-0 ${isActive ? "text-brand-500" : "text-gray-400 group-hover:text-gray-300"}`} 
                  />
                  <div className="truncate text-sm font-medium">
                    {session.id.substring(0, 12)}
                  </div>
                </div>
                <button
                  onClick={(e) => handleDelete(e, session.id)}
                  disabled={isDeleting === session.id}
                  className={`opacity-0 group-hover:opacity-100 p-1.5 hover:bg-red-50 dark:hover:bg-red-500/10 hover:text-red-500 transition-all rounded-lg ${
                    isDeleting === session.id ? "opacity-100 animate-pulse text-red-400" : ""
                  }`}
                  title="Delete Session"
                  aria-label={`Delete session ${session.id.substring(0, 8)}`}
                >
                  <Trash2 size={14} />
                </button>
              </Link>
            );
          })}
          
          {sessions.length === 0 && (
            <div className="px-3 py-8 text-center border-2 border-dashed border-gray-100 dark:border-gray-800/50 rounded-2xl">
              <p className="text-xs text-gray-400 font-medium">No sessions found</p>
            </div>
          )}
        </nav>
      </div>

      <div className="px-6 mt-auto pt-4 border-t border-(--border)">
        <button className="w-full flex items-center gap-3 px-4 py-2 text-sm font-medium text-gray-500 hover:text-foreground transition-colors group">
          <Settings size={18} className="text-gray-400 group-hover:text-foreground" />
          <span>Settings</span>
        </button>
      </div>
    </aside>
  );
}
