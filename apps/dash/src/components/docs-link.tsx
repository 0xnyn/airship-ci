import { ArrowUpRight } from "lucide-react";

import { DOCS_URL, type DocsPage, docsPages } from "@/lib/docs";

/**
 * A quiet link to one documentation page, titled as the documentation titles
 * it. Detail the dashboard should not carry — how a cloud role is built, what
 * a config key means — lives behind one of these instead of in a paragraph.
 */
export function DocsLink({ page }: { page: DocsPage }) {
  return (
    <a
      className="inline-flex items-baseline gap-0.5 text-active-foreground underline-offset-2 hover:underline"
      href={`${DOCS_URL}${docsPages[page]}`}
      rel="noopener noreferrer"
      target="_blank"
    >
      {page}
      <ArrowUpRight aria-hidden="true" className="size-3 self-center" />
    </a>
  );
}
