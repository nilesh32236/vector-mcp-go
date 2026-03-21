"use client";

import { useState, useEffect } from "react";
import { Folder, HardDrive, Plus, Trash2, Shield, Hammer, Info, RefreshCw } from "lucide-react";
import { getRepos, addRepo, deleteRepo, Repo } from "@/lib/api";

export default function ToolsPage() {
  const [repos, setRepos] = useState<Repo[]>([]);
  const [newPath, setNewPath] = useState("");
  const [isLoading, setIsLoading] = useState(true);
  const [isAdding, setIsAdding] = useState(false);

  useEffect(() => {
    fetchRepos();
  }, []);

  const fetchRepos = async () => {
    try {
      setIsLoading(true);
      const data = await getRepos();
      setRepos(data);
    } catch (e) {
      console.error("Failed to fetch repos:", e);
    } finally {
      setIsLoading(false);
    }
  };

  const handleAdd = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newPath.trim()) return;
    setIsAdding(true);
    try {
      await addRepo(newPath);
      setNewPath("");
      await fetchRepos();
    } catch (e) {
      console.error("Failed to add repo:", e);
    } finally {
      setIsAdding(false);
    }
  };

  const handleDelete = async (path: string) => {
    try {
      await deleteRepo(path);
      await fetchRepos();
    } catch (e) {
      console.error("Failed to delete repo:", e);
    }
  };

  return (
    <div className="flex flex-col h-full bg-background p-8 md:p-12 overflow-y-auto custom-scrollbar">
      <div className="max-w-4xl w-full mx-auto space-y-12">
        {/* Header */}
        <div className="space-y-4">
          <div className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-brand-500/10 border border-brand-500/20">
            <Shield size={14} className="text-brand-600" />
            <span className="text-[10px] font-bold uppercase tracking-wider text-brand-700">Source Management</span>
          </div>
          <h1 className="text-4xl font-extrabold tracking-tight text-foreground">
            Knowledge Base Sources
          </h1>
          <p className="text-gray-500 font-medium max-w-2xl">
            Manage the local directories and repositories indexed by Vector MCP. These sources provide the context for your AI interactions.
          </p>
        </div>

        {/* Add Source Section */}
        <section className="bg-white dark:bg-gray-900 border border-(--border) rounded-3xl p-8 shadow-sm">
          <div className="flex items-center gap-3 mb-6">
            <div className="w-10 h-10 rounded-xl bg-brand-600 flex items-center justify-center text-white shadow-lg shadow-brand-500/20">
              <Plus size={20} />
            </div>
            <div>
              <h2 className="text-lg font-bold">Index New Source</h2>
              <p className="text-xs text-gray-400 font-medium uppercase tracking-widest mt-0.5">Local Directory Path</p>
            </div>
          </div>

          <form onSubmit={handleAdd} className="flex gap-4">
            <div className="flex-1 relative group">
              <input
                type="text"
                value={newPath}
                onChange={(e) => setNewPath(e.target.value)}
                placeholder="/path/to/your/codebase"
                className="w-full bg-gray-50 dark:bg-gray-800 border border-(--border) rounded-xl px-4 py-3 text-sm font-medium outline-none focus:border-brand-500/50 focus:ring-4 focus:ring-brand-500/5 transition-all"
                disabled={isAdding}
              />
            </div>
            <button
              type="submit"
              disabled={!newPath.trim() || isAdding}
              className="px-6 bg-brand-600 hover:bg-brand-700 disabled:bg-gray-200 dark:disabled:bg-gray-800 text-white rounded-xl font-bold transition-all shadow-lg shadow-brand-500/10 active:scale-95 flex items-center gap-2"
            >
              {isAdding ? <RefreshCw className="animate-spin" size={18} /> : <span>Index Source</span>}
            </button>
          </form>
          
          <div className="mt-6 p-4 bg-blue-500/5 border border-blue-500/10 rounded-2xl flex items-start gap-3">
            <Info size={18} className="text-blue-600 shrink-0 mt-0.5" />
            <p className="text-xs text-blue-700 dark:text-blue-400 font-medium leading-relaxed">
              Indexing large codebases may take a few minutes. Vector MCP will automatically watch these directories for changes.
            </p>
          </div>
        </section>

        {/* Sources List */}
        <section className="space-y-6">
          <div className="flex items-center justify-between px-2">
            <h2 className="text-xs text-gray-400 font-bold uppercase tracking-widest">
              Indexed Directories ({repos.length})
            </h2>
            <button 
              onClick={fetchRepos}
              className="p-1.5 text-gray-400 hover:text-brand-500 transition-colors"
              title="Refresh repository list"
            >
              <RefreshCw size={16} />
            </button>
          </div>

          <div className="grid grid-cols-1 gap-4">
            {isLoading ? (
              <div className="py-20 flex flex-col items-center gap-4">
                <RefreshCw size={32} className="animate-spin text-brand-500" />
                <p className="text-xs font-bold uppercase tracking-widest text-gray-400">Loading sources...</p>
              </div>
            ) : repos.map((repo, idx) => (
              <div 
                key={idx}
                className="group flex items-center justify-between p-5 bg-white dark:bg-gray-900 border border-(--border) rounded-2xl hover:shadow-md transition-all hover:border-brand-500/30"
              >
                <div className="flex items-center gap-4">
                  <div className="w-10 h-10 rounded-xl bg-gray-100 dark:bg-gray-800 flex items-center justify-center text-gray-500 group-hover:bg-brand-500/10 group-hover:text-brand-600 transition-colors">
                    <Folder size={20} />
                  </div>
                  <div>
                    <p className="text-sm font-bold text-foreground truncate max-w-lg">
                      {repo.path}
                    </p>
                    <div className="flex items-center gap-3 mt-1">
                      <span className="flex items-center gap-1 text-[10px] font-bold text-emerald-600 uppercase tracking-widest">
                        <span className="w-1 h-1 rounded-full bg-emerald-500" />
                        Synced
                      </span>
                      <span className="text-[10px] font-bold text-gray-400 uppercase tracking-widest">
                        Local File System
                      </span>
                    </div>
                  </div>
                </div>
                
                <button
                  onClick={() => handleDelete(repo.path)}
                  className="p-2 opacity-0 group-hover:opacity-100 bg-red-500/10 text-red-600 rounded-lg hover:bg-red-600 hover:text-white transition-all shadow-sm"
                  title="Remove Source"
                >
                  <Trash2 size={16} />
                </button>
              </div>
            ))}

            {!isLoading && repos.length === 0 && (
              <div className="py-20 flex flex-col items-center gap-4 border-2 border-dashed border-gray-100 dark:border-gray-800 rounded-3xl">
                <HardDrive size={48} className="text-gray-200" />
                <p className="text-sm font-medium text-gray-400">No indexed sources found</p>
              </div>
            )}
          </div>
        </section>

        {/* Footer */}
        <div className="pt-12 border-t border-(--border) text-center">
          <p className="text-[10px] text-gray-400 font-bold uppercase tracking-widest">
            Vector MCP System v0.1.0 • Built for Performance
          </p>
        </div>
      </div>
    </div>
  );
}
