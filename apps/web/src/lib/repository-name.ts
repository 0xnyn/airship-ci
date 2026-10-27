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
