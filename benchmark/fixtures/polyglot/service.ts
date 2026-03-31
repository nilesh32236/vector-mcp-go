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
export function findUser(users: User[], id: string): User | undefined {
  return users.find((u) => u.id === id);
}
