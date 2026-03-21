"use client";

import { useState, useEffect } from "react";
import { getIndexStatus, triggerIndex, getSkeleton, listTools, callTool } from "@/lib/api";
import { 
  RefreshCw, 
  Database, 
  FolderTree, 
  Play, 
  Loader2,
  Terminal,
  Search,
  CheckCircle2,
  FileCode,
  Zap,
  Layout,
  FileText,
  Activity,
  ChevronRight,
  Code2,
  ShieldCheck,
  Cpu,
  Monitor,
  Command,
  Info,
  AlertCircle,
  ExternalLink,
  Layers,
  Box,
  Binary
} from "lucide-react";

const TOOL_METADATA: { [key: string]: { label: string; icon: any; category: string; description: string; color: string } } = {
  "trigger_project_index": { label: "Scan Project", icon: Zap, category: "Core", description: "Deep scan your entire codebase to update the AI's memory.", color: "text-blue-400" },
  "retrieve_docs": { label: "Search Documentation", icon: FileText, category: "Explore", description: "Find relevant information in READMEs, MDX, and PDFs.", color: "text-emerald-400" },
  "retrieve_context": { label: "Find Code Snippets", icon: Search, category: "Explore", description: "Search for specific logic or implementations.", color: "text-emerald-400" },
  "check_dependency_health": { label: "Health Check", icon: ShieldCheck, category: "Quality", description: "Verify if all your code imports are correctly installed.", color: "text-orange-400" },
  "analyze_architecture": { label: "Visualize Structure", icon: Layout, category: "Analysis", description: "Generate a visual map of how your project connects.", color: "text-purple-400" },
  "find_dead_code": { label: "Cleanup Assistant", icon: FileCode, category: "Quality", description: "Identify functions or variables that aren't being used.", color: "text-orange-400" },
  "get_codebase_skeleton": { label: "Explore Files", icon: FolderTree, category: "Analysis", description: "Get a high-level view of your directory structure.", color: "text-purple-400" },
  "ping": { label: "System Check", icon: CheckCircle2, category: "System", description: "Verify the connection between UI and Backend.", color: "text-gray-400" },
  "set_project_root": { label: "Change Project", icon: Code2, category: "System", description: "Switch the active directory the AI is working on.", color: "text-gray-400" }
};

