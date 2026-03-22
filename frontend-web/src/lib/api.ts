export const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_URL || "http://localhost:47821";

export interface Session {
  id: string;
  created_at: string;
  updated_at: string;
  history: Message[];
}

export interface Message {
  role: "user" | "assistant";
  content: string;
  created_at?: string;
  tool_calls?: any[];
  docs_used?: number;
  tool_count?: number;
}

export interface Repo {
  path: string;
  status: string;
}

/** Fetch all sessions */
export async function getSessions(): Promise<Session[]> {
  const res = await fetch(`${API_BASE_URL}/api/sessions`, {
    cache: "no-store",
  });
  if (!res.ok) throw new Error("Failed to fetch sessions");
  return res.json();
}

/** Fetch messages for a session (alias for getSession) */
export async function getSessionMessages(id: string): Promise<Message[]> {
  const session = await getSession(id);
  // Ensure we return the messages from history and handle naming discrepancies
  return session.history || (session as any).messages || [];
}

/** Create a new session */
export async function createSession(): Promise<Session> {
  const res = await fetch(`${API_BASE_URL}/api/sessions`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({}),
  });
  if (!res.ok) throw new Error("Failed to create session");
  return res.json();
}

/** Get a specific session */
export async function getSession(id: string): Promise<Session> {
  const res = await fetch(`${API_BASE_URL}/api/sessions/${id}`, {
    cache: "no-store",
  });
  if (!res.ok) throw new Error("Failed to fetch session");
  return res.json();
}

/** Delete a session */
export async function deleteSession(id: string): Promise<void> {
  const res = await fetch(`${API_BASE_URL}/api/sessions/${id}`, {
    method: "DELETE",
  });
  if (!res.ok) throw new Error("Failed to delete session");
}

/** Send a chat message (alias for sendChatMessage) */
export async function sendMessage(
  sessionId: string,
  message: string,
  model: string = "gemini-2.5-flash",
): Promise<Message> {
  const res = await sendChatMessage(sessionId, message, model);
  return {
    role: "assistant",
    content: res.content || res.response || "No response from assistant.",
    created_at: new Date().toISOString(),
    tool_count: res.tool_calls || 0,
    docs_used: res.docs_used || 0,
  };
}

/** Send a chat message */
export async function sendChatMessage(
  sessionId: string,
  message: string,
  model: string = "gemini-2.5-flash",
): Promise<{
  content?: string;
  response?: string;
  tool_calls: number;
  docs_used: number;
  error?: string;
}> {
  const res = await fetch(`${API_BASE_URL}/api/chat`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      session_id: sessionId,
      message,
      model,
    }),
  });

  if (!res.ok) {
    let errMessage = "Failed to send message";
    try {
      const errBody = await res.json();
      if (errBody.error) errMessage = errBody.error;
    } catch (e) {
      errMessage = res.statusText;
    }
    throw new Error(errMessage);
  }
  return res.json();
}

/** Tool Management Functions */

export async function getIndexStatus(): Promise<any> {
  const res = await fetch(`${API_BASE_URL}/api/tools/status`);
  if (!res.ok) throw new Error("Failed to fetch index status");
  return res.json();
}

export async function triggerIndex(path?: string): Promise<any> {
  const res = await fetch(`${API_BASE_URL}/api/tools/index`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ path }),
  });
  if (!res.ok) throw new Error("Failed to trigger indexing");
  return res.json();
}

export async function getSkeleton(path?: string): Promise<any> {
  const url = new URL(`${API_BASE_URL}/api/tools/skeleton`);
  if (path) url.searchParams.append("path", path);
  const res = await fetch(url.toString());
  if (!res.ok) throw new Error("Failed to fetch skeleton");
  return res.json();
}

export async function listTools(): Promise<any[]> {
  const res = await fetch(`${API_BASE_URL}/api/tools/list`);
  if (!res.ok) throw new Error("Failed to list tools");
  return res.json();
}

export async function callTool(name: string, args: any): Promise<any> {
  const res = await fetch(`${API_BASE_URL}/api/tools/call`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, arguments: args }),
  });
  if (!res.ok) throw new Error(`Failed to call tool: ${name}`);
  return res.json();
}

/** Source Management */

export async function getRepos(): Promise<Repo[]> {
  const res = await fetch(`${API_BASE_URL}/api/tools/repos`);
  if (!res.ok) throw new Error("Failed to fetch repos");
  return res.json();
}

export async function addRepo(path: string): Promise<any> {
  return triggerIndex(path);
}

export async function deleteRepo(path: string): Promise<any> {
  // Use callTool to delete the context of a project
  return callTool("delete_context", { target_path: "ALL", project_id: path });
}
