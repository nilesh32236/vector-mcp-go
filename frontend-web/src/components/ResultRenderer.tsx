"use client";

import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  CheckCircle2,
  AlertCircle,
  Info,
  FileText,
  Image as ImageIcon,
  Box,
} from "lucide-react";
import CodeViewer from "./CodeViewer";

interface ContentItem {
  type: string;
  text?: string;
  data?: string;
  mimeType?: string;
}

interface CallToolResult {
  content: ContentItem[];
  isError?: boolean;
  error?: string;
}

interface ResultRendererProps {
  result: any;
  toolName?: string;
}

export default function ResultRenderer({
  result,
  toolName,
}: ResultRendererProps) {
  if (!result) return null;

  // Handle various formats (raw string, error object, or standard MCP result)
  const isError = result.isError || !!result.error;
  const errorMsg =
    result.error ||
    (isError ? "An error occurred during tool execution." : null);

  // Normalize content to an array of ContentItem
  let content: ContentItem[] = [];
  if (result.content && Array.isArray(result.content)) {
    content = result.content;
  } else if (typeof result === "string") {
    content = [{ type: "text", text: result }];
  } else if (result.Text) {
    // Support for simple text results
    content = [{ type: "text", text: result.Text }];
  } else if (!isError) {
    // Fallback for serialized objects
    content = [
      {
        type: "text",
        text: "```json\n" + JSON.stringify(result, null, 2) + "\n```",
      },
    ];
  }

  return (
    <div className="space-y-6 animate-in fade-in slide-in-from-bottom-2 duration-300">
      {/* Status Banner */}
      <div
        className={`flex items-center gap-3 p-4 rounded-2xl border ${
          isError
            ? "bg-red-500/5 border-red-500/10 text-red-600"
            : "bg-emerald-500/5 border-emerald-500/10 text-emerald-600"
        }`}
      >
        {isError ? <AlertCircle size={20} /> : <CheckCircle2 size={20} />}
        <div className="flex-1">
          <p className="text-sm font-bold">
            {isError ? "Tool Execution Failed" : "Tool Execution Successful"}
          </p>
          {toolName && (
            <p className="text-[10px] font-medium uppercase tracking-widest opacity-70 mt-0.5">
              Source: {toolName}
            </p>
          )}
        </div>
      </div>

      {/* Error Message if any */}
      {isError && errorMsg && (
        <div className="p-6 bg-red-50 dark:bg-red-950/20 border border-red-100 dark:border-red-900/30 rounded-3xl text-red-700 dark:text-red-400 font-mono text-xs whitespace-pre-wrap leading-relaxed shadow-sm">
          {errorMsg}
        </div>
      )}

      {/* Content Rendering */}
      <div className="space-y-8">
        {content.map((item, idx) => {
          if (item.type === "text" && item.text) {
            // Detect if this is a "raw" code chunk with metadata but no backticks
            const isRawCode = item.text.trim().startsWith("// File:");

            if (isRawCode) {
              return (
                <div key={idx} className="space-y-4">
                  <CodeViewer code={item.text} language="typescript" />
                </div>
              );
            }

            const isDocument =
              item.text.includes("**Category**: document") ||
              item.text.includes("**Category**: doc") ||
              item.text.includes(".md") ||
              item.text.includes(".txt");

            // For documents, we "unwrap" the backticks so ReactMarkdown renders it as normal markdown
            let processedText = item.text;
            if (isDocument) {
              processedText = item.text.replace(
                /```[\s\S]*?\n([\s\S]*?)\n```/g,
                (match, p1) => p1,
              );
            }

            return (
              <div
                key={idx}
                className="prose prose-sm dark:prose-invert max-w-none bg-white dark:bg-slate-900/50 p-8 rounded-2xl border border-slate-200 dark:border-slate-800 shadow-sm transition-all hover:shadow-md"
              >
                <ReactMarkdown
                  remarkPlugins={[remarkGfm]}
                  components={{
                    h1: ({ children }) => {
                      return (
                        <h1 className="text-slate-900 dark:text-slate-100 font-black tracking-tight border-b border-slate-200 dark:border-slate-800 pb-4 mb-8">
                          {children}
                        </h1>
                      );
                    },
                    h2: ({ children }) => {
                      const text = String(children);
                      if (text.startsWith("Result ") || text.includes(": ")) {
                        return (
                          <div className="flex items-center gap-3 mt-12 mb-6 p-4 rounded-xl bg-slate-50 dark:bg-slate-800/50 border border-slate-200 dark:border-slate-700/50 group/result">
                            <div className="w-8 h-8 rounded-lg bg-slate-200 dark:bg-slate-700 flex items-center justify-center text-slate-500 dark:text-slate-400 group-hover/result:bg-brand-500/10 group-hover/result:text-brand-500 transition-colors">
                              <FileText size={16} />
                            </div>
                            <div className="flex flex-col">
                              <span className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-widest leading-none mb-1">
                                Search Result
                              </span>
                              <span className="text-sm font-bold text-slate-800 dark:text-slate-200">
                                {children}
                              </span>
                            </div>
                          </div>
                        );
                      }
                      return (
                        <h2 className="text-slate-800 dark:text-slate-200 font-bold tracking-tight border-b border-slate-200 dark:border-slate-800 pb-2 mb-6">
                          {children}
                        </h2>
                      );
                    },
                    h3: ({ children }) => {
                      return (
                        <h3 className="text-slate-800 dark:text-slate-200 font-bold mt-8 mb-4">
                          {children}
                        </h3>
                      );
                    },
                    h4: ({ children }) => {
                      const text = String(children);
                      if (text.startsWith("Result ") || text.includes(": ")) {
                        return (
                          <div className="flex items-center gap-3 mt-10 mb-4 p-3 rounded-lg bg-slate-50 dark:bg-slate-800/50 border border-slate-200 dark:border-slate-700/50">
                            <Box size={14} className="text-slate-400" />
                            <span className="text-[11px] font-bold text-slate-700 dark:text-slate-300">
                              {children}
                            </span>
                          </div>
                        );
                      }
                      return (
                        <h4 className="text-slate-700 dark:text-slate-300 font-bold mt-6 mb-3">
                          {children}
                        </h4>
                      );
                    },
                    table: ({ children }) => (
                      <div className="my-6 overflow-x-auto rounded-xl border border-slate-200 dark:border-slate-800">
                        <table className="min-w-full divide-y divide-slate-200 dark:divide-slate-800">
                          {children}
                        </table>
                      </div>
                    ),
                    thead: ({ children }) => (
                      <thead className="bg-slate-50 dark:bg-slate-800/50">
                        {children}
                      </thead>
                    ),
                    th: ({ children }) => (
                      <th className="px-4 py-3 text-left text-[10px] font-bold uppercase tracking-wider text-slate-400">
                        {children}
                      </th>
                    ),
                    td: ({ children }) => (
                      <td className="px-4 py-3 text-xs border-t border-slate-200 dark:border-slate-800">
                        {children}
                      </td>
                    ),
                    code: (props) => {
                      const { children, className, node, ...rest } = props;
                      const match = /language-(\w+)/.exec(className || "");
                      const language = match ? match[1] : "typescript";
                      const content = String(children).replace(/\n$/, "");

                      return (
                        <div className="my-8 rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-[#0d1117] overflow-hidden shadow-sm shadow-slate-200/50 dark:shadow-none transition-all hover:shadow-md">
                          <div className="bg-slate-50 dark:bg-[#161b22] border-b border-slate-200 dark:border-slate-800 px-4 py-2 flex items-center justify-between">
                            <div className="flex items-center gap-2">
                              <Box size={12} className="text-slate-400" />
                              <span className="text-[10px] font-bold uppercase tracking-widest text-slate-500 dark:text-slate-400">
                                {language}
                              </span>
                            </div>
                          </div>
                          <div className="max-h-125 overflow-auto custom-scrollbar">
                            <CodeViewer code={content} language={language} />
                          </div>
                        </div>
                      );
                    },
                    pre: ({ children }) => (
                      <div className="my-8">{children}</div>
                    ),
                  }}
                >
                  {processedText}
                </ReactMarkdown>
              </div>
            );
          }

          if (item.type === "image" && item.data) {
            return (
              <div
                key={idx}
                className="bg-white dark:bg-gray-900/50 p-4 rounded-3xl border border-(--border) shadow-sm overflow-hidden"
              >
                <div className="flex items-center gap-2 mb-4 px-4 py-2 bg-gray-50 dark:bg-gray-800 rounded-xl w-fit">
                  <ImageIcon size={14} className="text-brand-500" />
                  <span className="text-[10px] font-bold uppercase tracking-widest text-gray-500">
                    Attachment: {item.mimeType || "Image"}
                  </span>
                </div>
                <img
                  src={`data:${item.mimeType || "image/png"};base64,${item.data}`}
                  alt="Tool Output Image"
                  className="w-full h-auto rounded-2xl shadow-xl border border-(--border)"
                />
              </div>
            );
          }

          return (
            <div
              key={idx}
              className="flex items-start gap-3 p-6 bg-gray-50 dark:bg-gray-800/50 rounded-2xl border border-dashed border-(--border)"
            >
              <Box size={20} className="text-gray-400 shrink-0 mt-1" />
              <div>
                <p className="text-sm font-bold text-gray-400 uppercase tracking-widest leading-none">
                  Unknown Content Type: {item.type}
                </p>
                <pre className="mt-4 text-[11px] font-mono text-gray-500 overflow-auto">
                  {JSON.stringify(item, null, 2)}
                </pre>
              </div>
            </div>
          );
        })}
      </div>

      {/* Footer Info */}
      <div className="flex items-center gap-2 px-4 py-3 bg-blue-500/5 border border-blue-500/10 rounded-2xl">
        <Info size={14} className="text-blue-600" />
        <p className="text-[10px] font-medium text-blue-700 dark:text-blue-400 uppercase tracking-wider">
          This result was generated by the internal vector engine via{" "}
          {toolName || "an automated tool"}.
        </p>
      </div>
    </div>
  );
}
