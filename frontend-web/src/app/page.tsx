import { LayoutDashboard, MessageSquare, Plus, Wrench, Shield, Zap } from "lucide-react";
import Link from "next/link";

export default function Home() {
  return (
    <div className="flex flex-col items-center justify-center min-h-full px-6 py-20 bg-dot-grid">
      <div className="max-w-4xl w-full">
        {/* Hero Section */}
        <div className="text-center mb-16 space-y-6">
          <div className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-brand-500/10 border border-brand-500/20 mb-4">
            <Zap size={14} className="text-brand-600" />
            <span className="text-[10px] font-bold uppercase tracking-wider text-brand-700">Internal Engine Ready</span>
          </div>
          
          <h1 className="text-5xl font-extrabold tracking-tight text-foreground sm:text-6xl">
            Accelerate your <span className="text-brand-600">Codebase</span> <br />
            Intelligence.
          </h1>
          
          <p className="max-w-xl mx-auto text-lg text-gray-500 font-medium">
            Vector MCP provides deep insights into your local repositories and documentation using advanced AI. Start a session to explore.
          </p>

          <div className="flex items-center justify-center gap-4 pt-4">
            <button 
              className="px-8 py-3 bg-brand-600 hover:bg-brand-700 text-white rounded-xl font-bold shadow-xl shadow-brand-500/20 transition-all hover:scale-[1.02]"
            >
              Get Started
            </button>
            <Link 
              href="/tools" 
              className="px-8 py-3 bg-white dark:bg-gray-800 border border-(--border) text-foreground rounded-xl font-bold shadow-sm hover:bg-gray-50 dark:hover:bg-gray-700 transition-all"
            >
              Manage Sources
            </Link>
          </div>
        </div>

        {/* Features Grid - Focused on Internal Utility */}
        <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
          <div className="p-8 rounded-3xl bg-white dark:bg-gray-900 border border-(--border) shadow-sm hover:shadow-md transition-shadow group">
            <div className="w-12 h-12 rounded-2xl bg-blue-500/10 flex items-center justify-center mb-6 group-hover:scale-110 transition-transform">
              <MessageSquare size={24} className="text-blue-600" />
            </div>
            <h3 className="text-lg font-bold mb-2">Deep Chat</h3>
            <p className="text-sm text-gray-500 leading-relaxed font-medium">
              Interact with your entire codebase through natural language. Ask questions about architecture and logic.
            </p>
          </div>

          <div className="p-8 rounded-3xl bg-white dark:bg-gray-900 border border-(--border) shadow-sm hover:shadow-md transition-shadow group">
            <div className="w-12 h-12 rounded-2xl bg-emerald-500/10 flex items-center justify-center mb-6 group-hover:scale-110 transition-transform">
              <Shield size={24} className="text-emerald-600" />
            </div>
            <h3 className="text-lg font-bold mb-2">Internal & Secure</h3>
            <p className="text-sm text-gray-500 leading-relaxed font-medium">
              Designed specifically for internal team usage. All processing respects your local environment and privacy.
            </p>
          </div>

          <div className="p-8 rounded-3xl bg-white dark:bg-gray-900 border border-(--border) shadow-sm hover:shadow-md transition-shadow group">
            <div className="w-12 h-12 rounded-2xl bg-purple-500/10 flex items-center justify-center mb-6 group-hover:scale-110 transition-transform">
              <Wrench size={24} className="text-purple-600" />
            </div>
            <h3 className="text-lg font-bold mb-2">Tool Integration</h3>
            <p className="text-sm text-gray-500 leading-relaxed font-medium">
              Seamlessly integrates with your existing MCP servers and development workflows.
            </p>
          </div>
        </div>

        <div className="mt-16 text-center border-t border-(--border) pt-8">
          <p className="text-xs text-gray-400 font-bold uppercase tracking-widest">
            Vector MCP Open Source v0.1.0
          </p>
        </div>
      </div>
    </div>
  );
}