export default function ToolsPage() {
  const [activeTab, setActiveTab] = useState<"health" | "console">("health");
  const [status, setStatus] = useState<string>("");
  const [skeleton, setSkeleton] = useState<string>("");
  const [tools, setTools] = useState<any[]>([]);
  const [loading, setLoading] = useState<{ [key: string]: boolean }>({ initial: true });
  const [outputs, setOutputs] = useState<{ [key: string]: any }>({});
  const [selectedTool, setSelectedTool] = useState<string | null>("trigger_project_index");
  const [toolArgs, setToolArgs] = useState<{ [key: string]: any }>({});
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);

  useEffect(() => {
    const init = async () => {
      await Promise.all([fetchStatus(), fetchSkeleton(), fetchTools()]);
      setLoading(prev => ({ ...prev, initial: false }));
    };
    init();
  }, []);

  const showToast = (message: string, type: 'success' | 'error' = 'success') => {
    setToast({ message, type });
    setTimeout(() => setToast(null), 3000);
  };

  const fetchStatus = async () => {
    setLoading(prev => ({ ...prev, status: true }));
    try {
      const res = await getIndexStatus();
      setStatus(res.content?.[0]?.text || "");
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(prev => ({ ...prev, status: false }));
    }
  };

  const fetchSkeleton = async () => {
    setLoading(prev => ({ ...prev, skeleton: true }));
    try {
      const res = await getSkeleton();
      let text = res.content?.[0]?.text || "";
      text = text.replace(/<codebase_skeleton>|<\/codebase_skeleton>/g, "").trim();
      setSkeleton(text);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(prev => ({ ...prev, skeleton: false }));
    }
  };

  const fetchTools = async () => {
    try {
      const res = await listTools();
      setTools(res);
    } catch (e) {
      console.error(e);
    }
  };

  const handleCallTool = async (name: string) => {
    setLoading(prev => ({ ...prev, [name]: true }));
    try {
      const res = await callTool(name, toolArgs[name] || {});
      setOutputs(prev => ({ ...prev, [name]: res }));
      showToast(`${TOOL_METADATA[name]?.label || name} completed`);
    } catch (e: any) {
      console.error(e);
      setOutputs(prev => ({ ...prev, [name]: { error: e.message } }));
      showToast(e.message, "error");
    } finally {
      setLoading(prev => ({ ...prev, [name]: false }));
    }
  };

  const parseStats = () => {
    const lines = status.split("\n");
    const stats: any = {};
    lines.forEach(line => {
      if (line.includes("Fully Indexed:")) stats.indexed = line.split(":")[1].trim();
      if (line.includes("Modified:")) stats.modified = line.split(":")[1].trim();
      if (line.includes("Missing:")) stats.missing = line.split(":")[1].trim();
      if (line.includes("Deleted:")) stats.deleted = line.split(":")[1].trim();
    });
    return stats;
  };

  const stats = parseStats();

  return (
    <div className="flex h-full bg-[#050608] text-gray-300 font-sans overflow-hidden selection:bg-blue-500/30 selection:text-blue-200">
      {/* Toast Notification */}
      {toast && (
        <div className={`fixed top-8 right-8 z-[100] px-6 py-4 rounded-2xl shadow-2xl border backdrop-blur-xl flex items-center gap-3 animate-in fade-in slide-in-from-top-4 ${
          toast.type === 'error' ? 'bg-red-950/80 border-red-500/30 text-red-200' : 'bg-emerald-950/80 border-emerald-500/30 text-emerald-200'
        }`}>
          {toast.type === 'error' ? <AlertCircle size={20} /> : <CheckCircle2 size={20} />}
          <span className="font-bold text-sm">{toast.message}</span>
        </div>
      )}

      {/* Main Container */}
      <div className="flex-1 flex flex-col min-w-0 h-full relative">
        
        {/* Navigation Tabs Header */}
        <header className="shrink-0 pt-12 px-12 border-b border-gray-800/40 bg-gradient-to-b from-blue-600/5 to-transparent">
          <div className="max-w-6xl mx-auto flex flex-col md:flex-row md:items-end justify-between gap-8">
            <div className="space-y-4 pb-8">
              <div className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-blue-500/10 border border-blue-500/20 text-blue-400 text-[10px] font-black uppercase tracking-[0.2em]">
                <Cpu size={12} /> System v1.1.0
              </div>
              <h1 className="text-5xl font-black text-white tracking-tighter leading-none">Global Brain</h1>
            </div>

            <nav className="flex items-center gap-1 bg-gray-900/50 p-1 rounded-2xl border border-gray-800/50 mb-8 backdrop-blur-md">
              <button 
                onClick={() => setActiveTab("health")}
                className={`flex items-center gap-2 px-6 py-3 rounded-xl transition-all font-black text-[10px] uppercase tracking-widest ${
                  activeTab === "health" 
                    ? "bg-gray-800 text-white shadow-xl ring-1 ring-white/5" 
                    : "text-gray-500 hover:text-gray-300 hover:bg-gray-800/30"
                }`}
              >
                <Monitor size={14} className={activeTab === "health" ? "text-blue-400" : ""} />
                System Health
              </button>
              <button 
                onClick={() => setActiveTab("console")}
                className={`flex items-center gap-2 px-6 py-3 rounded-xl transition-all font-black text-[10px] uppercase tracking-widest ${
                  activeTab === "console" 
                    ? "bg-gray-800 text-white shadow-xl ring-1 ring-white/5" 
                    : "text-gray-500 hover:text-gray-300 hover:bg-gray-800/30"
                }`}
              >
                <Command size={14} className={activeTab === "console" ? "text-blue-400" : ""} />
                Operation Console
              </button>
            </nav>
          </div>
        </header>

        {/* Tab Content Area */}
        <main className="flex-1 overflow-y-auto custom-scrollbar p-12">
          <div className="max-w-6xl mx-auto">
            
            {/* TAB 1: SYSTEM HEALTH */}
            {activeTab === "health" && (
              <div className="animate-in fade-in slide-in-from-bottom-4 duration-700 space-y-12">
                
                {/* Metrics Grid */}
                <div className="grid grid-cols-1 md:grid-cols-4 gap-6">
                  {[
                    { label: "Indexed", value: stats.indexed || "0", color: "text-emerald-400", bg: "bg-emerald-500/5", border: "border-emerald-500/20", icon: ShieldCheck },
                    { label: "Modified", value: stats.modified || "0", color: "text-orange-400", bg: "bg-orange-500/5", border: "border-orange-500/20", icon: Activity },
                    { label: "Missing", value: stats.missing || "0", color: "text-blue-400", bg: "bg-blue-500/5", border: "border-blue-500/20", icon: Box },
                    { label: "Deleted", value: stats.deleted || "0", color: "text-red-400", bg: "bg-red-500/5", border: "border-red-500/20", icon: Layers }
                  ].map((m, i) => (
                    <div key={i} className={`p-8 rounded-[2rem] border ${m.border} ${m.bg} backdrop-blur-sm group hover:scale-[1.02] transition-all`}>
                      <div className="flex items-center justify-between mb-4">
                        <m.icon size={20} className={m.color} />
                        <span className={`text-3xl font-black font-mono tracking-tighter ${m.color}`}>{m.value}</span>
                      </div>
                      <div className="text-[10px] font-black text-gray-500 uppercase tracking-[0.2em]">{m.label} Files</div>
                    </div>
                  ))}
                </div>

                {/* Workspace Visualizer */}
                <div className="space-y-6">
                  <div className="flex items-center justify-between px-2">
                    <h3 className="text-[10px] font-black text-gray-600 uppercase tracking-[0.3em] flex items-center gap-2">
                      <FolderTree size={14} /> Structure Visualization
                    </h3>
                    <button onClick={fetchSkeleton} className="p-2 hover:bg-gray-800 rounded-lg transition-colors text-gray-500 hover:text-blue-400">
                      <RefreshCw size={14} className={loading.skeleton ? "animate-spin" : ""} />
                    </button>
                  </div>
                  <div className="bg-[#0a0c10] border border-gray-800/60 rounded-[3rem] p-10 shadow-2xl ring-1 ring-white/[0.02] relative overflow-hidden">
                    <div className="absolute top-0 right-0 p-12 opacity-[0.03] text-white pointer-events-none">
                      <Binary size={300} />
                    </div>
                    <div className="relative z-10 font-mono text-[12px] leading-[1.8] text-gray-500 bg-black/20 rounded-3xl p-8 max-h-[500px] overflow-auto custom-scrollbar">
                      {skeleton ? (
                        skeleton.split("\n").map((line, i) => (
                          <div key={i} className="hover:text-gray-300 transition-colors py-0.5 whitespace-pre group">
                            <span className="inline-block w-8 opacity-10 group-hover:opacity-30 text-[9px] font-sans">{i+1}</span>
                            {line.replace(/│/g, "╎").replace(/├──/g, "├─").replace(/└──/g, "└─")}
                          </div>
                        ))
                      ) : (
                        <div className="flex items-center justify-center py-20 animate-pulse uppercase tracking-[0.4em] font-black text-xs text-gray-700">Analyzing Filesystem...</div>
                      )}
                    </div>
                  </div>
                </div>
              </div>
            )}

            {/* TAB 2: OPERATION CONSOLE */}
            {activeTab === "console" && (
              <div className="animate-in fade-in slide-in-from-bottom-4 duration-700 grid grid-cols-1 lg:grid-cols-12 gap-10 min-h-[600px]">
                
                {/* Console Side Menu */}
                <div className="lg:col-span-4 space-y-8">
                  <div className="space-y-6 overflow-y-auto custom-scrollbar pr-2 max-h-[700px]">
                    {["Core", "Explore", "Quality", "Analysis", "System"].map(cat => {
                      const catTools = tools.filter(t => (TOOL_METADATA[t.name]?.category || "System") === cat);
                      if (catTools.length === 0) return null;
                      
                      return (
                        <div key={cat} className="space-y-3">
                          <div className="text-[9px] font-black text-gray-700 uppercase tracking-[0.4em] px-4">{cat} Operations</div>
                          <div className="space-y-1.5">
                            {catTools.map(tool => {
                              const meta = TOOL_METADATA[tool.name];
                              const isActive = selectedTool === tool.name;
                              const Icon = meta?.icon || Terminal;
                              
                              return (
                                <button
                                  key={tool.name}
                                  onClick={() => setSelectedTool(tool.name)}
                                  className={`w-full text-left p-5 rounded-3xl transition-all flex items-center gap-5 relative group ${
                                    isActive 
                                      ? "bg-blue-600 shadow-2xl shadow-blue-500/20 text-white" 
                                      : "bg-gray-900/40 border border-gray-800/50 hover:border-gray-700 hover:bg-gray-800/40 text-gray-400"
                                  }`}
                                >
                                  <div className={`p-2.5 rounded-2xl transition-colors ${isActive ? "bg-white/20" : "bg-black/40 group-hover:bg-black/60"}`}>
                                    <Icon size={18} className={isActive ? "text-white" : meta?.color || "text-gray-500"} />
                                  </div>
                                  <div className="min-w-0">
                                    <div className="text-xs font-black uppercase tracking-widest">{meta?.label || tool.name}</div>
                                    <div className={`text-[9px] mt-1 font-bold opacity-40 ${isActive ? "text-blue-100" : ""}`}>{meta?.category} Command</div>
                                  </div>
                                  {!isActive && <ChevronRight size={14} className="ml-auto opacity-0 group-hover:opacity-40 transition-all" />}
                                </button>
                              );
                            })}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </div>

                {/* Console Output Area */}
                <div className="lg:col-span-8 flex flex-col bg-[#0a0c10] border border-gray-800/60 rounded-[3rem] shadow-2xl relative overflow-hidden">
                  {selectedTool ? (
                    <div className="flex-1 flex flex-col min-h-0">
                      
                      {/* Tool Detail Header */}
                      <div className="p-10 pb-8 space-y-8 bg-gradient-to-br from-blue-600/[0.03] to-transparent shrink-0">
                        <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-8">
                          <div className="flex items-center gap-6">
                            <div className={`p-5 rounded-[2rem] border bg-gray-900 shadow-2xl ${TOOL_METADATA[selectedTool]?.color?.replace('text-', 'border-').replace('-400', '-500/20') || 'border-gray-800'}`}>
                              {(() => {
                                const Icon = TOOL_METADATA[selectedTool]?.icon || Terminal;
                                return <Icon size={32} className={TOOL_METADATA[selectedTool]?.color || 'text-blue-400'} />;
                              })()}
                            </div>
                            <div>
                              <h2 className="text-2xl font-black text-white tracking-tighter uppercase">{TOOL_METADATA[selectedTool]?.label || selectedTool}</h2>
                              <p className="text-gray-500 text-sm font-medium leading-relaxed max-w-md mt-1">{TOOL_METADATA[selectedTool]?.description}</p>
                            </div>
                          </div>
                          <button
                            onClick={() => handleCallTool(selectedTool)}
                            disabled={loading[selectedTool]}
                            className="shrink-0 flex items-center gap-3 px-8 py-4 bg-blue-600 hover:bg-blue-500 disabled:bg-gray-800 text-white rounded-2xl transition-all shadow-2xl shadow-blue-600/20 active:scale-95 font-black uppercase tracking-widest text-[10px]"
                          >
                            {loading[selectedTool] ? <Loader2 size={18} className="animate-spin" /> : <Play size={18} />}
                            Invoke Command
                          </button>
                        </div>

                        {/* Config Inputs */}
                        {(() => {
                          const tool = tools.find(t => t.name === selectedTool);
                          if (!tool?.inputSchema?.properties) return null;
                          return (
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-4 p-6 bg-black/20 rounded-3xl border border-gray-800/50">
                              {Object.entries(tool.inputSchema.properties).map(([name, prop]: [string, any]) => (
                                <div key={name} className="space-y-2">
                                  <label className="text-[9px] font-black text-gray-600 uppercase tracking-widest px-1">
                                    {name} {tool.inputSchema.required?.includes(name) && <span className="text-red-500">*</span>}
                                  </label>
                                  <input
                                    type="text"
                                    placeholder={prop.description || "Enter value..."}
                                    className="w-full bg-black/40 border border-gray-800 rounded-xl px-4 py-3 text-xs focus:outline-none focus:border-blue-500/50 transition-all text-white placeholder:text-gray-800"
                                    onChange={(e) => setToolArgs(prev => ({
                                      ...prev,
                                      [selectedTool]: { ...prev[selectedTool], [name]: e.target.value }
                                    }))}
                                  />
                                </div>
                              ))}
                            </div>
                          );
                        })()}
                      </div>

                      {/* Log Output */}
                      <div className="flex-1 p-10 pt-0 min-h-0 flex flex-col">
                        <div className="flex-1 bg-black/60 rounded-[2.5rem] border border-gray-800/80 p-8 flex flex-col shadow-inner overflow-hidden">
                          <div className="flex items-center gap-2 mb-6">
                            <div className="w-1.5 h-1.5 rounded-full bg-blue-500 shadow-lg shadow-blue-500/50" />
                            <span className="text-[9px] font-black text-gray-700 uppercase tracking-widest">Process Terminal Output</span>
                          </div>
                          <div className="flex-1 overflow-auto custom-scrollbar font-mono text-[12px] leading-[1.7] text-blue-100/80">
                            {outputs[selectedTool] ? (
                              <div className="space-y-6">
                                {typeof outputs[selectedTool] === 'string' ? (
                                  <div className="whitespace-pre-wrap">{outputs[selectedTool]}</div>
                                ) : (
                                  <div className="space-y-4">
                                    {outputs[selectedTool].content?.map((c: any, i: number) => (
                                      <div key={i} className="whitespace-pre-wrap p-4 bg-blue-500/5 rounded-2xl border border-blue-500/10">{c.text}</div>
                                    ))}
                                    {outputs[selectedTool].error && (
                                      <div className="p-6 bg-red-950/20 border border-red-500/30 rounded-2xl text-red-400 flex items-start gap-4">
                                        <AlertCircle size={20} className="mt-1" />
                                        <div className="space-y-1">
                                           <div className="font-black uppercase text-[10px] tracking-widest">Execution Failure</div>
                                           <div className="font-medium text-xs opacity-80">{outputs[selectedTool].error}</div>
                                        </div>
                                      </div>
                                    )}
                                  </div>
                                )}
                              </div>
                            ) : (
                              <div className="h-full flex flex-col items-center justify-center opacity-10 space-y-4">
                                <Terminal size={48} />
                                <div className="text-[10px] font-black uppercase tracking-[0.4em]">Terminal Idle</div>
                              </div>
                            )}
                          </div>
                        </div>
                      </div>
                    </div>
                  ) : (
                    <div className="flex-1 flex flex-col items-center justify-center p-20 text-center opacity-20">
                      <Command size={64} className="mb-6" />
                      <p className="font-black text-xs uppercase tracking-[0.4em]">Select functional module</p>
                    </div>
                  )}
                </div>
              </div>
            )}
          </div>
        </main>

        {/* Footer info */}
        <footer className="shrink-0 px-12 py-6 flex items-center justify-between border-t border-gray-800/30 bg-black/20">
           <div className="flex items-center gap-8">
              <div className="flex items-center gap-2">
                 <div className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
                 <span className="text-[9px] font-black text-gray-600 uppercase tracking-widest">Backend: Active</span>
              </div>
              <div className="flex items-center gap-2 text-gray-700">
                 <Database size={10} />
                 <span className="text-[9px] font-bold uppercase tracking-widest">LanceDB /home/nilesh/.local/share/vector-mcp-go/lancedb</span>
              </div>
           </div>
           <div className="flex items-center gap-4 text-gray-700">
              <span className="text-[9px] font-bold">Node.js {process.version || 'v22.21.1'}</span>
              <ExternalLink size={10} className="opacity-20" />
           </div>
        </footer>
      </div>

      <style jsx global>{`
        @import url('https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@400;500;600;700;800&display=swap');
        
        body {
          font-family: 'Plus Jakarta Sans', sans-serif;
        }

        .custom-scrollbar::-webkit-scrollbar {
          width: 4px;
          height: 4px;
        }
        .custom-scrollbar::-webkit-scrollbar-track {
          background: transparent;
        }
        .custom-scrollbar::-webkit-scrollbar-thumb {
          background: rgba(255,255,255,0.03);
          border-radius: 10px;
        }
        .custom-scrollbar::-webkit-scrollbar-thumb:hover {
          background: rgba(255,255,255,0.08);
        }

        @keyframes fade-in { from { opacity: 0; } to { opacity: 1; } }
        @keyframes slide-in-from-bottom-4 { from { transform: translateY(1rem); opacity: 0; } to { transform: translateY(0); opacity: 1; } }
        @keyframes slide-in-from-top-4 { from { transform: translateY(-1rem); opacity: 0; } to { transform: translateY(0); opacity: 1; } }
        
        .animate-in {
          animation-duration: 500ms;
          animation-timing-function: cubic-bezier(0.16, 1, 0.3, 1);
          fill-mode: forwards;
        }
        .fade-in { animation-name: fade-in; }
        .slide-in-from-bottom-4 { animation-name: slide-in-from-bottom-4; }
        .slide-in-from-top-4 { animation-name: slide-in-from-top-4; }
      `}</style>
    </div>
  );
}
