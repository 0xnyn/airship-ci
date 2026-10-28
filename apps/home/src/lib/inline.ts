// Content text may mark code with backticks; this turns it into HTML.
const escapeHtml = (s: string): string =>
  s.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");

const TICKED = /`([^`]+)`/g;

export const inline = (text: string): string =>
  escapeHtml(text).replace(TICKED, "<code>$1</code>");
