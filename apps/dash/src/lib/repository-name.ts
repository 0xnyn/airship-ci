const GITHUB_URL = /^https?:\/\/(www\.)?github\.com\//i;
const GIT_SUFFIX = /\.git$/i;
const TRAILING_SLASHES = /\/+$/;

/**
 * Reads what someone typed or pasted into the "activate a repository"
 * box: a name, owner/name, or a GitHub URL (with or without .git or a
 * trailing slash) and returns the repository's name in the organization,
 * or why it cannot be one of its repositories.
 */
export function parseRepositoryInput(
  typed: string,
  orgLogin: string
): { name: string } | { problem: string } {
  const cleaned = typed
    .trim()
    .replace(GITHUB_URL, "")
    .replace(TRAILING_SLASHES, "")
    .replace(GIT_SUFFIX, "");
  if (cleaned === "") {
    return { problem: "Type a repository name." };
  }
  const parts = cleaned.split("/").filter((part) => part !== "");
  if (parts.length > 2) {
    return { problem: `${cleaned} is not a repository name.` };
  }
  const [owner, name] =
    parts.length === 2 ? [parts[0], parts[1]] : [orgLogin, parts[0]];
  if (!(owner && name)) {
    return { problem: `${cleaned} is not a repository name.` };
  }
  if (owner.toLowerCase() !== orgLogin.toLowerCase()) {
    return { problem: `${cleaned} is not in ${orgLogin}.` };
  }
  return { name };
}

const PASTED = /[/:.]/;

/**
 * What a repository search box finds. Plain text matches anywhere in the
 * full name; a pasted owner/name or GitHub URL matches that repository
 * exactly. `missing` says why nothing could match a pasted name.
 */
export function matchRepositories<T extends { fullName: string }>(
  repositories: readonly T[],
  typed: string,
  orgLogin: string
): { items: T[]; missing: string | null } {
  const query = typed.trim();
  if (query === "") {
    return { items: [...repositories], missing: null };
  }
  if (!PASTED.test(query)) {
    const needle = query.toLowerCase();
    return {
      items: repositories.filter((repo) =>
        repo.fullName.toLowerCase().includes(needle)
      ),
      missing: null,
    };
  }
  const parsed = parseRepositoryInput(query, orgLogin);
  if ("problem" in parsed) {
    return { items: [], missing: parsed.problem };
  }
  const want = `${orgLogin}/${parsed.name}`.toLowerCase();
  const items = repositories.filter(
    (repo) => repo.fullName.toLowerCase() === want
  );
  return {
    items,
    missing:
      items.length === 0
        ? `Airship cannot see ${orgLogin}/${parsed.name}`
        : null,
  };
}
