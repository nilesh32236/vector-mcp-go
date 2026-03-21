export default function Home() {
  return (
    <div className="flex flex-col items-center justify-center h-full">
      <div className="p-8 rounded-2xl bg-gray-900 border border-gray-800 shadow-2xl flex flex-col items-center text-center max-w-md">
        <div className="w-16 h-16 bg-blue-500/20 rounded-full flex items-center justify-center mb-6">
          <div className="w-8 h-8 rounded-full bg-blue-500 shadow-[0_0_15px_rgba(59,130,246,0.8)]" />
        </div>
        <h1 className="text-3xl font-bold bg-clip-text text-transparent bg-gradient-to-r from-blue-400 to-emerald-400 mb-4">
          Global Brain Ready
        </h1>
        <p className="text-gray-400 mb-6">
          I've indexed your codebase and documentation. What would you like to
          explore today?
        </p>
        <div className="text-sm text-gray-500">
          Try creating a new session to begin.
        </div>
      </div>
    </div>
  );
}
