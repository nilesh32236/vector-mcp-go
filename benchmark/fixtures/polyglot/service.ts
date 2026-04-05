export type User = {
  id: string;
  name: string;
};

/**
 * Finds the first user whose `id` strictly equals the provided `id`.
 *
 * @param users - Array of users to search
 * @param id - Target user `id` to match
 * @returns The matching `User`, or `undefined` if none is found
 */
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
