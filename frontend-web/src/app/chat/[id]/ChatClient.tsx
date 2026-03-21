"use client";

import { useEffect, useState, useRef } from "react";
import { getSession, sendChatMessage, Message } from "@/lib/api";
import ReactMarkdown from "react-markdown";
import { Send, Loader2, Bot, User } from "lucide-react";

export default function ChatClient({ sessionId }: { sessionId: string }) {
  const [messages, setMessages] = useState<Message[]>([]);
  const [loading, setLoading] = useState(true);
  const [sending, setSending] = useState(false);
  const [model, setModel] = useState("gemini-2.5-flash");
  const [input, setInput] = useState("");
  const endRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    fetchSession();
  }, [sessionId]);

  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages, sending]);

  const fetchSession = async () => {
    try {
      setLoading(true);
      const session = await getSession(sessionId);
      if (session && session.history) {
        setMessages(session.history);
      }
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  const handleSend = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!input.trim() || sending) return;

    const userMsg: Message = { role: "user", content: input };
    setMessages((prev) => [...prev, userMsg]);
    setInput("");
    setSending(true);

    try {
      const resp = await sendChatMessage(sessionId, userMsg.content, model);
      const assistantMsg: Message = {
        role: "assistant",
        content: resp.content || resp.response || "",
      };
      setMessages((prev) => [...prev, assistantMsg]);
    } catch (err: any) {
      console.error(err);
      setMessages((prev) => [
        ...prev,
        { role: "assistant", content: `**Error:** ${err.message}` },
      ]);
    } finally {
      setSending(false);
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <Loader2 className="animate-spin text-blue-500" size={32} />
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full bg-gray-950 text-gray-200">
      {/* Header */}
      <div className="flex items-center justify-between px-6 py-4 border-b border-gray-800 bg-gray-900/80 backdrop-blur-md sticky top-0 z-10 w-full shadow-sm">
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded bg-blue-500/20 flex items-center justify-center">
            <Bot size={20} className="text-blue-400" />
          </div>
          <div>
            <h2 className="text-lg font-semibold tracking-tight leading-tight">
              Global Brain Chat
            </h2>
            <div className="text-xs text-gray-500 font-mono">
              Session: {sessionId.substring(0, 8)}
            </div>
          </div>
        </div>
        <select
          value={model}
          onChange={(e) => setModel(e.target.value)}
          className="bg-gray-800 text-sm border border-gray-700 rounded-lg px-3 py-1.5 focus:outline-none focus:border-blue-500 transition-colors cursor-pointer"
        >
          <option value="gemini-2.5-flash">Gemini 2.5 Flash</option>
          <option value="gemini-3.1-pro">Gemini 3.1 Pro</option>
        </select>
      </div>

      {/* Messages */}
      <div className="flex-1 overflow-y-auto p-4 sm:p-6 space-y-6 scroll-smooth">
        {messages.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-full text-gray-500 space-y-4">
            <Bot size={48} className="text-gray-700" />
            <p>No messages yet. Ask me anything about your docs or code!</p>
          </div>
        ) : (
          messages.map((m, idx) => (
            <div
              key={idx}
              className={`flex ${
                m.role === "user" ? "justify-end" : "justify-start"
              }`}
            >
              <div className="max-w-[85%] flex gap-3">
                {m.role === "assistant" && (
                  <div className="w-8 h-8 shrink-0 rounded-full bg-gray-800 border border-gray-700 flex items-center justify-center mt-1">
                    <Bot size={16} className="text-emerald-400" />
                  </div>
                )}

                <div
                  className={`rounded-2xl px-5 py-3 ${
                    m.role === "user"
                      ? "bg-blue-600 text-white rounded-br-none shadow-blue-900/20 shadow-lg"
                      : "bg-gray-800/80 text-gray-200 rounded-bl-none shadow-md border border-gray-700/50"
                  }`}
                >
                  {m.role === "user" ? (
                    <div className="whitespace-pre-wrap">{m.content}</div>
                  ) : (
                    <div className="prose prose-invert prose-emerald max-w-none prose-p:leading-relaxed prose-pre:bg-gray-900/80 prose-pre:border prose-pre:border-gray-700 prose-a:text-blue-400">
                      <ReactMarkdown>{m.content}</ReactMarkdown>
                    </div>
                  )}
                </div>

                {m.role === "user" && (
                  <div className="w-8 h-8 shrink-0 rounded-full bg-blue-900/50 border border-blue-800 flex items-center justify-center mt-1">
                    <User size={16} className="text-blue-200" />
                  </div>
                )}
              </div>
            </div>
          ))
        )}
        {sending && (
          <div className="flex justify-start">
            <div className="max-w-[85%] flex gap-3">
              <div className="w-8 h-8 shrink-0 rounded-full bg-gray-800 border border-gray-700 flex items-center justify-center mt-1">
                <Bot size={16} className="text-emerald-400" />
              </div>
              <div className="bg-gray-800/50 text-gray-400 rounded-2xl rounded-bl-none px-5 py-3 shadow-md border border-gray-700 flex items-center gap-3">
                <Loader2 className="animate-spin" size={16} />
                <span className="text-sm animate-pulse">
                  Querying vectors and thinking...
                </span>
              </div>
            </div>
          </div>
        )}
        <div ref={endRef} />
      </div>

      {/* Input */}
      <div className="p-4 sm:p-6 bg-gray-950 border-t border-gray-800/50 relative">
        <form
          onSubmit={handleSend}
          className="max-w-4xl mx-auto relative flex items-center"
        >
          <input
            type="text"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            disabled={sending}
            placeholder="Ask a question about your indexed files..."
            className="w-full bg-gray-900/80 text-white border border-gray-700 rounded-full pl-6 pr-14 py-4 focus:outline-none focus:border-blue-500/70 focus:ring-4 focus:ring-blue-500/10 transition-all shadow-inner placeholder:text-gray-500 disabled:opacity-50"
          />
          <button
            type="submit"
            disabled={!input.trim() || sending}
            className="absolute right-2 top-2 bottom-2 aspect-square bg-blue-600 hover:bg-blue-500 disabled:bg-gray-800 disabled:text-gray-600 transition-colors rounded-full flex items-center justify-center text-white shadow-md cursor-pointer"
          >
            {sending ? (
              <Loader2 size={18} className="animate-spin" />
            ) : (
              <Send size={18} />
            )}
          </button>
        </form>
      </div>
    </div>
  );
}
