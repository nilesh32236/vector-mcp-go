"use client";

import { useState, useRef, useEffect } from "react";
import ReactMarkdown from "react-markdown";
import { Send, User, Bot, Loader2, Sparkles, Copy, Check, Zap } from "lucide-react";
import { sendMessage, Message, getSessionMessages } from "@/lib/api";

type ChatClientProps = {
  sessionId: string;
};

export default function ChatClient({ sessionId }: ChatClientProps) {
  const [messages, setMessages] = useState<Message[]>([]);
  const [input, setInput] = useState("");
  const [isLoading, setIsLoading] = useState(true);
  const [isSending, setIsSending] = useState(false);
  const [copiedId, setCopiedId] = useState<number | null>(null);
  
  const messagesEndRef = useRef<HTMLDivElement>(null);

  const scrollToBottom = () => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  };

  useEffect(() => {
    loadMessages();
  }, [sessionId]);

  useEffect(() => {
    scrollToBottom();
  }, [messages, isSending]);

  const loadMessages = async () => {
    try {
      setIsLoading(true);
      const data = await getSessionMessages(sessionId);
      setMessages(data);
    } catch (err) {
      console.error(err);
    } finally {
      setIsLoading(false);
    }
  };

  const handleSend = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!input.trim() || isSending) return;

    const userMessage: Message = {
      role: "user",
      content: input,
      created_at: new Date().toISOString(),
    };

    setMessages((prev) => [...prev, userMessage]);
    setInput("");
    setIsSending(true);

    try {
      const response = await sendMessage(sessionId, input);
      setMessages((prev) => [...prev, response]);
    } catch (err) {
      console.error(err);
    } finally {
      setIsSending(false);
    }
  };

  const copyToClipboard = (text: string, id: number) => {
    navigator.clipboard.writeText(text);
    setCopiedId(id);
    setTimeout(() => setCopiedId(null), 2000);
  };

  if (isLoading) {
    return (
      <div className="flex flex-col items-center justify-center h-full gap-4 text-gray-400">
        <Loader2 className="animate-spin text-brand-500" size={40} />
        <p className="text-xs font-bold uppercase tracking-widest animate-pulse">Initializing Context...</p>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full bg-background relative overflow-hidden">
      {/* Header */}
      <header className="h-16 px-8 border-b border-(--border) flex items-center justify-between bg-background/80 backdrop-blur-md z-10">
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-lg bg-brand-500/10 flex items-center justify-center">
            <Zap size={16} className="text-brand-600" />
          </div>
          <div>
            <h2 className="text-sm font-bold tracking-tight">Intelligence Engine</h2>
            <div className="flex items-center gap-1.5">
              <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
              <span className="text-[10px] font-bold text-emerald-600 uppercase tracking-widest">Active Context</span>
            </div>
          </div>
        </div>
        <div className="text-[10px] font-bold text-gray-400 uppercase tracking-widest bg-gray-100 dark:bg-gray-800 px-2 py-1 rounded">
          Session: {sessionId.substring(0, 8)}
        </div>
      </header>

      {/* Messages */}
      <div className="flex-1 overflow-y-auto px-4 py-8 md:px-8 space-y-8 custom-scrollbar" role="log" aria-live="polite">
        {messages.map((msg, idx) => (
          <div
            key={idx}
            className={`flex gap-4 md:gap-6 ${
              msg.role === "user" ? "flex-row-reverse" : "flex-row"
            } animate-in fade-in slide-in-from-bottom-2 duration-300`}
          >
            <div
              className={`w-9 h-9 rounded-xl flex items-center justify-center shrink-0 shadow-sm ${
                msg.role === "user" ? "bg-brand-600 text-white" : "bg-white dark:bg-gray-800 border border-(--border) text-brand-600"
              }`}
            >
              {msg.role === "user" ? <User size={18} /> : <Bot size={18} />}
            </div>
            <div className={`flex flex-col max-w-[85%] md:max-w-[75%] ${msg.role === "user" ? "items-end" : "items-start"}`}>
              <div 
                className={`relative group p-4 rounded-2xl text-sm leading-relaxed shadow-sm transition-all ${
                  msg.role === "user"
                    ? "bg-brand-600 text-white rounded-tr-none"
                    : "bg-white dark:bg-gray-900 border border-(--border) text-foreground rounded-tl-none hover:border-brand-500/30"
                }`}
              >
                <div className="prose prose-sm dark:prose-invert max-w-none break-words prose-pre:bg-gray-950 prose-pre:border prose-pre:border-(--border) prose-pre:rounded-xl prose-code:text-brand-500 dark:prose-code:text-brand-400">
                  <ReactMarkdown>{msg.content}</ReactMarkdown>
                </div>
                {msg.role === "assistant" && (
                  <div className="absolute top-2 right-2 opacity-0 group-hover:opacity-100 transition-opacity">
                    <button 
                      onClick={() => copyToClipboard(msg.content, idx)}
                      className="p-1.5 hover:bg-gray-100 dark:hover:bg-gray-800 rounded-lg text-gray-400 hover:text-brand-500 transition-all"
                    >
                      {copiedId === idx ? <Check size={14} className="text-emerald-500" /> : <Copy size={14} />}
                    </button>
                  </div>
                )}
              </div>
              <span className="text-[10px] font-bold text-gray-400 uppercase tracking-widest mt-1.5 px-1">
                {msg.role === "user" ? "You" : "Vector Engine"} • {new Date(msg.created_at || "").toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
              </span>
            </div>
          </div>
        ))}
        {isSending && (
          <div className="flex gap-4 md:gap-6 animate-pulse">
            <div className="w-9 h-9 rounded-xl bg-gray-100 dark:bg-gray-800 flex items-center justify-center shrink-0 border border-(--border)">
              <Bot size={18} className="text-brand-500 opacity-50" />
            </div>
            <div className="bg-white dark:bg-gray-900 border border-(--border) p-4 rounded-2xl rounded-tl-none flex gap-1.5">
              <div className="w-1.5 h-1.5 bg-brand-500/50 rounded-full animate-bounce" style={{ animationDelay: '0ms' }} />
              <div className="w-1.5 h-1.5 bg-brand-500/50 rounded-full animate-bounce" style={{ animationDelay: '150ms' }} />
              <div className="w-1.5 h-1.5 bg-brand-500/50 rounded-full animate-bounce" style={{ animationDelay: '300ms' }} />
            </div>
          </div>
        )}
        <div ref={messagesEndRef} className="h-4" />
      </div>

      {/* Input */}
      <div className="px-4 pb-6 md:px-8 md:pb-8 bg-gradient-to-t from-background via-background to-transparent pt-4">
        <form onSubmit={handleSend} className="max-w-4xl mx-auto relative group">
          <div className="relative flex items-center bg-white dark:bg-gray-900 border border-(--border) rounded-2xl shadow-xl shadow-brand-500/5 transition-all focus-within:border-brand-500/50 focus-within:ring-4 focus-within:ring-brand-500/5 overflow-hidden">
            <input
              type="text"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              placeholder="Query the codebase engine..."
              className="w-full pl-4 pr-14 py-4 bg-transparent outline-none text-sm font-medium placeholder:text-gray-400 placeholder:italic"
              disabled={isSending}
              aria-label="Chat input"
            />
            <button
              type="submit"
              disabled={!input.trim() || isSending}
              className={`absolute right-2 p-2.5 rounded-xl transition-all ${
                input.trim() && !isSending
                  ? "bg-brand-600 text-white shadow-lg shadow-brand-500/20 hover:bg-brand-700 active:scale-95"
                  : "bg-gray-100 dark:bg-gray-800 text-gray-400 grayscale cursor-not-allowed"
              }`}
              aria-label="Send message"
            >
              <Send size={18} strokeWidth={2.5} />
            </button>
          </div>
          <p className="mt-2 text-[10px] text-gray-400 font-bold uppercase tracking-widest text-center">
            System processed locally • v0.1.0
          </p>
        </form>
      </div>
    </div>
  );
}
