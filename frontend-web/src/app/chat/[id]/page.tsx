import ChatClient from "./ChatClient";

type Props = {
  params: Promise<{ id: string }>;
};

export default async function ChatPage({ params }: Props) {
  const resolvedParams = await params;
  return <ChatClient sessionId={resolvedParams.id} />;
}
