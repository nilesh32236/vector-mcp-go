"use client";

import { useState, useEffect, Suspense } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import {
  Hammer,
  ChevronRight,
  Terminal,
  Search,
  Activity,
  ArrowLeft,
  Folder,
  Shield,
  Zap,
  Loader2,
} from "lucide-react";
import { listTools, Repo, getRepos } from "@/lib/api";
import Link from "next/link";

function ExplorerContent() {
  const [tools, setTools] = useState<any[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [repo, setRepo] = useState<Repo | null>(null);
  const searchParams = useSearchParams();
  const router = useRouter();
  const path = searchParams.get("path");

  useEffect(() => {
    if (!path) {
      router.push("/tools");
      return;
    }

    const fetchData = async () => {
      try {
        const [toolsData, repos] = await Promise.all([listTools(), getRepos()]);
        setTools(toolsData);
        const currentRepo = repos.find((r) => r.path === path);
        if (currentRepo) setRepo(currentRepo);
        else setRepo({ path, status: "Unknown" });
      } catch (e) {
        console.error("Failed to fetch explorer data:", e);
      } finally {
        setIsLoading(false);
      }
    };
    fetchData();
  }, [path, router]);

  if (isLoading) {
    return (
      <div className="h-full flex flex-col items-center justify-center gap-4">
        <Loader2 size={40} className="animate-spin text-brand-500" />
        <p className="text-xs font-bold uppercase tracking-[0.2em] text-gray-400">
          Loading Intelligence Engine...
        </p>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full bg-background animate-in fade-in duration-500">
      {/* Top Bar */}
      <div className="px-8 py-6 border-b border-(--border) bg-white dark:bg-gray-900 flex items-center justify-between sticky top-0 z-10 backdrop-blur-md">
        <div className="flex items-center gap-6">
          <Link
            href="/tools"
            className="p-2.5 bg-gray-50 dark:bg-gray-800 text-gray-400 hover:text-brand-500 rounded-xl transition-all hover:scale-105 active:scale-95"
          >
            <ArrowLeft size={20} />
          </Link>
          <div className="h-8 w-px bg-(--border)" />
          <div className="flex items-center gap-4">
            <div className="w-12 h-12 rounded-2xl bg-brand-600 flex items-center justify-center text-white shadow-lg shadow-brand-500/20">
              <Folder size={24} />
            </div>
            <div>
              <h1 className="text-xl font-extrabold tracking-tight">
                Repository Explorer
              </h1>
              <p className="text-xs text-gray-400 font-medium truncate max-w-xl mt-0.5">
                {path}
              </p>
            </div>
          </div>
        </div>

        <div className="flex items-center gap-3">
          <div className="px-3 py-1.5 rounded-full bg-emerald-500/10 border border-emerald-500/20 flex items-center gap-2">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-500" />
            <span className="text-[10px] font-bold uppercase tracking-widest text-emerald-600">
              {repo?.status || "Synced"}
            </span>
          </div>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto custom-scrollbar">
        <div className="max-w-6xl mx-auto p-12 space-y-16">
          {/* Metadata Grid */}
          <div className="grid grid-cols-1 md:grid-cols-3 gap-8">
            <div className="p-8 rounded-3xl bg-white dark:bg-gray-900 border border-(--border) shadow-sm hover:shadow-md transition-shadow group">
              <Shield
                size={24}
                className="text-brand-600 mb-6 group-hover:scale-110 transition-transform"
              />
              <h3 className="text-sm font-bold mb-2">
                Internal Engine Protected
              </h3>
              <p className="text-xs text-gray-500 dark:text-gray-400 leading-relaxed">
                This repository is fully indexed and monitored for real-time
                changes.
              </p>
            </div>
            <div className="p-8 rounded-3xl bg-white dark:bg-gray-900 border border-(--border) shadow-sm hover:shadow-md transition-shadow group">
              <Zap
                size={24}
                className="text-amber-500 mb-6 group-hover:scale-110 transition-transform"
              />
              <h3 className="text-sm font-bold mb-2">High Performance</h3>
              <p className="text-xs text-gray-500 dark:text-gray-400 leading-relaxed">
                Vector embeddings are optimized for sub-millisecond retrieval.
              </p>
            </div>
            <div className="p-8 rounded-3xl bg-white dark:bg-gray-900 border border-(--border) shadow-sm hover:shadow-md transition-shadow group">
              <Activity
                size={24}
                className="text-emerald-500 mb-6 group-hover:scale-110 transition-transform"
              />
              <h3 className="text-sm font-bold mb-2">Active Monitoring</h3>
              <p className="text-xs text-gray-500 dark:text-gray-400 leading-relaxed">
                Source health is verified periodically to ensure context
                accuracy.
              </p>
            </div>
          </div>

          {/* Tools Grid */}
          <section className="space-y-8">
            <div className="flex items-center justify-between border-b border-(--border) pb-6">
              <div>
                <h2 className="text-2xl font-black tracking-tight text-foreground">
                  Available Intelligence Tools
                </h2>
                <p className="text-sm text-gray-500 mt-1">
                  Select a tool to interact directly with the{" "}
                  {path?.split("/").pop()} codebase context.
                </p>
              </div>
              <div className="px-4 py-2 bg-gray-50 dark:bg-gray-800 rounded-2xl border border-(--border) flex items-center gap-2">
                <Hammer size={16} className="text-brand-500" />
                <span className="text-[10px] font-bold uppercase tracking-[0.2em]">
                  {tools.length} Tools Ready
                </span>
              </div>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
              {tools.map((tool) => (
                <Link
                  key={tool.name}
                  href={`/tools/execute?path=${encodeURIComponent(path || "")}&tool=${encodeURIComponent(tool.name)}`}
                  className="group p-8 bg-white dark:bg-gray-900 border border-(--border) rounded-3xl hover:border-brand-500/50 hover:shadow-xl hover:shadow-brand-500/5 transition-all flex flex-col justify-between"
                >
                  <div>
                    <div className="w-12 h-12 rounded-2xl bg-gray-50 dark:bg-gray-800 flex items-center justify-center mb-6 group-hover:bg-brand-600 group-hover:text-white transition-all shadow-sm">
                      {tool.name.includes("search") ? (
                        <Search size={20} />
                      ) : tool.name.includes("index") ? (
                        <Activity size={20} />
                      ) : (
                        <Terminal size={20} />
                      )}
                    </div>
                    <h3 className="text-lg font-bold group-hover:text-brand-600 transition-colors uppercase tracking-tight">
                      {tool.name.replace(/_/g, " ")}
                    </h3>
                    <p className="text-xs text-gray-500 dark:text-gray-400 mt-4 leading-relaxed line-clamp-3 font-medium italic">
                      {tool.description}
                    </p>
                  </div>
                  <div className="mt-8 flex items-center justify-between pt-6 border-t border-(--border)">
                    <span className="text-[10px] font-bold uppercase tracking-widest text-brand-600">
                      Configure & Execute
                    </span>
                    <ChevronRight
                      size={18}
                      className="text-gray-300 group-hover:text-brand-500 transition-transform group-hover:translate-x-1"
                    />
                  </div>
                </Link>
              ))}
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}

export default function RepoExplorer() {
  return (
    <Suspense
      fallback={
        <div className="h-full flex items-center justify-center">
          <Loader2 className="animate-spin text-brand-500" />
        </div>
      }
    >
      <ExplorerContent />
    </Suspense>
  );
}
