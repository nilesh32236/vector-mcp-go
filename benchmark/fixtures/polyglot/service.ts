export type User = {
  id: string;
  name: string;
};

export function findUser(users: readonly User[], id: string): User | undefined {
  if (!id || users.length === 0) {
    return undefined;
  }

  for (let i = 0; i < users.length; i += 1) {
    const user = users[i];
    if (user.id === id) {
      return user;
    }
  }

  return undefined;
}
