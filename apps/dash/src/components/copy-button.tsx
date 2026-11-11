"use client";

import { Button } from "@airship/ui/components/button";
import { Check, Copy, FileCode, SquareTerminal } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

const RESET_MS = 1500;

export function CopyButton({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), RESET_MS);
    } catch {
      toast.error("Could not copy to the clipboard");
    }
  };

  return (
    <Button aria-label={label} onClick={copy} size="icon-sm" variant="ghost">
      {copied ? <Check /> : <Copy />}
    </Button>
  );
}

// A small highlighter for the YAML and shell snippets the dashboard shows:
// keys, strings, ${{ }} expressions and comments, in the landing page's code
// card colours. Anything it does not recognise stays plain.
const LINE = /^(\s*-?\s*)([\w.-]+)(:)(.*)$/;
const EXPRESSION = /(\$\{\{[^}]*\}\})/;

function Value({ text }: { text: string }) {
  const comment = text.indexOf(" #");
  const body = comment >= 0 ? text.slice(0, comment) : text;
  const note = comment >= 0 ? text.slice(comment) : "";
  return (
    <>
      {body.split(EXPRESSION).map((part, index) =>
        EXPRESSION.test(part) ? (
          // biome-ignore lint/suspicious/noArrayIndexKey: parts of one line.
          <span className="text-syntax-expression" key={index}>
            {part}
          </span>
        ) : (
          // biome-ignore lint/suspicious/noArrayIndexKey: parts of one line.
          <span className="text-syntax-string" key={index}>
            {part}
          </span>
        )
      )}
      {note ? <span className="text-syntax-comment">{note}</span> : null}
    </>
  );
}

function Highlighted({ value }: { value: string }) {
  return value.split("\n").map((line, index) => {
    const match = LINE.exec(line);
    const key = `${index}:${line}`;
    if (line.trimStart().startsWith("#")) {
      return (
        <span className="block text-syntax-comment" key={key}>
          {line || " "}
        </span>
      );
    }
    if (!match) {
      return (
        <span className="block" key={key}>
          {line.startsWith(" ") || line.startsWith("-") ? (
            <Value text={line} />
          ) : (
            line || " "
          )}
        </span>
      );
    }
    const [, lead, name, colon, rest] = match;
    return (
      <span className="block" key={key}>
        {lead}
        <span className="text-syntax-key">{name}</span>
        {colon}
        <Value text={rest ?? ""} />
      </span>
    );
  });
}

const WHITESPACE = /^\s+$/;
const SHELL_TOKEN = /("[^"]*"|'[^']*'|\s+|[^\s"']+)/;

/** Shell: the command in the key colour, flags as literals, quotes as strings. */
function Shell({ value }: { value: string }) {
  let continued = false;
  return value.split("\n").map((line, index) => {
    const key = `${index}:${line}`;
    if (line.trimStart().startsWith("#")) {
      continued = false;
      return (
        <span className="block text-syntax-comment" key={key}>
          {line}
        </span>
      );
    }
    const first = !continued;
    continued = line.trimEnd().endsWith("\\");
    let commandSeen = !first;
    return (
      <span className="block" key={key}>
        {line.split(SHELL_TOKEN).map((part, at) => {
          if (part === "") {
            return null;
          }
          const partKey = `${at}:${part}`;
          if (WHITESPACE.test(part)) {
            return part;
          }
          if (!commandSeen) {
            commandSeen = true;
            return (
              <span className="text-syntax-key" key={partKey}>
                {part}
              </span>
            );
          }
          if (part.startsWith('"') || part.startsWith("'")) {
            return (
              <span className="text-syntax-string" key={partKey}>
                {part}
              </span>
            );
          }
          if (part.startsWith("-")) {
            return (
              <span className="text-syntax-literal" key={partKey}>
                {part}
              </span>
            );
          }
          return part;
        })}
      </span>
    );
  });
}

/**
 * A block of copyable text, such as a token or a command. With a `file` it
 * is drawn as the landing page's code card: a window bar naming the file,
 * and the contents highlighted.
 */
export function CodeBlock({
  file,
  label,
  language = "yaml",
  value,
}: {
  file?: string;
  label: string;
  language?: "yaml" | "shell";
  value: string;
}) {
  if (!file) {
    return (
      <div className="relative">
        <pre className="overflow-x-auto rounded-md border bg-muted p-3 pr-10 font-mono text-xs/relaxed">
          {value}
        </pre>
        <div className="absolute top-1 right-1">
          <CopyButton label={label} value={value} />
        </div>
      </div>
    );
  }
  const FileGlyph = language === "shell" ? SquareTerminal : FileCode;
  return (
    <figure className="overflow-hidden rounded-lg border bg-card">
      <figcaption className="flex h-10 items-center gap-2 border-b bg-muted pr-1.5 pl-4">
        <FileGlyph
          aria-hidden="true"
          className="size-3.5 shrink-0 text-muted-foreground"
        />
        <span className="truncate text-muted-foreground text-xs">{file}</span>
        <span className="ml-auto">
          <CopyButton label={label} value={value} />
        </span>
      </figcaption>
      <pre className="overflow-x-auto p-4 font-mono text-[13px]/[20px] text-soft-foreground">
        <code>
          {language === "shell" ? (
            <Shell value={value} />
          ) : (
            <Highlighted value={value} />
          )}
        </code>
      </pre>
    </figure>
  );
}
