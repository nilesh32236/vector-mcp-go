"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { Plus, MessageSquare, Trash2, Settings, Hammer, Wrench } from "lucide-react";
import { getSessions, createSession, deleteSession, Session } from "@/lib/api";

export default function Sidebar() {
  const [sessions, setSessions] = useState<Session[]>([]);
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
      console.error(e);
    }
  };

  const handleNewChat = async () => {
    try {
      const { id } = await createSession();
      // Optimistically add to UI, but fetch to be totally sure
      await fetchSessions();
      router.push(`/chat/${id}`);
    } catch (e) {
      console.error(e);
    }
  };

  const handleDelete = async (e: React.MouseEvent, id: string) => {
    e.preventDefault();
    e.stopPropagation();
    try {
      await deleteSession(id);
      await fetchSessions();
      if (pathname === `/chat/${id}`) {
        router.push("/");
      }
    } catch (err) {
      console.error(err);
    }
  };

  return (
    <div className="w-64 bg-gray-900 text-white h-screen flex flex-col pt-6 pb-2 border-r border-gray-800">
      <div className="px-4 mb-6">
        <h1 className="text-xl font-bold text-center tracking-wide mb-6 bg-clip-text text-transparent bg-gradient-to-r from-blue-400 to-emerald-400">
          Vector MCP
        </h1>
        <div className="space-y-2">
          <button
            onClick={handleNewChat}
            className="w-full flex items-center justify-center gap-2 bg-blue-600 hover:bg-blue-500 transition-colors py-2 px-4 rounded-lg font-medium shadow-md"
          >
            <Plus size={20} />
            New Chat
          </button>
          
          <Link
            href="/tools"
            className={`w-full flex items-center justify-center gap-2 border border-gray-700 hover:bg-gray-800 transition-colors py-2 px-4 rounded-lg font-medium ${
              pathname === "/tools" ? "bg-gray-800 text-blue-400 border-blue-500/50" : "text-gray-300"
            }`}
          >
            <Wrench size={18} />
            Management
          </Link>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-4 space-y-2">
        <div className="text-xs font-semibold text-gray-500 uppercase tracking-wider px-3 mb-2">
          Chat History
        </div>
        {sessions.map((session) => {
          const isActive = pathname === `/chat/${session.id}`;
          return (
            <Link
              key={session.id}
              href={`/chat/${session.id}`}
              className={`flex items-center justify-between gap-3 px-3 py-2.5 rounded-lg group transition-colors ${
                isActive
                  ? "bg-gray-800 text-blue-400"
                  : "hover:bg-gray-800 text-gray-300"
              }`}
            >
              <div className="flex items-center gap-3 overflow-hidden">
                <MessageSquare size={18} className="shrink-0" />
                <div className="truncate text-sm">
                  {session.id.substring(0, 8)}...
                </div>
              </div>
              <button
                onClick={(e) => handleDelete(e, session.id)}
                className="opacity-0 group-hover:opacity-100 p-1 hover:text-red-400 transition-all rounded"
                title="Delete Session"
              >
                <Trash2 size={16} />
              </button>
            </Link>
          );
        })}
      </div>
    </div>
  );
}
