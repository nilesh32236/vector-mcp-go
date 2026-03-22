"use client";

import { useState, useEffect, Suspense } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import {
  Hammer,
  ArrowLeft,
  Play,
  Loader2,
  Terminal,
  ChevronRight,
  Settings,
  Sparkles,
} from "lucide-react";
import { listTools, callTool } from "@/lib/api";
import Link from "next/link";
import ResultRenderer from "@/components/ResultRenderer";

function ExecutorContent() {
  const [selectedTool, setSelectedTool] = useState<any | null>(null);
  const [args, setArgs] = useState<Record<string, any>>({});
  const [result, setResult] = useState<any>(null);
  const [isExecuting, setIsExecuting] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const [isSidebarCollapsed, setIsSidebarCollapsed] = useState(false);

  const searchParams = useSearchParams();
  const router = useRouter();
  const path = searchParams.get("path");
  const toolName = searchParams.get("tool");

  useEffect(() => {
    if (!path || !toolName) {
      router.push("/tools");
      return;
    }

    const fetchTool = async () => {
      try {
        const tools = await listTools();
        const tool = tools.find((t) => t.name === toolName);
        if (tool) {
          setSelectedTool(tool);

          // Initial args
          const initialArgs: Record<string, any> = {};
          if (tool.inputSchema.properties.project_path)
            initialArgs.project_path = path;
          if (tool.inputSchema.properties.target_path)
            initialArgs.target_path = path;
          if (tool.inputSchema.properties.project_id)
            initialArgs.project_id = path;
          if (tool.inputSchema.properties.directory_path)
            initialArgs.directory_path = path;
          setArgs(initialArgs);
        } else {
          router.push(`/tools/explorer?path=${encodeURIComponent(path)}`);
        }
      } catch (e) {
        console.error("Failed to fetch tool:", e);
      } finally {
        setIsLoading(false);
      }
    };
    fetchTool();
  }, [path, toolName, router]);

  const handleExecute = async () => {
    if (!selectedTool) return;
    setIsExecuting(true);
    setResult(null);
    try {
      const data = await callTool(selectedTool.name, args);
      setResult(data);
    } catch (e: any) {
      setResult({ error: e.message || "Execution failed", isError: true });
    } finally {
      setIsExecuting(false);
    }
  };

  const renderArgumentInput = (name: string, prop: any) => {
    const isRequired = selectedTool?.inputSchema.required?.includes(name);

    if (prop.type === "string") {
      const isLargeInput =
        name.toLowerCase().includes("query") ||
        name.toLowerCase().includes("text") ||
        name.toLowerCase().includes("content");

      return (
        <div key={name} className="space-y-1">
          <label className="text-[10px] font-bold uppercase tracking-widest text-gray-400 flex items-center gap-2">
            {name} {isRequired && <span className="text-red-500">*</span>}
          </label>
          {isLargeInput ? (
            <textarea
              value={args[name] || ""}
              onChange={(e) => setArgs({ ...args, [name]: e.target.value })}
              placeholder={prop.description || name}
              rows={4}
              className="w-full bg-white dark:bg-gray-800 border border-(--border) rounded-2xl px-5 py-3 text-sm font-medium outline-none focus:border-brand-500 focus:ring-4 focus:ring-brand-500/5 transition-all text-foreground shadow-sm resize-none custom-scrollbar"
            />
          ) : (
            <input
              type="text"
              value={args[name] || ""}
              onChange={(e) => setArgs({ ...args, [name]: e.target.value })}
              placeholder={prop.description || name}
              className="w-full bg-white dark:bg-gray-800 border border-(--border) rounded-2xl px-5 py-3 text-sm font-medium outline-none focus:border-brand-500 focus:ring-4 focus:ring-brand-500/5 transition-all text-foreground shadow-sm"
            />
          )}
        </div>
      );
    }

    if (prop.type === "number" || prop.type === "integer") {
      return (
        <div key={name} className="space-y-2">
          <label className="text-[10px] font-bold uppercase tracking-widest text-gray-400">
            {name} {isRequired && <span className="text-red-500">*</span>}
          </label>
          <input
            type="number"
            value={args[name] || ""}
            onChange={(e) =>
              setArgs({ ...args, [name]: Number(e.target.value) })
            }
            className="w-full bg-white dark:bg-gray-800 border border-(--border) rounded-2xl px-5 py-3 text-sm font-medium outline-none focus:border-brand-500 focus:ring-4 focus:ring-brand-500/5 transition-all text-foreground shadow-sm"
          />
        </div>
      );
    }

    if (prop.type === "boolean") {
      return (
        <label
          key={name}
          className="flex items-center gap-4 py-4 px-6 bg-white dark:bg-gray-800 border border-(--border) rounded-2xl cursor-pointer group hover:border-brand-500/30 transition-all"
        >
          <div className="flex-1">
            <p className="text-xs font-bold uppercase tracking-widest text-gray-400 mb-0.5">
              {name}
            </p>
            <p className="text-[11px] text-gray-500">
              {prop.description || "Enable this option"}
            </p>
          </div>
          <input
            type="checkbox"
            checked={!!args[name]}
            onChange={(e) => setArgs({ ...args, [name]: e.target.checked })}
            className="w-5 h-5 rounded-lg border-2 border-(--border) text-brand-600 focus:ring-brand-500 transition-all cursor-pointer"
          />
        </label>
      );
    }

    return null;
  };

  if (isLoading) {
    return (
      <div className="h-full flex items-center justify-center">
        <Loader2 size={40} className="animate-spin text-brand-500" />
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full bg-background animate-in fade-in duration-500">
      {/* Header */}
      <div className="px-8 py-6 border-b border-(--border) bg-white dark:bg-gray-900 flex items-center justify-between sticky top-0 z-10 backdrop-blur-md">
        <div className="flex items-center gap-6">
          <Link
            href={`/tools/explorer?path=${encodeURIComponent(path || "")}`}
            className="p-2.5 bg-gray-50 dark:bg-gray-800 text-gray-400 hover:text-brand-500 rounded-xl transition-all hover:scale-105 active:scale-95"
          >
            <ArrowLeft size={20} />
          </Link>
          <div className="h-8 w-px bg-(--border)" />
          <div className="flex items-center gap-4">
            <div className="w-10 h-10 rounded-xl bg-slate-100 dark:bg-slate-800 flex items-center justify-center text-slate-600 dark:text-slate-400 border border-slate-200 dark:border-slate-700 shadow-sm">
              <Terminal size={20} />
            </div>
            <div>
              <div className="flex items-center gap-2">
                <h1 className="text-lg font-black tracking-tight text-slate-900 dark:text-slate-100 uppercase">
                  {toolName?.replace(/_/g, " ")}
                </h1>
                <span className="text-[9px] bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400 px-2 py-0.5 rounded-full font-bold uppercase tracking-wider border border-slate-200 dark:border-slate-700">
                  Tool
                </span>
              </div>
              <p className="text-[10px] text-slate-400 dark:text-slate-500 font-bold truncate max-w-sm mt-0.5 tracking-wide">
                {path}
              </p>
            </div>
          </div>
        </div>

        <button
          onClick={handleExecute}
          disabled={isExecuting}
          className="flex items-center gap-2.5 bg-slate-900 dark:bg-slate-100 hover:bg-slate-800 dark:hover:bg-white disabled:bg-slate-200 dark:disabled:bg-slate-800 text-white dark:text-slate-900 px-6 py-2.5 rounded-xl font-bold transition-all shadow-lg active:scale-95 text-xs uppercase tracking-widest border border-slate-700 dark:border-slate-200"
        >
          {isExecuting ? (
            <Loader2 className="animate-spin" size={16} />
          ) : (
            <Play size={16} fill="currentColor" />
          )}
          {isExecuting ? "Executing..." : "Execute"}
        </button>
      </div>

      <div className="flex-1 overflow-y-auto custom-scrollbar">
        <div className="max-w-400 mx-auto p-8 flex flex-col lg:flex-row gap-8">
          {/* Controls Panel */}
          <div
            className={`transition-all duration-500 ease-in-out shrink-0 ${
              isSidebarCollapsed
                ? "w-0 overflow-hidden opacity-0 pointer-events-none -ml-8"
                : "w-full lg:w-90 opacity-100"
            }`}
          >
            <div className="sticky top-10">
              <div className="space-y-4">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2 text-[10px] font-bold uppercase tracking-[0.2em] text-gray-400">
                    <Settings size={14} />
                    Configuration
                  </div>
                </div>
                <div className="p-8 bg-white dark:bg-gray-900 border border-(--border) rounded-3xl space-y-8 shadow-sm">
                  {selectedTool &&
                    Object.entries(selectedTool.inputSchema.properties).map(
                      ([name, prop]) => renderArgumentInput(name, prop),
                    )}
                </div>
              </div>

              <div className="p-6 bg-slate-900 dark:bg-slate-800/50 rounded-2xl text-slate-300 border border-slate-700 dark:border-slate-700/50 shadow-xl space-y-4">
                <Sparkles size={18} className="text-brand-400" />
                <h3 className="font-bold text-sm text-white">
                  Advanced Search
                </h3>
                <p className="text-[10px] text-slate-400 leading-relaxed font-medium">
                  Refine your query with specific keywords for higher match
                  scores.
                </p>
              </div>
            </div>
          </div>

          <div className="flex-1 min-w-0 space-y-6">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2 text-[10px] font-bold uppercase tracking-[0.2em] text-gray-400">
                <Hammer size={14} />
                Live Result Output
              </div>
              <button
                onClick={() => setIsSidebarCollapsed(!isSidebarCollapsed)}
                className="flex items-center gap-2 px-4 py-2 bg-gray-50 dark:bg-gray-800 hover:bg-brand-500/10 text-gray-400 hover:text-brand-500 rounded-xl transition-all font-bold text-[10px] uppercase tracking-widest border border-transparent hover:border-brand-500/20"
              >
                {isSidebarCollapsed ? (
                  <>
                    Show Sidebar <ChevronRight size={14} />
                  </>
                ) : (
                  <>
                    <ArrowLeft size={14} /> Hide Sidebar
                  </>
                )}
              </button>
            </div>

            {isExecuting ? (
              <div className="bg-white dark:bg-gray-900/50 border border-(--border) rounded-3xl p-16 flex flex-col items-center justify-center text-center space-y-6 shadow-sm">
                <div className="relative">
                  <div className="absolute inset-0 bg-brand-500 blur-2xl opacity-20 animate-pulse" />
                  <Loader2
                    size={48}
                    className="animate-spin text-brand-500 relative"
                  />
                </div>
                <div className="space-y-2">
                  <p className="text-sm font-bold">
                    Connecting to Internal Engine...
                  </p>
                  <p className="text-xs text-gray-400">
                    Executing machine instructions for {toolName}
                  </p>
                </div>
              </div>
            ) : (
              <ResultRenderer
                result={result}
                toolName={toolName || undefined}
              />
            )}

            {!result && !isExecuting && (
              <div className="bg-white dark:bg-gray-900/50 border-2 border-dashed border-(--border) rounded-3xl p-20 flex flex-col items-center justify-center text-center space-y-6">
                <div className="w-20 h-20 rounded-4xl bg-gray-50 dark:bg-gray-800/50 flex items-center justify-center text-gray-200 dark:text-gray-700">
                  <Terminal size={40} />
                </div>
                <div className="max-w-xs">
                  <p className="text-sm font-bold text-foreground">
                    Waiting for Execution
                  </p>
                  <p className="text-xs text-gray-400 mt-2">
                    Configure the parameters on the left and click "Execute
                    Tool" to see the human-readable output here.
                  </p>
                </div>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

export default function ToolExecutor() {
  return (
    <Suspense
      fallback={
        <div className="h-full flex items-center justify-center">
          <Loader2 className="animate-spin text-brand-500" />
        </div>
      }
    >
      <ExecutorContent />
    </Suspense>
  );
}
