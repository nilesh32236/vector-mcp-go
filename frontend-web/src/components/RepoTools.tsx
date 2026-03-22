"use client";

import { useState, useEffect } from "react";
import {
  Hammer,
  ChevronRight,
  Play,
  Terminal,
  Search,
  FileSearch,
  Trash2,
  Activity,
  X,
  Loader2,
  CheckCircle2,
  AlertCircle,
} from "lucide-react";
import { listTools, callTool, Repo } from "@/lib/api";

interface Tool {
  name: string;
  description: string;
  inputSchema: {
    type: string;
    properties: Record<string, any>;
    required?: string[];
  };
}

interface RepoToolsProps {
  repo: Repo;
  onClose: () => void;
}

export default function RepoTools({ repo, onClose }: RepoToolsProps) {
  const [tools, setTools] = useState<Tool[]>([]);
  const [selectedTool, setSelectedTool] = useState<Tool | null>(null);
  const [args, setArgs] = useState<Record<string, any>>({});
  const [result, setResult] = useState<any>(null);
  const [isExecuting, setIsExecuting] = useState(false);
  const [isLoadingTools, setIsLoadingTools] = useState(true);

  useEffect(() => {
    const fetchTools = async () => {
      try {
        const data = await listTools();
        setTools(data as Tool[]);
        // Set default project_path or target_path if applicable
      } catch (e) {
        console.error("Failed to fetch tools:", e);
      } finally {
        setIsLoadingTools(false);
      }
    };
    fetchTools();
  }, []);

  const handleToolSelect = (tool: Tool) => {
    setSelectedTool(tool);
    setResult(null);

    // Pre-fill common arguments
    const initialArgs: Record<string, any> = {};
    if (tool.inputSchema.properties.project_path) {
      initialArgs.project_path = repo.path;
    }
    if (tool.inputSchema.properties.target_path) {
      initialArgs.target_path = repo.path;
    }
    if (tool.inputSchema.properties.project_id) {
      initialArgs.project_id = repo.path;
    }
    if (tool.inputSchema.properties.directory_path) {
      initialArgs.directory_path = repo.path;
    }
    setArgs(initialArgs);
  };

  const handleExecute = async () => {
    if (!selectedTool) return;
    setIsExecuting(true);
    setResult(null);
    try {
      const data = await callTool(selectedTool.name, args);
      setResult(data);
    } catch (e: any) {
      setResult({ error: e.message || "Execution failed" });
    } finally {
      setIsExecuting(false);
    }
  };

  const renderArgumentInput = (name: string, prop: any) => {
    const isRequired = selectedTool?.inputSchema.required?.includes(name);

    if (prop.type === "string") {
      return (
        <div key={name} className="space-y-1.5">
          <label className="text-[10px] font-bold uppercase tracking-wider text-gray-400 flex items-center gap-1.5">
            {name} {isRequired && <span className="text-red-500">*</span>}
          </label>
          <input
            type="text"
            value={args[name] || ""}
            onChange={(e) => setArgs({ ...args, [name]: e.target.value })}
            placeholder={prop.description || name}
            className="w-full bg-gray-50 dark:bg-gray-800 border border-(--border) rounded-xl px-3 py-2 text-xs font-medium outline-none focus:border-brand-500/50 focus:ring-4 focus:ring-brand-500/5 transition-all text-foreground"
          />
        </div>
      );
    }

    if (prop.type === "number" || prop.type === "integer") {
      return (
        <div key={name} className="space-y-1.5">
          <label className="text-[10px] font-bold uppercase tracking-wider text-gray-400">
            {name} {isRequired && <span className="text-red-500">*</span>}
          </label>
          <input
            type="number"
            value={args[name] || ""}
            onChange={(e) =>
              setArgs({ ...args, [name]: Number(e.target.value) })
            }
            className="w-full bg-gray-50 dark:bg-gray-800 border border-(--border) rounded-xl px-3 py-2 text-xs font-medium outline-none focus:border-brand-500/50 focus:ring-4 focus:ring-brand-500/5 transition-all text-foreground"
          />
        </div>
      );
    }

    if (prop.type === "boolean") {
      return (
        <div key={name} className="flex items-center gap-3 py-2">
          <input
            type="checkbox"
            checked={!!args[name]}
            onChange={(e) => setArgs({ ...args, [name]: e.target.checked })}
            className="w-4 h-4 rounded border-(--border) text-brand-600 focus:ring-brand-500"
          />
          <label className="text-xs font-medium text-gray-600 dark:text-gray-400">
            {name} (Boolean)
          </label>
        </div>
      );
    }

    return null;
  };

  return (
    <div className="fixed inset-0 bg-black/50 backdrop-blur-sm z-50 flex items-center justify-center p-4 md:p-8 animate-in fade-in duration-200">
      <div className="bg-white dark:bg-gray-900 border border-(--border) rounded-3xl w-full max-w-6xl max-h-[90vh] flex flex-col shadow-2xl animate-in zoom-in-95 duration-200">
        {/* Header */}
        <div className="p-6 border-b border-(--border) flex items-center justify-between">
          <div className="flex items-center gap-4">
            <div className="w-12 h-12 rounded-2xl bg-brand-600 flex items-center justify-center text-white shadow-lg shadow-brand-500/20">
              <Hammer size={24} />
            </div>
            <div>
              <h3 className="text-xl font-bold tracking-tight">
                Repository Intelligence
              </h3>
              <p className="text-xs text-gray-400 font-medium truncate max-w-lg mt-0.5">
                {repo.path}
              </p>
            </div>
          </div>
          <button
            onClick={onClose}
            className="p-2.5 bg-gray-100 dark:bg-gray-800 text-gray-500 rounded-xl hover:bg-gray-200 dark:hover:bg-gray-700 transition-colors"
          >
            <X size={20} />
          </button>
        </div>

        <div className="flex-1 overflow-hidden flex flex-col md:flex-row">
          {/* Tools List */}
          <div className="w-full md:w-80 border-b md:border-b-0 md:border-r border-(--border) flex flex-col bg-gray-50/50 dark:bg-black/10">
            <div className="p-4 border-b border-(--border)">
              <h4 className="text-[10px] font-bold uppercase tracking-[0.2em] text-gray-400 px-2 mb-2">
                Available Actions
              </h4>
              <div className="relative">
                <Search
                  size={14}
                  className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400"
                />
                <input
                  type="text"
                  placeholder="Filter tools..."
                  className="w-full bg-white dark:bg-gray-800 border border-(--border) rounded-lg pl-9 pr-3 py-1.5 text-xs outline-none focus:border-brand-500/30"
                />
              </div>
            </div>
            <div className="flex-1 overflow-y-auto p-2 custom-scrollbar">
              {isLoadingTools ? (
                <div className="p-10 flex flex-col items-center gap-3">
                  <Loader2 size={24} className="animate-spin text-brand-500" />
                  <p className="text-[10px] font-bold uppercase text-gray-400">
                    Loading tools...
                  </p>
                </div>
              ) : (
                tools.map((tool) => (
                  <button
                    key={tool.name}
                    onClick={() => handleToolSelect(tool)}
                    className={`w-full flex items-center justify-between p-3 rounded-xl transition-all mb-1 group text-left ${
                      selectedTool?.name === tool.name
                        ? "bg-brand-600 text-white shadow-md shadow-brand-500/20 ring-1 ring-white/10"
                        : "hover:bg-white dark:hover:bg-gray-800 text-gray-500 hover:text-foreground border border-transparent hover:border-brand-500/10"
                    }`}
                  >
                    <div className="flex items-center gap-3 overflow-hidden">
                      <div
                        className={`w-8 h-8 rounded-lg flex items-center justify-center shrink-0 ${
                          selectedTool?.name === tool.name
                            ? "bg-white/20 text-white"
                            : "bg-gray-100 dark:bg-gray-800 text-gray-400 group-hover:text-brand-500"
                        }`}
                      >
                        {tool.name.includes("search") ? (
                          <Search size={14} />
                        ) : tool.name.includes("index") ? (
                          <Activity size={14} />
                        ) : tool.name.includes("delete") ? (
                          <Trash2 size={14} />
                        ) : (
                          <Terminal size={14} />
                        )}
                      </div>
                      <div className="truncate">
                        <p className="text-xs font-bold leading-none">
                          {tool.name.replace(/_/g, " ")}
                        </p>
                        <p
                          className={`text-[9px] mt-1 line-clamp-1 italic ${selectedTool?.name === tool.name ? "text-white/60" : "text-gray-400"}`}
                        >
                          {tool.description}
                        </p>
                      </div>
                    </div>
                    <ChevronRight
                      size={14}
                      className={`shrink-0 transition-transform ${selectedTool?.name === tool.name ? "translate-x-0.5" : "text-gray-300"}`}
                    />
                  </button>
                ))
              )}
            </div>
          </div>

          {/* Execution Pane */}
          <div className="flex-1 overflow-y-auto flex flex-col custom-scrollbar bg-white dark:bg-gray-900">
            {selectedTool ? (
              <div className="p-6 md:p-8 space-y-8 max-w-3xl">
                <div className="space-y-4">
                  <div className="flex items-center justify-between">
                    <h4 className="text-xl font-extrabold tracking-tight text-foreground">
                      Configure & Run
                    </h4>
                    <button
                      onClick={handleExecute}
                      disabled={isExecuting}
                      className="flex items-center gap-2.5 bg-brand-600 hover:bg-brand-700 disabled:bg-gray-200 dark:disabled:bg-gray-800 text-white px-5 py-2.5 rounded-xl font-bold transition-all shadow-lg shadow-brand-500/10 active:scale-95 text-sm"
                    >
                      {isExecuting ? (
                        <Loader2 className="animate-spin" size={18} />
                      ) : (
                        <Play size={18} fill="currentColor" />
                      )}
                      {isExecuting ? "Executing..." : "Run Tool"}
                    </button>
                  </div>
                  <p className="text-sm text-gray-500 dark:text-gray-400 leading-relaxed bg-gray-50 dark:bg-gray-800/50 p-4 rounded-2xl border border-(--border)">
                    {selectedTool.description}
                  </p>
                </div>

                <div className="grid grid-cols-1 md:grid-cols-2 gap-6 p-1">
                  {Object.entries(selectedTool.inputSchema.properties).map(
                    ([name, prop]) => renderArgumentInput(name, prop),
                  )}
                </div>

                {/* Result Section */}
                <div className="space-y-4 pt-4">
                  <div className="flex items-center gap-2 text-xs font-bold uppercase tracking-widest text-gray-400">
                    <Terminal size={14} />
                    <span>Output Terminal</span>
                  </div>

                  <div className="min-h-75 rounded-3xl bg-black/95 dark:bg-black border border-white/10 overflow-hidden shadow-2xl flex flex-col font-mono">
                    <div className="px-5 py-3 border-b border-white/5 flex items-center justify-between bg-white/5 backdrop-blur-sm">
                      <div className="flex gap-1.5">
                        <div className="w-2.5 h-2.5 rounded-full bg-red-500/50" />
                        <div className="w-2.5 h-2.5 rounded-full bg-yellow-500/50" />
                        <div className="w-2.5 h-2.5 rounded-full bg-green-500/50" />
                      </div>
                      <span className="text-[10px] text-gray-500 uppercase tracking-tighter">
                        vector-engine-v1.1
                      </span>
                    </div>

                    <div className="flex-1 p-6 text-xs leading-relaxed overflow-auto custom-scrollbar">
                      {isExecuting ? (
                        <div className="flex items-center gap-3 text-brand-400 animate-pulse">
                          <span>$ executing {selectedTool.name}...</span>
                        </div>
                      ) : result ? (
                        <div className="space-y-4">
                          {result.isError || result.error ? (
                            <div className="text-red-400 flex items-start gap-2">
                              <AlertCircle
                                size={14}
                                className="shrink-0 mt-0.5"
                              />
                              <pre className="whitespace-pre-wrap">
                                {result.error ||
                                  JSON.stringify(result, null, 2)}
                              </pre>
                            </div>
                          ) : (
                            <div className="text-emerald-400 space-y-4">
                              <div className="flex items-center gap-2 text-[10px] font-bold uppercase tracking-[0.2em] opacity-50 mb-2">
                                <CheckCircle2 size={12} />
                                Execution Successful
                              </div>
                              <pre className="text-gray-300 whitespace-pre-wrap">
                                {typeof result === "string"
                                  ? result
                                  : JSON.stringify(result, null, 2)}
                              </pre>
                            </div>
                          )}
                        </div>
                      ) : (
                        <div className="text-gray-500 italic">
                          <span>$ waiting for tool execution...</span>
                        </div>
                      )}
                    </div>
                  </div>
                </div>
              </div>
            ) : (
              <div className="flex-1 flex flex-col items-center justify-center p-12 text-center space-y-6">
                <div className="w-20 h-20 rounded-4xl bg-gray-50 dark:bg-gray-800/50 flex items-center justify-center text-gray-200 dark:text-gray-700">
                  <Play size={40} />
                </div>
                <div className="max-w-sm">
                  <h4 className="text-lg font-bold text-foreground">
                    Select a tool to begin
                  </h4>
                  <p className="text-sm text-gray-400 mt-2">
                    Choose an MCP tool from the panel to run analysis, search,
                    or maintenance on this codebase.
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
