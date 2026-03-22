"use client";

import { useState } from "react";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { vscDarkPlus } from "react-syntax-highlighter/dist/esm/styles/prism";
import {
  FileCode,
  ChevronRight,
  Terminal,
  Copy,
  Check,
  ExternalLink,
  Cpu,
  Bookmark,
  Activity,
} from "lucide-react";

interface CodeMetadata {
  file?: string;
  entity?: string;
  type?: string;
  calls?: string;
  score?: string;
  functionality?: string;
}

interface CodeViewerProps {
  code: string;
  language?: string;
}

export default function CodeViewer({
  code,
  language = "typescript",
}: CodeViewerProps) {
  const [copied, setCopied] = useState(false);

  // Parser for MCP metadata tags
  const parseMetadata = (
    content: string,
  ): { meta: CodeMetadata; cleanCode: string } => {
    const meta: CodeMetadata = {};
    const lines = content.split("\n");
    let metaEndIndex = 0;

    for (let i = 0; i < lines.length; i++) {
      const line = lines[i].trim();

      // If the line is empty, just skip it but keep track of it as part of metadata area
      if (line === "") {
        metaEndIndex = i + 1;
        continue;
      }

      // If the line starts with //, it's metadata
      if (line.startsWith("//")) {
        metaEndIndex = i + 1;

        // Split by // in case there are multiple tags on one line
        const parts = line.split("//").filter((p) => p.trim() !== "");
        parts.forEach((part) => {
          const colonIndex = part.indexOf(":");
          if (colonIndex !== -1) {
            const key = part.substring(0, colonIndex).trim().toLowerCase();
            const value = part.substring(colonIndex + 1).trim();

            if (key === "file") meta.file = value;
            else if (key === "entity") meta.entity = value;
            else if (key === "type") meta.type = value;
            else if (key === "calls") meta.calls = value;
            else if (key === "score") meta.score = value;
            else if (key === "functionality") meta.functionality = value;
          }
        });
      } else {
        // Hit actual code
        break;
      }
    }

    const cleanCode = lines.slice(metaEndIndex).join("\n").trim();
    return { meta, cleanCode };
  };

  const { meta, cleanCode } = parseMetadata(code);

  const handleCopy = () => {
    navigator.clipboard.writeText(cleanCode);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="group rounded-2xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-[#0d1117] overflow-hidden shadow-xl transition-all hover:shadow-2xl">
      {/* IDE Header/Tab */}
      <div className="bg-slate-50 dark:bg-[#161b22] border-b border-slate-200 dark:border-slate-800 px-5 py-3 flex flex-col gap-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3 overflow-hidden">
            <div className="w-8 h-8 rounded-lg bg-slate-200 dark:bg-slate-800 text-slate-500 dark:text-slate-400 flex items-center justify-center shrink-0 border border-slate-300 dark:border-slate-700">
              <FileCode size={16} />
            </div>
            <div className="overflow-hidden">
              <div className="flex items-center gap-2 text-slate-400 dark:text-slate-500 text-[9px] uppercase font-bold tracking-widest mb-0.5">
                <span>Repository</span>
                <ChevronRight size={8} />
                <span>Source</span>
              </div>
              <h4 className="text-xs font-bold text-slate-700 dark:text-slate-300 truncate pr-4">
                {meta.file || "snippet.ts"}
              </h4>
            </div>
          </div>

          <div className="flex items-center gap-2 shrink-0">
            {meta.score && (
              <div className="px-2.5 py-1 rounded-md bg-amber-500/5 border border-amber-500/10 flex items-center gap-1.5">
                <Activity size={10} className="text-amber-500" />
                <span className="text-[9px] font-bold text-amber-600 dark:text-amber-500/80">
                  MATCH: {meta.score}
                </span>
              </div>
            )}
            <div className="h-6 w-px bg-slate-200 dark:bg-slate-800 mx-1" />
            <button
              onClick={handleCopy}
              className="p-2 hover:bg-slate-200 dark:hover:bg-slate-800 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 rounded-lg transition-all"
              title="Copy code"
            >
              {copied ? (
                <Check size={14} className="text-emerald-500" />
              ) : (
                <Copy size={14} />
              )}
            </button>
            <button
              className="p-2 hover:bg-slate-200 dark:hover:bg-slate-800 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 rounded-lg transition-all"
              title="Open in editor"
            >
              <ExternalLink size={14} />
            </button>
          </div>
        </div>

        {/* Semantic Metadata Tags */}
        {(meta.entity ||
          meta.type ||
          (meta.functionality && meta.functionality.trim() !== "")) && (
          <div className="flex flex-wrap gap-1.5 pt-0.5">
            {meta.entity && (
              <div className="px-2 py-0.5 rounded-md bg-slate-100 dark:bg-slate-800/50 border border-slate-200 dark:border-slate-700/50 flex items-center gap-1.5">
                <Cpu size={10} className="text-slate-400" />
                <span className="text-[8px] font-bold text-slate-500 dark:text-slate-400 uppercase tracking-tight">
                  {meta.type || "Entity"}: {meta.entity}
                </span>
              </div>
            )}
            {meta.functionality && meta.functionality.trim() !== "" && (
              <div className="px-2 py-0.5 rounded-md bg-brand-500/5 dark:bg-brand-500/10 border border-brand-500/10 dark:border-brand-500/20 flex items-center gap-1.5">
                <Bookmark size={10} className="text-brand-500" />
                <span className="text-[8px] font-bold text-brand-600 dark:text-brand-400 uppercase tracking-tight">
                  Purpose: {meta.functionality}
                </span>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Code Area */}
      <div className="relative">
        <div className="absolute top-3 right-5 pointer-events-none z-10 opacity-20 group-hover:opacity-40 transition-opacity">
          <span className="text-[9px] font-bold uppercase tracking-widest text-slate-500 dark:text-slate-400">
            {language}
          </span>
        </div>

        <div className="max-h-150 overflow-auto custom-scrollbar bg-[#0d1117] font-mono border-t border-slate-200 dark:border-transparent">
          <SyntaxHighlighter
            language={language}
            style={vscDarkPlus}
            customStyle={{
              margin: 0,
              padding: "1.5rem",
              backgroundColor: "transparent",
              fontSize: "12px",
              lineHeight: "1.6",
              fontFamily: '"JetBrains Mono", "Fira Code", monospace',
            }}
            wrapLines={true}
            wrapLongLines={true}
            showLineNumbers={true}
            lineNumberStyle={{
              minWidth: "3em",
              paddingRight: "1rem",
              color: "#4b5563",
              textAlign: "right",
              opacity: 0.3,
            }}
          >
            {cleanCode || code}
          </SyntaxHighlighter>
        </div>
      </div>

      {/* Code Footer / Dependencies */}
      {meta.calls && (
        <div className="bg-slate-50 dark:bg-[#161b22] border-t border-slate-200 dark:border-slate-800 px-5 py-2.5 flex items-center gap-3 overflow-hidden">
          <Terminal size={12} className="text-slate-400 shrink-0" />
          <p className="text-[9px] text-slate-500 dark:text-slate-500 font-medium truncate uppercase tracking-tight">
            <span className="font-bold text-slate-400 mr-2">DEPENDENCIES:</span>
            {meta.calls}
          </p>
        </div>
      )}
    </div>
  );
}
