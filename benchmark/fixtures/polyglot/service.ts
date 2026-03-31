export type User = {
  id: string;
  name: string;
};

export function findUser(users: User[], id: string): User | undefined {
  return users.find((u) => u.id === id);
}
