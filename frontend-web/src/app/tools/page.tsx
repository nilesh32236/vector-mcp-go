"use client";

import { useState, useEffect } from "react";
import {
  Folder,
  HardDrive,
  Plus,
  Trash2,
  Shield,
  Hammer,
  Info,
  RefreshCw,
} from "lucide-react";
import {
  getRepos,
  addRepo,
  deleteRepo,
  Repo,
  triggerIndex,
  getSkeleton,
} from "@/lib/api";
import { useRouter, useSearchParams } from "next/navigation";
import Link from "next/link";
import { Suspense } from "react";

function ToolsContent() {
  const [repos, setRepos] = useState<Repo[]>([]);
  const [newPath, setNewPath] = useState("");
  const [isLoading, setIsLoading] = useState(true);
  const [isAdding, setIsAdding] = useState(false);
  const [skeleton, setSkeleton] = useState<{
    path: string;
    content: string;
  } | null>(null);
  const router = useRouter();
  const searchParams = useSearchParams();

  useEffect(() => {
    fetchRepos();

    // Polling logic for indexing status
    const interval = setInterval(() => {
      const isIndexing = repos.some(
        (r) =>
          r.status.includes("Indexing") ||
          r.status.includes("Initializing") ||
          r.status.includes("🔄") ||
          r.status.includes("Scanning"),
      );

      if (isIndexing || repos.length === 0) {
        fetchRepos();
      }
    }, 2000);

    return () => clearInterval(interval);
  }, [repos.length, repos.some((r) => r.status.includes("Indexing"))]);

  const fetchRepos = async () => {
    try {
      // Only show spinner on initial load or if empty
      if (repos.length === 0) setIsLoading(true);
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
    if (
      !confirm("Are you sure you want to remove this project from the index?")
    )
      return;
    try {
      await deleteRepo(path);
      await fetchRepos();
    } catch (e) {
      console.error("Failed to delete repo:", e);
    }
  };

  const handleReindex = async (path: string) => {
    try {
      await triggerIndex(path);
      await fetchRepos();
    } catch (e) {
      console.error("Failed to trigger re-index:", e);
    }
  };

  const handleReindexAll = async () => {
    try {
      await Promise.all(repos.map((r) => triggerIndex(r.path)));
      await fetchRepos();
    } catch (e) {
      console.error("Failed to re-index all:", e);
    }
  };

  const handleViewSkeleton = async (path: string) => {
    try {
      const data = await getSkeleton(path);
      const content =
        typeof data === "string" ? data : JSON.stringify(data, null, 2);
      setSkeleton({ path, content });
    } catch (e) {
      console.error("Failed to fetch skeleton:", e);
      alert(
        "Failed to fetch codebase skeleton. Ensure the path is correct and accessible.",
      );
    }
  };

  return (
    <div className="flex flex-col h-full bg-background p-8 md:p-12 overflow-y-auto custom-scrollbar">
      <div className="max-w-4xl w-full mx-auto space-y-12">
        {/* Header */}
        <div className="space-y-4">
          <div className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-brand-500/10 border border-brand-500/20">
            <Shield size={14} className="text-brand-600" />
            <span className="text-[10px] font-bold uppercase tracking-wider text-brand-700">
              Source Management
            </span>
          </div>
          <h1 className="text-4xl font-extrabold tracking-tight text-foreground">
            Knowledge Base Sources
          </h1>
          <p className="text-gray-500 font-medium max-w-2xl">
            Manage the local directories and repositories indexed by Vector MCP.
            These sources provide the context for your AI interactions.
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
              <p className="text-xs text-gray-400 font-medium uppercase tracking-widest mt-0.5">
                Local Directory Path
              </p>
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
              {isAdding ? (
                <RefreshCw className="animate-spin" size={18} />
              ) : (
                <span>Index Source</span>
              )}
            </button>
          </form>

          <div className="mt-6 p-4 bg-blue-500/5 border border-blue-500/10 rounded-2xl flex items-start gap-3">
            <Info size={18} className="text-blue-600 shrink-0 mt-0.5" />
            <p className="text-xs text-blue-700 dark:text-blue-400 font-medium leading-relaxed">
              Indexing large codebases may take a few minutes. Vector MCP will
              automatically watch these directories for changes.
            </p>
          </div>
        </section>

        {/* Sources List */}
        <section className="space-y-6">
          <div className="flex items-center justify-between px-2">
            <h2 className="text-xs text-gray-400 font-bold uppercase tracking-widest">
              Indexed Directories ({repos.length})
            </h2>
            <div className="flex items-center gap-2">
              <button
                onClick={handleReindexAll}
                disabled={repos.length === 0}
                className="flex items-center gap-1.5 px-3 py-1.5 text-[10px] font-bold uppercase tracking-widest text-brand-600 hover:bg-brand-500/10 rounded-lg transition-colors disabled:opacity-50"
                title="Re-index all repositories"
              >
                <RefreshCw size={14} />
                Re-index All
              </button>
              <button
                onClick={fetchRepos}
                className="p-1.5 text-gray-400 hover:text-brand-500 transition-colors"
                title="Refresh repository list"
              >
                <RefreshCw size={16} />
              </button>
            </div>
          </div>

          <div className="grid grid-cols-1 gap-4">
            {isLoading ? (
              <div className="py-20 flex flex-col items-center gap-4">
                <RefreshCw size={32} className="animate-spin text-brand-500" />
                <p className="text-xs font-bold uppercase tracking-widest text-gray-400">
                  Loading sources...
                </p>
              </div>
            ) : (
              repos.map((repo, idx) => (
                <div
                  key={idx}
                  className="group flex items-center justify-between p-5 bg-white dark:bg-gray-900 border border-(--border) rounded-2xl hover:shadow-md transition-all hover:border-brand-500/30 cursor-pointer"
                  onClick={() =>
                    router.push(
                      `/tools/explorer?path=${encodeURIComponent(repo.path)}`,
                    )
                  }
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
                        {repo.status.includes("Indexing") ||
                        repo.status.includes("Initializing") ||
                        repo.status.includes("Scanning") ? (
                          <span className="flex items-center gap-1.5 text-[10px] font-bold text-brand-600 uppercase tracking-widest animate-pulse">
                            <RefreshCw size={10} className="animate-spin" />
                            {repo.status}
                          </span>
                        ) : repo.status.includes("Failed") ||
                          repo.status.includes("Error") ? (
                          <span className="flex items-center gap-1 text-[10px] font-bold text-red-500 uppercase tracking-widest">
                            <span className="w-1 h-1 rounded-full bg-red-500" />
                            {repo.status}
                          </span>
                        ) : (
                          <span className="flex items-center gap-1 text-[10px] font-bold text-emerald-600 uppercase tracking-widest">
                            <span className="w-1 h-1 rounded-full bg-emerald-500" />
                            {repo.status || "Synced"}
                          </span>
                        )}
                        <span className="text-[10px] font-bold text-gray-400 uppercase tracking-widest">
                          Local File System
                        </span>
                      </div>
                    </div>
                  </div>

                  <div
                    className="flex items-center gap-2"
                    onClick={(e) => e.stopPropagation()}
                  >
                    <button
                      onClick={() => handleViewSkeleton(repo.path)}
                      className="p-2 opacity-0 group-hover:opacity-100 bg-emerald-500/10 text-emerald-600 rounded-lg hover:bg-emerald-600 hover:text-white transition-all shadow-sm"
                      title="View Codebase Skeleton"
                    >
                      <Hammer size={16} />
                    </button>
                    <button
                      onClick={() => handleReindex(repo.path)}
                      className="p-2 opacity-0 group-hover:opacity-100 bg-brand-500/10 text-brand-600 rounded-lg hover:bg-brand-600 hover:text-white transition-all shadow-sm"
                      title="Re-index Source"
                    >
                      <RefreshCw size={16} />
                    </button>
                    <button
                      onClick={() => handleDelete(repo.path)}
                      className="p-2 opacity-0 group-hover:opacity-100 bg-red-500/10 text-red-600 rounded-lg hover:bg-red-600 hover:text-white transition-all shadow-sm"
                      title="Remove Source"
                    >
                      <Trash2 size={16} />
                    </button>
                  </div>
                </div>
              ))
            )}

            {!isLoading && repos.length === 0 && (
              <div className="py-20 flex flex-col items-center gap-4 border-2 border-dashed border-gray-100 dark:border-gray-800 rounded-3xl">
                <HardDrive size={48} className="text-gray-200" />
                <p className="text-sm font-medium text-gray-400">
                  No indexed sources found
                </p>
              </div>
            )}
          </div>
        </section>

        {/* Skeleton Modal */}
        {skeleton && (
          <div className="fixed inset-0 bg-black/50 backdrop-blur-sm z-50 flex items-center justify-center p-4 md:p-12 animate-in fade-in duration-200">
            <div className="bg-white dark:bg-gray-900 border border-(--border) rounded-3xl w-full max-w-4xl max-h-[85vh] flex flex-col shadow-2xl animate-in zoom-in-95 duration-200">
              <div className="p-6 border-b border-(--border) flex items-center justify-between">
                <div className="flex items-center gap-3">
                  <div className="w-10 h-10 rounded-xl bg-emerald-500/10 flex items-center justify-center text-emerald-600">
                    <Hammer size={20} />
                  </div>
                  <div>
                    <h3 className="text-lg font-bold">Codebase Skeleton</h3>
                    <p className="text-xs text-gray-400 font-medium truncate max-w-sm">
                      {skeleton.path}
                    </p>
                  </div>
                </div>
                <button
                  onClick={() => setSkeleton(null)}
                  className="p-2 bg-gray-100 dark:bg-gray-800 text-gray-500 rounded-xl hover:bg-gray-200 dark:hover:bg-gray-700 transition-colors"
                >
                  <Plus size={20} className="rotate-45" />
                </button>
              </div>
              <div className="flex-1 overflow-auto p-6 md:p-8 custom-scrollbar">
                <pre className="text-xs font-mono text-gray-700 dark:text-gray-300 bg-gray-50 dark:bg-black/50 p-6 rounded-2xl border border-(--border) whitespace-pre-wrap leading-relaxed">
                  {skeleton.content}
                </pre>
              </div>
            </div>
          </div>
        )}

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

export default function ToolsPage() {
  return (
    <Suspense
      fallback={
        <div className="h-full flex items-center justify-center">
          <RefreshCw className="animate-spin text-brand-500" size={32} />
        </div>
      }
    >
      <ToolsContent />
    </Suspense>
  );
}
